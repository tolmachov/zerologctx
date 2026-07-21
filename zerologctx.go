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
//   - Custom context types satisfying context.Context (e.g. via embedding).
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
//     same struct type.
//   - Composite-literal initialisation (`App{logger: ctxLogger}`) is not
//     tracked.
//   - Facts do not cross package boundaries (no analysis.Facts): an exported
//     context-bearing logger declared in another package is not recognised.
//   - Method values (`m := e.Msg; m("...")`) are not checked.
//   - Loggers and Events returned by helper functions, and loggers received
//     as function parameters, are not recognised; attach the context to the
//     Event at the call site instead.
//   - Only the canonical github.com/rs/zerolog import path is recognised;
//     forks and copies vendored under other paths are not.
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
Msg(), Msgf(), MsgFunc() or Send() without calling Ctx(ctx) first in the chain.`,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

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

// factKind describes what the analyzer knows about a tracked variable at a
// given assignment site.
type factKind uint8

const (
	// factNone: the variable was (re)assigned a value without context.
	factNone factKind = iota
	// factLoggerCtx: a zerolog.Logger with an embedded context.
	factLoggerCtx
	// factBuilderCtx: a zerolog.Context builder that has Ctx(ctx) applied.
	factBuilderCtx
	// factEventCtx: a *zerolog.Event with Ctx(ctx) somewhere upstream.
	factEventCtx
)

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

// positiveFactFor maps a track category to the positive fact kind a variable
// of that category may carry. The two enums stay in one-to-one correspondence
// through this function, and factTable.set enforces it.
func positiveFactFor(k trackKind) factKind {
	switch k {
	case trackLogger:
		return factLoggerCtx
	case trackEvent:
		return factEventCtx
	case trackBuilder:
		return factBuilderCtx
	}
	return factNone
}

// maxFactPasses bounds the fact-collection fixpoint loop. Facts only move
// from factNone to a positive kind (the predicates are monotone in the fact
// table), so the loop terminates naturally; each pass resolves at least one
// more link of an out-of-source-order dependency chain, and chains deeper
// than this are reported by collectFacts as an error instead of silently
// truncating the analysis into false positives.
const maxFactPasses = 10

// state holds the per-pass mutable analysis state.
type state struct {
	pass *analysis.Pass

	// contextIface is the canonical context.Context interface, found by
	// scanImports. Non-nil whenever run() proceeds past its early exit.
	contextIface *types.Interface

	// facts records, per tracked variable (locals, parameters, package-level
	// vars, struct fields), what was assigned at each source position. Keyed
	// by *types.Object so different bindings with the same name in different
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
	// classifying nolint comments. Surfaced by run() so a driver that cannot
	// serve sources fails loudly instead of silently degrading the
	// documented nolint semantics.
	readErr error

	// noInitVars caches the set of variables declared without an initializer
	// (`var c context.Context`); such variables make poor suggested-fix
	// candidates. Built lazily by noInitVarSet.
	noInitVars map[types.Object]bool
}

// newState constructs a fresh analysis state for the given pass, including
// the token.File→ast.File map used for nolint processing. Failing on a nil
// token.File surfaces FileSet corruption immediately rather than silently
// skipping files later, which would cause //nolint:zerologctx directives to
// be unexpectedly ignored.
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

	// Packages without zerolog in their transitive import graph have nothing
	// to analyse — the common case in monorepos, and a silent skip by design.
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

	// Phase A: collect context facts to a fixpoint.
	if err := s.collectFacts(insp); err != nil {
		return nil, err
	}

	// Phase B: check terminal calls.
	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		s.handleCall(n.(*ast.CallExpr))
	})

	// A failure to read sources degrades nolint classification (see
	// isStandaloneComment); make it loud so a misconfigured driver is
	// noticed instead of silently changing suppression semantics.
	if s.readErr != nil {
		return nil, fmt.Errorf("zerologctx: reading source for nolint processing: %w", s.readErr)
	}
	return nil, nil
}

// collectFacts runs the fact-collection phase over assignments, var
// declarations and mutating Event statements, repeated to a fixpoint so facts
// that depend on other facts (aliases, package-level declarations in later
// files) propagate regardless of source order. Hitting maxFactPasses means an
// out-of-source-order dependency chain deeper than the cap (or a broken
// monotonicity invariant after a future change); both must be loud, since a
// silently truncated fact table produces baffling false positives.
func (s *state) collectFacts(insp *inspector.Inspector) error {
	factNodes := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.ValueSpec)(nil),
		(*ast.ExprStmt)(nil),
	}
	for range maxFactPasses {
		s.facts.dirty = false
		insp.Preorder(factNodes, func(n ast.Node) {
			switch node := n.(type) {
			case *ast.AssignStmt:
				s.handleAssign(node)
			case *ast.ValueSpec:
				s.handleValueSpec(node)
			case *ast.ExprStmt:
				s.handleExprStmt(node)
			}
		})
		if !s.facts.dirty {
			return nil
		}
	}
	return fmt.Errorf("zerologctx: fact propagation did not converge after %d passes", maxFactPasses)
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

// factTable records per-object, position-keyed context facts. Lookup follows
// nearest-preceding-assignment semantics: the last assignment before the use
// position wins, preserving source-order reassignment behaviour; when every
// recorded assignment follows the use (a package-level var declared in a
// later file, or a closure using a variable before its assignment site), the
// earliest one is used as the best available approximation.
type factTable struct {
	entries map[types.Object]map[token.Pos]factKind

	// dirty is set by set when a collection pass learns something new; the
	// fixpoint loop in collectFacts stops when a full pass leaves it false.
	dirty bool
}

func newFactTable() *factTable {
	return &factTable{entries: make(map[types.Object]map[token.Pos]factKind)}
}

// set records what a tracked variable holds as of the given position. Writes
// whose kind does not match the object's track category (the positiveFactFor
// correspondence) are rejected: they would corrupt lookups that compare
// against a specific kind.
func (t *factTable) set(obj types.Object, pos token.Pos, kind factKind) {
	if kind != factNone && kind != positiveFactFor(trackKindOf(obj.Type())) {
		return
	}
	m := t.entries[obj]
	if m == nil {
		m = make(map[token.Pos]factKind)
		t.entries[obj] = m
	}
	if old, ok := m[pos]; !ok || old != kind {
		m[pos] = kind
		t.dirty = true
	}
}

// at returns what the table knows about obj at the given use position.
func (t *factTable) at(obj types.Object, at token.Pos) factKind {
	entries := t.entries[obj]
	if len(entries) == 0 {
		return factNone
	}
	nearest, earliest := factNone, factNone
	var nearestPos, earliestPos token.Pos
	haveNearest, haveEarliest := false, false
	for p, k := range entries {
		if !haveEarliest || p < earliestPos {
			haveEarliest, earliestPos, earliest = true, p, k
		}
		if p < at && (!haveNearest || p > nearestPos) {
			haveNearest, nearestPos, nearest = true, p, k
		}
	}
	if haveNearest {
		return nearest
	}
	return earliest
}

// handleAssign records facts established by `:=` and `=` assignments. A
// tuple assignment (`a, b := fn()`) cannot be split into per-LHS facts, but
// it still invalidates any previously recorded fact for its targets.
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
				s.facts.set(obj, node.Pos(), factNone)
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
	s.facts.set(root, node.Pos(), factEventCtx)
}

// recordRHS classifies a right-hand-side expression for the given target
// object. Reassignment to a value without context records factNone, which
// supersedes any earlier positive fact at later use positions.
func (s *state) recordRHS(obj types.Object, pos token.Pos, rhs ast.Expr) {
	tk := trackKindOf(obj.Type())
	if tk == trackNone {
		return
	}
	kind := factNone
	if s.exprHasCtx(tk, rhs, pos) {
		kind = positiveFactFor(tk)
	}
	s.facts.set(obj, pos, kind)
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
	s.facts.set(obj, pos, factNone)
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

	diag := analysis.Diagnostic{
		Pos: node.Pos(),
		Message: fmt.Sprintf(
			"zerolog event missing .Ctx(ctx) before %s() - context should be included for proper log correlation",
			sel.Sel.Name,
		),
	}
	if ctxName, ok := s.findCtxInScope(node.Pos()); ok {
		diag.SuggestedFixes = []analysis.SuggestedFix{{
			Message: fmt.Sprintf("Insert .Ctx(%s) before %s()", ctxName, sel.Sel.Name),
			TextEdits: []analysis.TextEdit{{
				Pos:     sel.Sel.Pos(),
				End:     sel.Sel.Pos(),
				NewText: []byte("Ctx(" + ctxName + ")."),
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
	return s.factIs(expr, at, factEventCtx)
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
	return s.factIs(expr, at, factLoggerCtx)
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
	return s.factIs(expr, at, factBuilderCtx)
}

// factIs reports whether expr resolves to a tracked variable whose fact at
// the given position is exactly kind. Shared base case of the three
// predicates, making the predicate↔fact-kind correspondence explicit.
func (s *state) factIs(expr ast.Expr, at token.Pos, kind factKind) bool {
	obj := s.objectFromExpr(expr)
	return obj != nil && s.facts.at(obj, at) == kind
}

// callArgIsContext reports whether the call's first argument satisfies
// context.Context.
func (s *state) callArgIsContext(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	argType := s.pass.TypesInfo.TypeOf(call.Args[0])
	return argType != nil && s.isContextType(argType)
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

// isContextType reports whether typ satisfies context.Context (directly or
// via a custom type that embeds it).
func (s *state) isContextType(typ types.Type) bool {
	if typ == nil || s.contextIface == nil {
		return false
	}
	if types.Implements(typ, s.contextIface) {
		return true
	}
	// Also try the pointer type: a type whose methods use pointer receivers
	// implements the interface only through *T.
	if _, isPtr := typ.(*types.Pointer); !isPtr {
		if types.Implements(types.NewPointer(typ), s.contextIface) {
			return true
		}
	}
	return false
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

// objectFromExpr resolves the *types.Object behind a bare identifier or a
// selector expression (struct field, package-qualified variable). Returns nil
// for any other shape.
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
	// Positions that cannot be matched to an analysed file (cgo-remapped
	// positions are the only realistic case after newState verified the
	// FileSet) fail open in the reporting direction: an extra diagnostic is
	// recoverable noise, a silently honoured-or-dropped nolint is not.
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
			s.readErr = err
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

// noInitVarSet returns (building lazily) the set of variables declared
// without an initializer, e.g. `var c context.Context`. Suggesting such a
// variable in a fix would insert a nil context.
func (s *state) noInitVarSet() map[types.Object]bool {
	if s.noInitVars != nil {
		return s.noInitVars
	}
	s.noInitVars = make(map[types.Object]bool)
	for _, f := range s.pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 0 {
				return true
			}
			for _, name := range vs.Names {
				if obj := s.pass.TypesInfo.Defs[name]; obj != nil {
					s.noInitVars[obj] = true
				}
			}
			return true
		})
	}
	return s.noInitVars
}

// findCtxInScope searches the lexical scopes around pos for a variable that
// satisfies context.Context, to power the suggested fix. Variables literally
// named "ctx" are preferred; otherwise the nearest preceding candidate in the
// innermost scope that has one is used. Package-level candidates are usable
// regardless of declaration order. Variables declared without an initializer
// are skipped. Returns "", false if no candidate exists.
func (s *state) findCtxInScope(pos token.Pos) (string, bool) {
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
	scope := s.pass.TypesInfo.Scopes[astFile]
	if scope == nil {
		return "", false
	}
	scope = scope.Innermost(pos)

	noInit := s.noInitVarSet()
	pkgScope := s.pass.Pkg.Scope()
	usable := func(v *types.Var, sc *types.Scope) bool {
		if noInit[v] || !s.isContextType(v.Type()) {
			return false
		}
		// Package-level variables may be referenced regardless of their
		// declaration order; locals only after their declaration.
		return sc == pkgScope || v.Pos() < pos
	}

	fallback := ""
	for sc := scope; sc != nil; sc = sc.Parent() {
		// Prefer a variable literally named "ctx", even from an outer scope.
		if obj := sc.Lookup("ctx"); obj != nil {
			if v, ok := obj.(*types.Var); ok && usable(v, sc) {
				return "ctx", true
			}
		}
		if fallback != "" {
			continue
		}
		// Pick the nearest preceding candidate in this scope; fall back to
		// any candidate for order-independent (package) scope.
		var bestName string
		var bestPos token.Pos
		bestPreceding := false
		for _, name := range sc.Names() {
			v, ok := sc.Lookup(name).(*types.Var)
			if !ok || !usable(v, sc) {
				continue
			}
			preceding := v.Pos() < pos
			switch {
			case bestName == "",
				preceding && !bestPreceding,
				preceding == bestPreceding && preceding && v.Pos() > bestPos:
				bestName, bestPos, bestPreceding = name, v.Pos(), preceding
			}
		}
		fallback = bestName
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}
