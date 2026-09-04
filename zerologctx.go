// Package zerologctx provides a static-analysis linter that ensures zerolog
// events include a context.Context via the .Ctx(ctx) method before terminal
// operations such as Msg(), Msgf(), MsgFunc(), or Send().
//
// The analyzer recognises several ways context can be attached:
//
//   - .Ctx(ctx) directly in the Event chain:
//     log.Info().Ctx(ctx).Msg("hi")
//   - A logger built with embedded context, including loggers derived from it
//     via Logger-returning methods (With()...Logger(), Level(), Output(), ...):
//     l := log.With().Ctx(ctx).Logger(); l.Info().Msg("hi")
//   - Context propagated through assignments and aliases of Event, Logger and
//     zerolog.Context (builder) variables:
//     e := log.Info().Ctx(ctx); e.Msg("hi")
//   - A mutating statement on a tracked Event variable (zerolog Event methods
//     mutate the receiver in place):
//     e := log.Info(); e.Ctx(ctx); e.Msg("hi")
//   - Struct fields initialised by a composite literal, keyed or positional:
//     app := &App{logger: log.With().Ctx(ctx).Logger()}
//   - Custom context types satisfying context.Context (e.g. via embedding).
//
// A diagnostic is emitted only when a context is actually reachable at the
// call site — a context.Context-typed function parameter, a local variable
// declared before the call, a package-level variable, or a field of the
// enclosing method's receiver. Calls in code that has no context to pass are
// not reported.
//
// Reachability and fixability are separate: a reachable context whose name is
// shadowed at the call site is still reported, but without a suggested fix,
// since inserting the name would reference the shadowing declaration instead.
// A value whose type satisfies context.Context only through its pointer is
// suggested as &v.
//
// A //nolint:zerologctx (or //nolint:all, or bare //nolint) comment is
// honoured when it appears on one of the chain's own lines (from the chain
// start through the line of the terminal method's name) or as a standalone
// comment on the line immediately above the chain. An end-of-line comment
// that belongs to the previous statement does not apply.
//
// # Known limitations
//
// The analysis is flow-insensitive and intra-package by design:
//
//   - An assignment inside a conditional branch is treated as unconditional:
//     after `if cond { l = ctxLogger }` the analyzer assumes l has context.
//   - Struct fields are tracked per field declaration, not per instance:
//     `a.logger = ctxLogger` also marks `b.logger` for other values of the
//     same struct type. This is deliberate; see objectFromExpr. For the same
//     reason a field, like a package-level variable, is context-bearing if any
//     assignment to it carries a context, rather than by source order.
//   - Across package boundaries only exported objects carry facts, and only
//     as "was ever assigned a context", without position ordering. A fact
//     cannot be published for another package's variable assigned here, so
//     `other.Logger = ctxLogger` is invisible to a third package.
//   - Method values (`m := e.Msg; m("...")`) are not checked.
//   - Loggers and Events returned by helper functions, and loggers received
//     as function parameters, are not recognised; attach the context to the
//     Event at the call site instead.
//   - Only the canonical github.com/rs/zerolog import path is recognised;
//     forks and copies vendored under other paths are not.
//   - Contexts promoted from a struct embedded in the receiver are not seen;
//     only the receiver's own fields are.
package zerologctx

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// zerologPkgPath is the canonical import path of the zerolog library used
// for type-identity checks against zerolog's Event, Logger and Context types.
const zerologPkgPath = "github.com/rs/zerolog"

// Analyzer is the zerologctx analyzer. See its Doc field for the user-facing
// description.
var Analyzer = &analysis.Analyzer{
	Name: "zerologctx",
	Doc: `Ensures zerolog events include context via the Ctx() method.
This analyzer reports whenever a zerolog event uses terminal methods like
Msg(), Msgf(), MsgFunc() or Send() without calling Ctx(ctx) first in the
chain — but only when a context.Context is actually available at the call
site: as a function parameter, a local variable declared before the call, a
package-level variable, or a context-typed field of the method's receiver.
Calls with no reachable context are not reported.`,
	Requires:  []*analysis.Analyzer{inspect.Analyzer},
	Run:       run,
	FactTypes: []analysis.Fact{new(ctxCarrier)},
}

// ctxCarrier marks an object another package can name — an exported
// package-level variable, or an exported field of a struct — as holding a
// zerolog value with an embedded context. Without it the analysis would stop
// at the package boundary and report every use of a logger that demonstrably
// carries a context, which is the failure mode that gets a linter switched
// off.
type ctxCarrier struct{}

// AFact marks ctxCarrier as an analysis fact.
func (*ctxCarrier) AFact() {}

func (*ctxCarrier) String() string { return "carries a context" }

// terminalMethods are the *zerolog.Event methods that produce output and must
// be preceded by Ctx() somewhere in the chain. Keep in sync with zerolog's
// Event terminals; non-terminal methods (Str, Int, Dict, Discard, ...) must
// not appear here.
var terminalMethods = map[string]struct{}{
	"Msg":     {}, // log.Info().Msg("message")
	"Msgf":    {}, // log.Info().Msgf("message %d", 42)
	"MsgFunc": {}, // log.Info().MsgFunc(func() string { return "message" })
	"Send":    {}, // log.Info().Send()
}

// trackKindOf classifies a type as one of the zerolog value kinds the
// analyzer records facts for, or trackNone for everything else.
type trackKind uint8

const (
	trackNone trackKind = iota
	trackLogger
	trackEvent
	trackBuilder
)

func trackKindOf(t types.Type) trackKind {
	switch {
	case isZerologLogger(t):
		return trackLogger
	case isZerologEvent(t):
		return trackEvent
	case isZerologContext(t):
		return trackBuilder
	}
	return trackNone
}

// state holds the per-pass mutable analysis state.
type state struct {
	pass *analysis.Pass

	// contextIface is the canonical context.Context interface, found by
	// scanImports. Non-nil whenever run() proceeds past its early exit.
	contextIface *types.Interface

	// facts records, per tracked variable (locals, parameters, package-level
	// vars, struct fields), what was assigned at each source position. Keyed
	// by types.Object so different bindings with the same name in different
	// scopes do not collide. See factTable for the lookup semantics.
	facts *factTable

	// fileMap maps token.Files to the *ast.File the analyzer should scan
	// for nolint directives. Populated eagerly by buildFileMap before
	// traversal begins; never nil after a successful run().
	fileMap map[*token.File]*ast.File

	// commentIndex caches a per-file line→comments index for nolint lookups.
	commentIndex map[*ast.File]map[int][]*ast.Comment

	// srcCache caches file contents (possibly nil on read failure) used to
	// distinguish standalone comments from end-of-line ones.
	srcCache map[*token.File][]byte

	// readErr holds the first pass.ReadFile failure encountered while
	// classifying nolint comments, and readErrFile the file it happened on.
	// Surfaced by run() so a driver that cannot serve sources fails loudly
	// instead of silently degrading the documented nolint semantics.
	readErr     error
	readErrFile string

	// nilCtxVars caches the set of context variables that can only ever hold
	// nil: declared without an initializer and never assigned anywhere in the
	// package. They are neither fix candidates nor evidence that a context is
	// reachable. Built lazily by nilCtxVarSet.
	nilCtxVars map[types.Object]bool
}

// newState constructs a fresh analysis state for the given pass, including
// the token.File→ast.File map used for nolint processing.
//
// Both per-file checks fail the pass rather than skipping the file, because
// each silently disables a documented behaviour: a nil token.File means
// //nolint:zerologctx directives would be ignored, and a missing file scope
// means reachableCtx — the gate for every diagnostic — would answer "no
// context anywhere" and the analyzer would report nothing at all while
// exiting successfully.
func newState(pass *analysis.Pass, contextIface *types.Interface) (*state, error) {
	s := &state{
		pass:         pass,
		contextIface: contextIface,
		facts:        newFactTable(),
		fileMap:      make(map[*token.File]*ast.File, len(pass.Files)),
		commentIndex: make(map[*ast.File]map[int][]*ast.Comment),
		srcCache:     make(map[*token.File][]byte),
	}
	for _, f := range pass.Files {
		pf := pass.Fset.File(f.Pos())
		if pf == nil {
			return nil, fmt.Errorf("zerologctx: FileSet.File returned nil for %s; this indicates a corrupted FileSet", f.Name)
		}
		if pass.TypesInfo.Scopes[f] == nil {
			return nil, fmt.Errorf("zerologctx: no scope recorded for %s; the driver did not populate types.Info.Scopes, which the context-availability check requires", pf.Name())
		}
		s.fileMap[pf] = f
	}
	return s, nil
}

// run is the analyzer entry point.
func run(pass *analysis.Pass) (any, error) {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, fmt.Errorf("zerologctx: inspect.Analyzer result missing or wrong type")
	}

	// Packages without zerolog in their transitive import graph have nothing to
	// analyse — the common case in monorepos, and a silent skip by design. The
	// asymmetry with the loud contextIface check below is deliberate: zerolog
	// types can only reach this package through the same import graph, so if
	// the walk does not find zerolog there is nothing here to report on,
	// whereas finding zerolog without context.Context is a contradiction.
	hasZerolog, contextIface := scanImports(pass.Pkg)
	if !hasZerolog {
		return nil, nil
	}
	// zerolog itself imports "context", so with zerolog present the interface
	// must be discoverable. Failing to find it means the driver served an
	// incomplete import graph; skipping silently here would disable the
	// linter for a package that actively uses zerolog.
	if contextIface == nil {
		return nil, fmt.Errorf("zerologctx: could not locate context.Context in the import graph of %s", pass.Pkg.Path())
	}

	s, err := newState(pass, contextIface)
	if err != nil {
		return nil, err
	}

	// Phase A: collect context facts to a fixpoint, then publish the ones
	// importing packages need.
	s.collectFacts(insp)
	s.exportCtxFacts()

	// Phase B: check terminal calls.
	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		s.handleCall(n.(*ast.CallExpr))
	})

	// A failure to read sources degrades nolint classification (see
	// isStandaloneComment); make it loud so a misconfigured driver is
	// noticed instead of silently changing suppression semantics.
	if s.readErr != nil {
		return nil, fmt.Errorf("zerologctx: reading %s for nolint processing: %w", s.readErrFile, s.readErr)
	}
	return nil, nil
}

// collectFacts runs the fact-collection phase over assignments, var
// declarations and mutating Event statements, repeated to a fixpoint so facts
// that depend on other facts (aliases, package-level declarations in later
// files) propagate regardless of source order.
//
// The loop needs no pass budget. The fact lattice is finite — one entry per
// tracked object and assignment position — and factTable.set only ever ascends
// it, so each pass either changes nothing and ends the loop or consumes one of
// finitely many ascents. A dependency chain running against the traversal
// order resolves one link per pass, however deep it is.
func (s *state) collectFacts(insp *inspector.Inspector) {
	factNodes := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.ValueSpec)(nil),
		(*ast.ExprStmt)(nil),
		(*ast.CompositeLit)(nil),
	}
	for {
		before := s.facts.writes
		insp.Preorder(factNodes, func(n ast.Node) {
			switch node := n.(type) {
			case *ast.AssignStmt:
				s.handleAssign(node)
			case *ast.ValueSpec:
				s.handleValueSpec(node)
			case *ast.ExprStmt:
				s.handleExprStmt(node)
			case *ast.CompositeLit:
				s.handleCompositeLit(node)
			}
		})
		if s.facts.writes == before {
			return
		}
	}
}

// exportCtxFacts publishes a fact for every object an importing package can
// name that was ever assigned a context-bearing value. There is no negative
// fact: absence means "not known to carry a context".
//
// The position-keyed nearest-preceding lookup used within a package has no
// meaning across one: an importing package has no ordering relative to the
// declarations it imports. Any positive assignment therefore counts. That
// direction is chosen deliberately — the cost is missing a diagnostic on a
// logger that sometimes carries a context, and the alternative is reporting
// one that does.
func (s *state) exportCtxFacts() {
	for obj, entries := range s.facts.entries {
		// ExportObjectFact panics on objects this package does not own, which
		// assigning to another package's exported variable puts in the table.
		// Exported() is a name test, not a scope test — a capitalised local
		// passes it — but such a fact has no object path and is dropped when
		// the driver encodes it, so the filter only needs to be cheap.
		if obj.Pkg() != s.pass.Pkg || !obj.Exported() {
			continue
		}
		for _, hasCtx := range entries {
			if hasCtx {
				s.pass.ExportObjectFact(obj, new(ctxCarrier))
				break
			}
		}
	}
}

// scanImports walks pkg's transitive import graph once, reporting whether
// zerolog (or one of its sub-packages, e.g. zerolog/log) is imported and
// locating the standard library's context.Context interface. The walk stops
// early once both are found.
func scanImports(pkg *types.Package) (hasZerolog bool, contextIface *types.Interface) {
	if pkg == nil {
		return false, nil
	}
	seen := map[*types.Package]bool{}
	var visit func(p *types.Package)
	visit = func(p *types.Package) {
		if p == nil || seen[p] || (hasZerolog && contextIface != nil) {
			return
		}
		seen[p] = true
		switch {
		case p.Path() == zerologPkgPath || strings.HasPrefix(p.Path(), zerologPkgPath+"/"):
			hasZerolog = true
		case p.Path() == "context":
			if obj := p.Scope().Lookup("Context"); obj != nil {
				if iface, ok := obj.Type().Underlying().(*types.Interface); ok {
					contextIface = iface
				}
			}
		}
		for _, imp := range p.Imports() {
			visit(imp)
		}
	}
	visit(pkg)
	return hasZerolog, contextIface
}

// factTable records, per tracked object, which assignment positions leave it
// holding a context.
//
// Lookup has two regimes, because a source position means different things for
// different objects:
//
//   - Locals and parameters (positionOrdered): the nearest preceding
//     assignment wins, preserving reassignment order within a function body.
//     When every recorded assignment follows the use — a closure reading a
//     variable before its assignment site — the earliest is the best available
//     approximation.
//   - Struct fields and package-level variables: any context-bearing
//     assignment wins. These are written and read from unrelated places, so
//     ordering them by source position would make the verdict depend on which
//     constructor happens to be written first in the file. This is the same
//     rule exportCtxFacts applies across package boundaries.
type factTable struct {
	entries map[types.Object]map[token.Pos]bool

	// writes counts effective writes. It only ever grows, so collectFacts can
	// detect "this pass changed nothing" by comparing it before and after,
	// with no flag to reset — and therefore no way to forget to reset one and
	// leave the fixpoint loop spinning.
	writes int
}

func newFactTable() *factTable {
	return &factTable{entries: make(map[types.Object]map[token.Pos]bool)}
}

// positionOrdered reports whether nearest-preceding-assignment lookup is
// meaningful for obj. It is for locals and parameters, whose assignments and
// uses are ordered inside one function body. It is not for struct fields
// (which have no scope) or package-level variables, whose writers and readers
// sit in unrelated functions.
func positionOrdered(obj types.Object) bool {
	v, ok := obj.(*types.Var)
	if !ok || v.Pkg() == nil {
		return false
	}
	parent := v.Parent()
	return parent != nil && parent != v.Pkg().Scope()
}

// set records whether obj holds a context as of pos.
//
// Writes at one position join (logical or) rather than overwrite, because
// token.Pos does not uniquely identify a write site: Go allows the same
// assignment target twice in one statement (`l, l = ctxLogger, plain`), and an
// ExprStmt shares its position with a composite literal it starts with
// (`H{e: log.Info()}.e.Ctx(ctx)`). Treating a same-position collision as
// impossible turned both of those into a hard analyzer error on legal code.
// "Carries a context" is the right answer for a collision the analyzer cannot
// order, being the direction that stays silent.
//
// Joining also makes the table ascend monotonically by construction, which is
// what lets collectFacts iterate to a fixpoint with no pass budget.
func (t *factTable) set(obj types.Object, pos token.Pos, hasCtx bool) {
	m := t.entries[obj]
	if m == nil {
		m = make(map[token.Pos]bool)
		t.entries[obj] = m
	}
	old, seen := m[pos]
	next := old || hasCtx
	if seen && next == old {
		return
	}
	m[pos] = next
	t.writes++
}

// hasCtx reports what the table knows about obj at the given use position.
func (t *factTable) hasCtx(obj types.Object, at token.Pos) bool {
	entries := t.entries[obj]
	if len(entries) == 0 {
		return false
	}
	if !positionOrdered(obj) {
		for _, ctx := range entries {
			if ctx {
				return true
			}
		}
		return false
	}

	nearest, earliest := false, false
	var nearestPos, earliestPos token.Pos
	haveNearest, haveEarliest := false, false
	for p, ctx := range entries {
		if !haveEarliest || p < earliestPos {
			haveEarliest, earliestPos, earliest = true, p, ctx
		}
		if p < at && (!haveNearest || p > nearestPos) {
			haveNearest, nearestPos, nearest = true, p, ctx
		}
	}
	if haveNearest {
		return nearest
	}
	return earliest
}

// handleAssign records facts established by `:=` and `=` assignments. A tuple
// assignment (`a, b := fn()`) cannot be split into per-LHS facts, so it records
// a contextless fact at its own position, which supersedes earlier facts for
// uses after it without disturbing uses before it.
func (s *state) handleAssign(node *ast.AssignStmt) {
	if len(node.Lhs) != len(node.Rhs) {
		for _, lhs := range node.Lhs {
			s.clearIfTracked(lhs, node.Pos())
		}
		return
	}
	for i, lhs := range node.Lhs {
		obj := s.objectFromExpr(lhs)
		if obj == nil {
			continue
		}
		s.recordRHS(obj, node.Pos(), node.Rhs[i])
	}
}

// handleValueSpec records facts established by `var` declarations (including
// package-level vars). A multi-value spec backed by a single call is treated
// like a tuple assignment.
func (s *state) handleValueSpec(node *ast.ValueSpec) {
	if len(node.Values) == 0 {
		return
	}
	if len(node.Names) != len(node.Values) {
		for _, name := range node.Names {
			if obj := s.pass.TypesInfo.Defs[name]; obj != nil && trackKindOf(obj.Type()) != trackNone {
				s.facts.set(obj, node.Pos(), false)
			}
		}
		return
	}
	for i, name := range node.Names {
		obj := s.pass.TypesInfo.Defs[name]
		if obj == nil {
			continue
		}
		s.recordRHS(obj, node.Pos(), node.Values[i])
	}
}

// handleExprStmt records the fact established by a mutating statement such as
// `e.Ctx(ctx)`: zerolog Event methods mutate the receiver in place and return
// it, so a discarded chain still attaches the context to the root variable.
func (s *state) handleExprStmt(node *ast.ExprStmt) {
	call, ok := ast.Unparen(node.X).(*ast.CallExpr)
	if !ok {
		return
	}
	if !isZerologEvent(s.pass.TypesInfo.TypeOf(call)) {
		return
	}
	if !s.eventHasCtx(call, node.Pos()) {
		return
	}
	root := s.chainRootObject(call)
	if root == nil || trackKindOf(root.Type()) != trackEvent {
		return
	}
	s.facts.set(root, node.Pos(), true)
}

// handleCompositeLit records the facts established by struct literal field
// initialisation, `App{logger: ctxLogger}`, in both keyed and positional form.
// Without it, a logger installed at construction time — the most natural way
// to give a struct a context-bearing logger — produced a false positive on
// every use of that field.
//
// Fields are keyed by their declaration, exactly as an `app.logger = ...`
// assignment is, so both forms feed the same fact. See the note on
// objectFromExpr for why field facts are deliberately not per-instance.
func (s *state) handleCompositeLit(node *ast.CompositeLit) {
	typ := s.pass.TypesInfo.TypeOf(node)
	if typ == nil {
		return
	}
	st, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return
	}
	for i, elt := range node.Elts {
		field, value := s.compositeLitField(st, i, elt)
		if field == nil {
			continue
		}
		s.recordRHS(field, node.Pos(), value)
	}
}

// compositeLitField resolves one composite-literal element to the field it
// initialises and the expression it initialises it with. Returns a nil field
// for anything it cannot resolve (a non-identifier key, a positional element
// past the end of the struct).
func (s *state) compositeLitField(st *types.Struct, i int, elt ast.Expr) (*types.Var, ast.Expr) {
	kv, keyed := elt.(*ast.KeyValueExpr)
	if !keyed {
		if i >= st.NumFields() {
			return nil, nil
		}
		return st.Field(i), elt
	}
	key, ok := kv.Key.(*ast.Ident)
	if !ok {
		return nil, nil
	}
	field, ok := s.pass.TypesInfo.ObjectOf(key).(*types.Var)
	if !ok {
		return nil, nil
	}
	return field, kv.Value
}

// recordRHS classifies a right-hand-side expression for the given target
// object. Reassignment to a value without context records factNone, which
// supersedes any earlier positive fact at later use positions.
func (s *state) recordRHS(obj types.Object, pos token.Pos, rhs ast.Expr) {
	tk := trackKindOf(obj.Type())
	if tk == trackNone {
		return
	}
	s.facts.set(obj, pos, s.exprHasCtx(tk, rhs, pos))
}

// exprHasCtx dispatches to the category-specific context predicate.
func (s *state) exprHasCtx(tk trackKind, expr ast.Expr, at token.Pos) bool {
	switch tk {
	case trackLogger:
		return s.loggerHasCtx(expr, at)
	case trackEvent:
		return s.eventHasCtx(expr, at)
	case trackBuilder:
		return s.builderHasCtx(expr, at)
	}
	return false
}

// clearIfTracked records factNone for an assignment target whose type the
// analyzer tracks (used for tuple assignments, where the RHS value cannot be
// classified per target).
func (s *state) clearIfTracked(lhs ast.Expr, pos token.Pos) {
	obj := s.objectFromExpr(lhs)
	if obj == nil || trackKindOf(obj.Type()) == trackNone {
		return
	}
	s.facts.set(obj, pos, false)
}

// chainRootObject walks a fluent call chain to its base expression and
// resolves the variable it is rooted at, e.g. `e` for `e.Str("k","v").Ctx(c)`.
// Returns nil when the base is not a plain identifier or field selector.
func (s *state) chainRootObject(expr ast.Expr) types.Object {
	for {
		expr = ast.Unparen(expr)
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return s.objectFromExpr(expr)
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil
		}
		expr = sel.X
	}
}

// handleCall checks a CallExpr to see whether it is a terminal zerolog call
// (Msg/Msgf/MsgFunc/Send) on an *Event that lacks an upstream Ctx(ctx).
func (s *state) handleCall(node *ast.CallExpr) {
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if _, terminal := terminalMethods[sel.Sel.Name]; !terminal {
		return
	}
	recvType := s.pass.TypesInfo.TypeOf(sel.X)
	if recvType == nil || !isZerologEvent(recvType) {
		return
	}
	if s.eventHasCtx(sel.X, node.Pos()) {
		return
	}
	if s.hasNoLintDirective(node, sel.Sel.Pos()) {
		return
	}

	// With the real zerolog API only an untyped nil can reach a Ctx() call
	// without satisfying context.Context; give it a message that does not
	// falsely claim the Ctx() call is missing.
	if s.chainHasNonCtxArg(sel.X) {
		s.pass.Report(analysis.Diagnostic{
			Pos: node.Pos(),
			Message: fmt.Sprintf(
				"zerolog event calls Ctx() with a non-context argument before %s() - pass a context.Context for proper log correlation",
				sel.Sel.Name,
			),
		})
		return
	}

	// Report only when a context is actually reachable at the call site — as
	// a scope variable or a receiver field. When there is nothing to pass,
	// there is nothing to fix, so stay silent.
	ctxExpr, reachable := s.reachableCtx(node.Pos())
	if !reachable {
		return
	}
	diag := analysis.Diagnostic{
		Pos: node.Pos(),
		Message: fmt.Sprintf(
			"zerolog event missing .Ctx(ctx) before %s() - context should be included for proper log correlation",
			sel.Sel.Name,
		),
	}
	// A reachable context that cannot be named here (shadowed by another
	// declaration) still deserves the diagnostic, but not a fix that would
	// insert the wrong value.
	if ctxExpr != "" {
		diag.SuggestedFixes = []analysis.SuggestedFix{{
			Message: fmt.Sprintf("Insert .Ctx(%s) before %s()", ctxExpr, sel.Sel.Name),
			TextEdits: []analysis.TextEdit{{
				Pos:     sel.Sel.Pos(),
				End:     sel.Sel.Pos(),
				NewText: []byte("Ctx(" + ctxExpr + ")."),
			}},
		}}
	}
	s.pass.Report(diag)
}

// eventHasCtx reports whether expr — an expression of type *zerolog.Event —
// carries a context: via an inline Ctx(ctx) call in its chain, via a tracked
// Event variable at its root, or by originating from a context-bearing
// logger. The walk is type-driven, so every Event-producing Logger method
// (Info, Error, Err, WithLevel, ...) is covered without a method whitelist.
func (s *state) eventHasCtx(expr ast.Expr, at token.Pos) bool {
	expr = ast.Unparen(expr)
	if call, ok := expr.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		recv := s.pass.TypesInfo.TypeOf(sel.X)
		switch {
		case isZerologEvent(recv):
			// Event.Ctx(ctx) attaches the context. The Event-receiver check
			// preserves the load-bearing distinction from Logger lookups like
			// log.Ctx(ctx), which do NOT attach context to created events.
			if sel.Sel.Name == "Ctx" && s.callArgIsContext(call) {
				return true
			}
			return s.eventHasCtx(sel.X, at)
		case isZerologLogger(recv):
			return s.loggerHasCtx(sel.X, at)
		}
		return false
	}
	return s.factIs(expr, at, trackEvent)
}

// loggerHasCtx reports whether expr — an expression of type zerolog.Logger or
// *zerolog.Logger — has an embedded context: a With()...Ctx(ctx)...Logger()
// construction chain, a tracked logger variable, or a Logger-returning
// derivation (Level, Output, Sample, ...) of either.
func (s *state) loggerHasCtx(expr ast.Expr, at token.Pos) bool {
	expr = ast.Unparen(expr)
	switch x := expr.(type) {
	case *ast.StarExpr: // (*holder.Logger).Info()
		return s.loggerHasCtx(x.X, at)
	case *ast.UnaryExpr: // &logger
		if x.Op == token.AND {
			return s.loggerHasCtx(x.X, at)
		}
		return false
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		recv := s.pass.TypesInfo.TypeOf(sel.X)
		switch {
		case isZerologContext(recv):
			// builder.Logger()
			return s.builderHasCtx(sel.X, at)
		case isZerologLogger(recv):
			// Logger-to-Logger derivation keeps the embedded context.
			return s.loggerHasCtx(sel.X, at)
		}
		return false
	}
	return s.factIs(expr, at, trackLogger)
}

// builderHasCtx reports whether expr — an expression of type zerolog.Context
// (the builder) — has Ctx(ctx) applied. The Context-receiver requirement on
// the Ctx call is what keeps Logger lookups such as log.Ctx(ctx) or
// zerolog.Ctx(ctx) from counting: their receiver is a package, not a builder.
func (s *state) builderHasCtx(expr ast.Expr, at token.Pos) bool {
	expr = ast.Unparen(expr)
	if call, ok := expr.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		recv := s.pass.TypesInfo.TypeOf(sel.X)
		switch {
		case isZerologContext(recv):
			if sel.Sel.Name == "Ctx" && s.callArgIsContext(call) {
				return true
			}
			return s.builderHasCtx(sel.X, at)
		case isZerologLogger(recv):
			// logger.With() — a builder seeded from the logger, inheriting
			// its embedded context.
			return s.loggerHasCtx(sel.X, at)
		}
		return false
	}
	return s.factIs(expr, at, trackBuilder)
}

// factIs reports whether expr resolves to a tracked variable of category tk
// that carries a context at the given position. Shared base case of the three
// predicates; the tk check is what keeps a Logger fact from answering an Event
// question, which is the correspondence the three predicates rely on.
//
// For an object owned by another package the local table says nothing — this
// package never saw its assignments — so the imported ctxCarrier fact answers
// instead.
func (s *state) factIs(expr ast.Expr, at token.Pos, tk trackKind) bool {
	obj := s.objectFromExpr(expr)
	if obj == nil || trackKindOf(obj.Type()) != tk {
		return false
	}
	if obj.Pkg() != nil && obj.Pkg() != s.pass.Pkg {
		return s.pass.ImportObjectFact(obj, new(ctxCarrier))
	}
	return s.facts.hasCtx(obj, at)
}

// callArgIsContext reports whether the call's first argument satisfies
// context.Context as written. The check is exact rather than "could be passed
// after taking its address": the compiler has already accepted the argument,
// so anything looser would only mask a genuinely non-context value.
func (s *state) callArgIsContext(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	return s.implementsContext(s.pass.TypesInfo.TypeOf(call.Args[0]))
}

// chainHasNonCtxArg reports whether the Event chain contains a Ctx() call on
// an Event receiver whose argument does not satisfy context.Context (with the
// real zerolog API this means an untyped nil).
func (s *state) chainHasNonCtxArg(expr ast.Expr) bool {
	for {
		expr = ast.Unparen(expr)
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "Ctx" && isZerologEvent(s.pass.TypesInfo.TypeOf(sel.X)) && !s.callArgIsContext(call) {
			return true
		}
		expr = sel.X
	}
}

// isContextType reports whether a value of typ can be handed to Ctx(), either
// as written or by taking its address. This is the reachability question — is
// there a context here at all — and is deliberately wider than
// implementsContext; use ctxExpr to render the value, since the two cases need
// different syntax.
func (s *state) isContextType(typ types.Type) bool {
	return s.implementsContext(typ) || s.addressableContext(typ)
}

// implementsContext reports whether typ itself satisfies context.Context
// (directly or via a custom type that embeds it).
func (s *state) implementsContext(typ types.Type) bool {
	return typ != nil && s.contextIface != nil && types.Implements(typ, s.contextIface)
}

// addressableContext reports whether typ does not satisfy context.Context but
// *typ does — the shape of a custom context type whose methods use pointer
// receivers. Such a value can only be passed as &v, so treating it like a
// plain context (as an earlier version of isContextType did) produced
// suggested fixes that did not compile.
func (s *state) addressableContext(typ types.Type) bool {
	if typ == nil || s.contextIface == nil {
		return false
	}
	if _, isPtr := typ.(*types.Pointer); isPtr {
		return false
	}
	return !types.Implements(typ, s.contextIface) &&
		types.Implements(types.NewPointer(typ), s.contextIface)
}

// ctxExpr renders name as an expression of type context.Context: the name
// itself, or its address when only the pointer type satisfies the interface.
// Reports false when the value is not a context at all.
func (s *state) ctxExpr(name string, typ types.Type) (string, bool) {
	switch {
	case s.implementsContext(typ):
		return name, true
	case s.addressableContext(typ):
		return "&" + name, true
	}
	return "", false
}

// isZerologNamed reports whether t (or its pointer element) is the named type
// zerologPkgPath.name. Comparing the defining package path avoids matching
// similarly named types from forks or unrelated packages.
func isZerologNamed(t types.Type, name string) bool {
	if t == nil {
		return false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Name() == name && obj.Pkg().Path() == zerologPkgPath
}

func isZerologEvent(t types.Type) bool   { return isZerologNamed(t, "Event") }
func isZerologLogger(t types.Type) bool  { return isZerologNamed(t, "Logger") }
func isZerologContext(t types.Type) bool { return isZerologNamed(t, "Context") }

// objectFromExpr resolves the types.Object behind a bare identifier or a
// selector expression (struct field, package-qualified variable). Returns nil
// for any other shape.
//
// A struct field resolves to its declaration, so `a.logger` and `b.logger`
// share one fact. That is a deliberate choice, not an oversight: keying facts
// per instance would break the dominant shape, where a constructor fills the
// field on one variable and methods read it through their own receiver, which
// is a different object. Trading that false negative for a false positive on
// ordinary constructor code is the wrong direction for a linter.
func (s *state) objectFromExpr(expr ast.Expr) types.Object {
	switch x := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return s.pass.TypesInfo.ObjectOf(x)
	case *ast.SelectorExpr:
		if sel, ok := s.pass.TypesInfo.Selections[x]; ok {
			return sel.Obj()
		}
		return s.pass.TypesInfo.ObjectOf(x.Sel)
	}
	return nil
}

// hasNoLintDirective reports whether a //nolint comment suppressing
// zerologctx applies to the given call: a directive on any of the chain's own
// lines (chain start through the line of the terminal method's name, covering
// both single-line calls and multi-line fluent chains), or a standalone
// comment on the line immediately above the chain. An end-of-line comment
// trailing the previous statement is deliberately not honoured — it belongs
// to that statement.
func (s *state) hasNoLintDirective(call *ast.CallExpr, terminalPos token.Pos) bool {
	// Positions that cannot be matched to an analysed file — cgo-remapped
	// positions are the only realistic case after newState verified the
	// FileSet — are treated as carrying no directive. reachableCtx bails out on
	// the same conditions, so such a call ends up silent rather than reported;
	// what this avoids is honouring or dropping a nolint by guesswork.
	tokFile := s.pass.Fset.File(call.Pos())
	if tokFile == nil {
		return false
	}
	astFile := s.fileFor(tokFile)
	if astFile == nil {
		return false
	}

	chainStart := tokFile.Line(call.Pos())
	terminalLine := tokFile.Line(terminalPos)
	byLine := s.commentsByLine(astFile, tokFile)

	for line := chainStart; line <= terminalLine; line++ {
		for _, c := range byLine[line] {
			if isNoLintComment(c.Text, "zerologctx") {
				return true
			}
		}
	}
	for _, c := range byLine[chainStart-1] {
		if s.isStandaloneComment(tokFile, c) && isNoLintComment(c.Text, "zerologctx") {
			return true
		}
	}
	return false
}

// commentsByLine returns (building and caching on first use) a line-indexed
// view of the file's comments, so each diagnostic checks only the handful of
// lines it cares about instead of scanning every comment in the file.
func (s *state) commentsByLine(astFile *ast.File, tokFile *token.File) map[int][]*ast.Comment {
	if idx, ok := s.commentIndex[astFile]; ok {
		return idx
	}
	idx := make(map[int][]*ast.Comment)
	for _, cg := range astFile.Comments {
		for _, c := range cg.List {
			line := tokFile.Line(c.Pos())
			idx[line] = append(idx[line], c)
		}
	}
	s.commentIndex[astFile] = idx
	return idx
}

// isStandaloneComment reports whether the comment is the first thing on its
// line (only whitespace before it). When the source cannot be read the
// comment is treated as standalone, erring on the side of honouring nolint.
func (s *state) isStandaloneComment(tokFile *token.File, c *ast.Comment) bool {
	src := s.sourceFor(tokFile)
	if src == nil {
		return true
	}
	line := tokFile.Line(c.Pos())
	start := tokFile.Offset(tokFile.LineStart(line))
	end := tokFile.Offset(c.Pos())
	if start < 0 || end > len(src) || start > end {
		return true
	}
	for _, b := range src[start:end] {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	return true
}

// sourceFor returns the file's contents, caching the result (including
// failures, cached as nil) per token.File. The first read failure is kept in
// s.readErr and surfaced by run(), so a driver that cannot serve sources is
// noticed instead of silently falling back on nolint classification.
func (s *state) sourceFor(tokFile *token.File) []byte {
	if src, ok := s.srcCache[tokFile]; ok {
		return src
	}
	src, err := s.pass.ReadFile(tokFile.Name())
	if err != nil {
		if s.readErr == nil {
			s.readErr, s.readErrFile = err, tokFile.Name()
		}
		src = nil
	}
	s.srcCache[tokFile] = src
	return src
}

// fileFor returns the *ast.File for a token.File, or nil if the file is not
// in the analysed set (e.g. an imported file whose positions happen to fall
// in the same FileSet). newState populates fileMap before any use.
func (s *state) fileFor(tf *token.File) *ast.File {
	return s.fileMap[tf]
}

// isNoLintComment reports whether a comment is a nolint directive that
// applies to linterName. It accepts:
//
//   - //nolint                       (bare, suppresses all linters)
//   - //nolint:all                   (explicit "all")
//   - //nolint:zerologctx
//   - // nolint: zerologctx          (whitespace variants)
//   - //nolint:l1,zerologctx,l2      (comma-separated lists)
//   - //nolint:zerologctx // reason  (trailing reason after second //)
func isNoLintComment(commentText, linterName string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(commentText, "//"))
	// Strip any trailing reason that uses a second // separator.
	if idx := strings.Index(text, "//"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	if !strings.HasPrefix(text, "nolint") {
		return false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "nolint"))
	if text == "" {
		// Bare //nolint suppresses all linters, mirroring golangci-lint.
		return true
	}
	if !strings.HasPrefix(text, ":") {
		return false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, ":"))
	if text == "" {
		return false
	}
	for linter := range strings.SplitSeq(text, ",") {
		l := strings.TrimSpace(linter)
		if l == linterName || l == "all" {
			return true
		}
	}
	return false
}

// nilCtxVarSet returns (building lazily) the set of context variables that can
// only ever hold nil: declared without an initializer, e.g.
// `var c context.Context`, and never assigned anywhere in the package. Such a
// variable is not a context that can be passed, so it neither answers the
// reachability question nor makes a usable fix.
//
// The absence of an initializer alone is not enough: `var ctx context.Context`
// followed by `ctx = ...` is an ordinary context, and treating it as nil used
// to suppress the diagnostic entirely. Taking a variable's address counts as
// an assignment, since the callee may write through the pointer.
//
// The context-type filter is applied here rather than left to the caller so
// that membership means what the name says: for a non-nilable type such as
// zerolog.Logger, "declared without an initializer" would mean "zero value",
// not "nil".
func (s *state) nilCtxVarSet() map[types.Object]bool {
	if s.nilCtxVars != nil {
		return s.nilCtxVars
	}
	s.nilCtxVars = make(map[types.Object]bool)
	assigned := make(map[types.Object]bool)
	markAssigned := func(expr ast.Expr) {
		if obj := s.objectFromExpr(expr); obj != nil {
			assigned[obj] = true
		}
	}
	for _, f := range s.pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ValueSpec:
				if len(node.Values) != 0 {
					return true
				}
				for _, name := range node.Names {
					if obj := s.pass.TypesInfo.Defs[name]; obj != nil && s.isContextType(obj.Type()) {
						s.nilCtxVars[obj] = true
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range node.Lhs {
					markAssigned(lhs)
				}
			case *ast.RangeStmt:
				for _, lhs := range []ast.Expr{node.Key, node.Value} {
					if lhs != nil {
						markAssigned(lhs)
					}
				}
			case *ast.UnaryExpr:
				if node.Op == token.AND {
					markAssigned(node.X)
				}
			}
			return true
		})
	}
	for obj := range assigned {
		delete(s.nilCtxVars, obj)
	}
	return s.nilCtxVars
}

// reachableCtx answers two separate questions about the call site at pos:
// whether a context.Context is reachable there at all — the gate for reporting
// a missing-Ctx diagnostic — and, when one can also be referenced safely, the
// expression a suggested fix should insert.
//
// The two are deliberately not the same question. A context shadowed at the
// call site is reachable (renaming the shadow makes it usable), so the
// diagnostic stands, but writing its name into a fix would silently retarget
// the call, so no fix is offered. A blank field, by contrast, can never be
// referenced and is therefore not reachable at all.
//
// Candidates are ranked as follows: a variable literally named "ctx" wins,
// even from an outer scope; otherwise the nearest preceding candidate in the
// innermost scope that has one. Package-level candidates are usable regardless
// of declaration order. Variables stuck at nil are skipped. When no scope
// variable qualifies, a context-typed field of the enclosing method's receiver
// is the last resort.
func (s *state) reachableCtx(pos token.Pos) (string, bool) {
	if s.contextIface == nil {
		return "", false
	}
	tokFile := s.pass.Fset.File(pos)
	if tokFile == nil {
		return "", false
	}
	astFile := s.fileFor(tokFile)
	if astFile == nil {
		return "", false
	}
	fileScope := s.pass.TypesInfo.Scopes[astFile]
	if fileScope == nil {
		return "", false
	}
	scope := fileScope.Innermost(pos)

	nilCtxVars := s.nilCtxVarSet()
	pkgScope := s.pass.Pkg.Scope()
	// candidate reports whether v holds a context that exists at pos.
	// Package-level variables may be referenced regardless of their
	// declaration order; locals only after their declaration.
	candidate := func(v *types.Var, sc *types.Scope) bool {
		if nilCtxVars[v] || !s.isContextType(v.Type()) {
			return false
		}
		return sc == pkgScope || v.Pos() < pos
	}

	reachable := false
	best := ""
	var bestPos token.Pos
	bestPreceding := false
	for sc := scope; sc != nil && sc != types.Universe; sc = sc.Parent() {
		// Prefer a variable literally named "ctx", even from an outer scope.
		if v, ok := sc.Lookup("ctx").(*types.Var); ok && candidate(v, sc) {
			reachable = true
			if expr, ok := s.fixExprFor("ctx", v, scope, pos); ok {
				return expr, true
			}
		}
		if best != "" {
			continue
		}
		// Pick the nearest preceding candidate in this scope; fall back to
		// any candidate for order-independent (package) scope.
		for _, name := range sc.Names() {
			v, ok := sc.Lookup(name).(*types.Var)
			if !ok || !candidate(v, sc) {
				continue
			}
			reachable = true
			expr, ok := s.fixExprFor(name, v, scope, pos)
			if !ok {
				continue
			}
			preceding := v.Pos() < pos
			switch {
			case best == "",
				preceding && !bestPreceding,
				preceding == bestPreceding && preceding && v.Pos() > bestPos:
				best, bestPos, bestPreceding = expr, v.Pos(), preceding
			}
		}
	}
	if best != "" {
		return best, true
	}
	fieldExpr, fieldReachable := s.receiverCtx(astFile, scope, pos)
	return fieldExpr, reachable || fieldReachable
}

// fixExprFor renders v as the expression a suggested fix would insert at pos,
// reporting false when it cannot be written safely there. The name must still
// denote v at pos — an inner declaration of the same name would silently
// retarget the inserted expression into a compile error — and the value has to
// be renderable as a context.Context.
func (s *state) fixExprFor(name string, v *types.Var, scope *types.Scope, pos token.Pos) (string, bool) {
	if _, obj := scope.LookupParent(name, pos); obj != v {
		return "", false
	}
	return s.ctxExpr(name, v.Type())
}

// receiverCtx looks for a context-typed field on the receiver of the method
// enclosing pos. It returns the expression a fix should insert ("recv.field",
// or "&recv.field" when only the pointer type satisfies context.Context) and
// whether such a field exists at all — the same reachable/fixable split as
// reachableCtx. Calls inside a FuncLit nested in a method still resolve to
// that method's receiver. Only direct struct fields are considered, not fields
// promoted from embedded structs; an embedded context.Context itself counts
// (as "recv.Context"). A blank field can never be referenced, so it is not
// reachable; a receiver name taken over by a local is reachable but not
// fixable.
func (s *state) receiverCtx(astFile *ast.File, scope *types.Scope, pos token.Pos) (string, bool) {
	for _, decl := range astFile.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || pos < fd.Pos() || pos >= fd.End() {
			continue
		}
		if len(fd.Recv.List) != 1 || len(fd.Recv.List[0].Names) != 1 {
			return "", false
		}
		recvIdent := fd.Recv.List[0].Names[0]
		if recvIdent.Name == "_" {
			return "", false
		}
		obj := s.pass.TypesInfo.Defs[recvIdent]
		if obj == nil {
			return "", false
		}
		t := obj.Type()
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return "", false
		}
		_, named := scope.LookupParent(recvIdent.Name, pos)
		for f := range st.Fields() {
			// A blank field cannot be referenced by any expression, so its
			// context is not reachable through the receiver.
			if f.Name() == "_" {
				continue
			}
			expr, ok := s.ctxExpr(recvIdent.Name+"."+f.Name(), f.Type())
			if !ok {
				continue
			}
			// The field exists either way; the fix only holds while the
			// receiver name still denotes the receiver at pos.
			if named != obj {
				return "", true
			}
			return expr, true
		}
		return "", false
	}
	return "", false
}
