// Package zerologctx provides a static-analysis linter that ensures zerolog
// events include a context.Context via the .Ctx(ctx) method before terminal
// operations such as Msg(), Msgf(), MsgFunc(), or Send().
//
// The analyzer recognises several ways context can be attached:
//
//   - .Ctx(ctx) directly in the Event chain:
//     log.Info().Ctx(ctx).Msg("hi")
//   - A logger built with embedded context:
//     l := log.With().Ctx(ctx).Logger(); l.Info().Msg("hi")
//   - Context propagated through Event variable assignments:
//     e := log.Info().Ctx(ctx); e.Msg("hi")
//   - Custom context types satisfying context.Context (e.g. via embedding).
//
// A //nolint:zerologctx (or //nolint:all, or bare //nolint) comment on the
// terminal-call line, the line above the chain, or with a trailing reason is
// honoured.
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
// for type-identity checks against *zerolog.Event.
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

// logLevelMethods are zerolog.Logger methods that create an Event. They are
// the entry points the analyzer walks past when tracing back from a terminal
// call to its originating logger variable.
var logLevelMethods = map[string]struct{}{
	"Info":      {},
	"Error":     {},
	"Debug":     {},
	"Warn":      {},
	"Fatal":     {},
	"Panic":     {},
	"Trace":     {},
	"Log":       {},
	"Print":     {},
	"WithLevel": {},
}

// state holds the per-pass mutable analysis state. Centralising the maps and
// pass reference avoids threading 3+ parameters through every helper and
// gives one place to record/reset facts when behaviour evolves.
type state struct {
	pass *analysis.Pass

	// loggersWithCtx tracks variables (locals, parameters, package-level
	// vars, struct fields) whose value is a zerolog Logger that already has
	// a context embedded. Keyed by *types.Object so different bindings with
	// the same name in different scopes do not collide.
	loggersWithCtx map[types.Object]bool

	// eventsWithCtx tracks variables holding a *zerolog.Event chain that has
	// a Ctx(ctx) call somewhere upstream.
	eventsWithCtx map[types.Object]bool

	// contextIface is the canonical context.Context interface, found by a
	// recursive walk of the package's imports. May be nil if the package
	// transitively does not import "context"; in that case isContextType
	// returns false.
	contextIface *types.Interface

	// fileMap maps token.Files to the *ast.File the analyzer should scan
	// for nolint directives. Populated eagerly by buildFileMap before traversal
	// begins; never nil after a successful run().
	fileMap map[*token.File]*ast.File
}

// newState constructs a fresh analysis state for the given pass.
func newState(pass *analysis.Pass) *state {
	return &state{
		pass:           pass,
		loggersWithCtx: make(map[types.Object]bool),
		eventsWithCtx:  make(map[types.Object]bool),
		contextIface:   findContextInterface(pass.Pkg),
	}
}

// run is the analyzer entry point.
func run(pass *analysis.Pass) (any, error) {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, fmt.Errorf("zerologctx: inspect.Analyzer result missing or wrong type")
	}

	// Skip packages that do not even import zerolog. This is the common case
	// in monorepos and avoids walking the AST entirely for those packages.
	if !importsZerolog(pass.Pkg) {
		return nil, nil
	}

	s := newState(pass)
	// contextIface is always non-nil for packages that import zerolog, because
	// zerolog itself transitively imports "context". This check guards against
	// future zerolog API changes or unusual testdata stubs.
	if s.contextIface == nil {
		return nil, fmt.Errorf("zerologctx: could not locate context.Context interface in %s's import graph", pass.Pkg.Path())
	}
	if err := s.buildFileMap(); err != nil {
		return nil, err
	}

	// Single-pass traversal: AssignStmt and ValueSpec nodes register loggers
	// and Events that carry context; CallExpr nodes check terminal calls.
	// Both kinds are visited in source order in one walk, so an Event used
	// before its declaring assignment (e.g. via a closure) may not yet be
	// registered when the terminal call is visited.
	nodeFilter := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.ValueSpec)(nil),
		(*ast.CallExpr)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.AssignStmt:
			s.handleAssign(node)
		case *ast.ValueSpec:
			s.handleValueSpec(node)
		case *ast.CallExpr:
			s.handleCall(node)
		}
	})

	return nil, nil
}

// handleAssign records context-bearing loggers and events established by
// short-var-decl (`:=`) or assignment (`=`). It also clears any prior fact
// when a tracked variable is reassigned to a value without context, which
// would otherwise produce false negatives.
func (s *state) handleAssign(node *ast.AssignStmt) {
	// Tuple assignments like `a, b := fn()` are not tracked; we cannot
	// statically split a single multi-return into per-LHS facts.
	if len(node.Rhs) == 1 && len(node.Lhs) > 1 {
		return
	}
	if len(node.Lhs) != len(node.Rhs) {
		return
	}

	for i, lhs := range node.Lhs {
		obj := s.objectFromLHS(lhs)
		if obj == nil {
			continue
		}
		s.recordRHS(obj, node.Rhs[i])
	}
}

// handleValueSpec records context-bearing loggers and events established by
// `var` declarations (including package-level vars).
func (s *state) handleValueSpec(node *ast.ValueSpec) {
	if len(node.Values) == 0 {
		return
	}
	if len(node.Names) != len(node.Values) {
		return
	}
	for i, name := range node.Names {
		obj := s.pass.TypesInfo.Defs[name]
		if obj == nil {
			continue
		}
		s.recordRHS(obj, node.Values[i])
	}
}

// recordRHS classifies a right-hand-side expression and updates the maps for
// the given target object. Reassignment to a value without context clears
// any prior fact for the same object.
func (s *state) recordRHS(obj types.Object, rhs ast.Expr) {
	switch {
	case s.isLoggerWithContext(rhs):
		s.loggersWithCtx[obj] = true
		delete(s.eventsWithCtx, obj)
	case s.isEventWithContext(rhs):
		s.eventsWithCtx[obj] = true
		delete(s.loggersWithCtx, obj)
	default:
		// Reassignment to a non-context-bearing value: clear any prior fact.
		delete(s.loggersWithCtx, obj)
		delete(s.eventsWithCtx, obj)
	}
}

// objectFromLHS resolves the *types.Object for an assignment LHS. It handles
// bare identifiers (locals, params, package vars) and struct-field selectors
// (`s.logger`, `app.fields.logger`).
func (s *state) objectFromLHS(lhs ast.Expr) types.Object {
	switch x := lhs.(type) {
	case *ast.Ident:
		if obj := s.pass.TypesInfo.Defs[x]; obj != nil {
			return obj
		}
		return s.pass.TypesInfo.ObjectOf(x)
	case *ast.SelectorExpr:
		if sel, ok := s.pass.TypesInfo.Selections[x]; ok {
			return sel.Obj()
		}
		return s.pass.TypesInfo.ObjectOf(x.Sel)
	}
	return nil
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

	hasCtx := s.hasCtxCallInChain(sel.X, true) ||
		s.isEventFromLoggerWithContext(sel.X) ||
		s.isEventFromVariableWithContext(sel.X)
	if hasCtx {
		return
	}
	if s.hasNoLintDirective(node, sel.Sel.Pos()) {
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

// hasCtxCallInChain walks a fluent call chain looking for a Ctx(ctx) call
// whose argument satisfies context.Context.
//
// If requireEventReceiver is true, only Ctx() calls whose receiver is a
// *zerolog.Event are counted. This enforces the load-bearing distinction
// between Event.Ctx(ctx) (which attaches context to the event) and
// Logger.Ctx(ctx) (which retrieves a logger from a context and does NOT
// attach context to subsequently created events).
//
// When requireEventReceiver is false the receiver is not checked, which is
// appropriate inside a With()...Logger() chain where Ctx() is called on a
// *zerolog.Context builder by construction.
func (s *state) hasCtxCallInChain(expr ast.Expr, requireEventReceiver bool) bool {
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "Ctx" && len(call.Args) > 0 {
			argType := s.pass.TypesInfo.TypeOf(call.Args[0])
			if argType != nil && s.isContextType(argType) {
				if !requireEventReceiver {
					return true
				}
				if recvType := s.pass.TypesInfo.TypeOf(sel.X); recvType != nil && isZerologEvent(recvType) {
					return true
				}
			}
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
	// Allow value-receiver types to satisfy via their pointer receiver as well.
	if _, isPtr := typ.(*types.Pointer); !isPtr {
		if types.Implements(types.NewPointer(typ), s.contextIface) {
			return true
		}
	}
	return false
}

// findContextInterface walks pkg's import graph (depth-first) looking for
// the standard library's context.Context interface. Returns nil if context
// is not transitively imported, in which case the analyzer cannot reason
// about Ctx() arguments and treats them as non-context.
func findContextInterface(pkg *types.Package) *types.Interface {
	if pkg == nil {
		return nil
	}
	seen := map[*types.Package]bool{}
	var visit func(p *types.Package) *types.Interface
	visit = func(p *types.Package) *types.Interface {
		if p == nil || seen[p] {
			return nil
		}
		seen[p] = true
		if p.Path() == "context" {
			if obj := p.Scope().Lookup("Context"); obj != nil {
				if objType := obj.Type(); objType != nil {
					if iface, ok := objType.Underlying().(*types.Interface); ok {
						return iface
					}
				}
			}
		}
		for _, imp := range p.Imports() {
			if iface := visit(imp); iface != nil {
				return iface
			}
		}
		return nil
	}
	return visit(pkg)
}

// importsZerolog reports whether pkg directly imports the zerolog library.
// Used to short-circuit the inspector pass for packages that cannot possibly
// contain zerolog calls. Note: this checks only direct imports, not transitive
// ones, so packages that re-export zerolog via a wrapper will not match.
func importsZerolog(pkg *types.Package) bool {
	if pkg == nil {
		return false
	}
	for _, imp := range pkg.Imports() {
		if imp.Path() == zerologPkgPath || strings.HasPrefix(imp.Path(), zerologPkgPath+"/") {
			return true
		}
	}
	return false
}

// isZerologEvent reports whether t is *zerolog.Event or zerolog.Event from
// the canonical github.com/rs/zerolog package. This is the precise
// replacement for the previous strings.Contains(typeString, "zerolog.Event")
// shortcut, which would also match unrelated types like zerolog.EventMarshaler
// or types in forks named similarly.
func isZerologEvent(t types.Type) bool {
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
	return obj.Name() == "Event" && obj.Pkg().Path() == zerologPkgPath
}

// isLoggerWithContext checks whether expr is a Logger constructor chain that
// embeds a context, e.g. log.With().Ctx(ctx).Logger().
func (s *state) isLoggerWithContext(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Logger" {
		return false
	}
	// Inside a With()...Logger() chain the Ctx() receiver is a
	// *zerolog.Context by construction, so we don't enforce the receiver
	// type — any Ctx(ctx) call counts.
	return s.hasCtxCallInChain(sel.X, false)
}

// isEventFromLoggerWithContext walks the originating log-level call (Info,
// Error, Print, ...) for an Event chain and reports whether the underlying
// logger is one we previously recorded as carrying context. It supports both
// bare identifiers and selector expressions (struct fields, package-qualified
// loggers).
func (s *state) isEventFromLoggerWithContext(expr ast.Expr) bool {
	for expr != nil {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if _, isLevel := logLevelMethods[sel.Sel.Name]; isLevel {
			if obj := s.objectFromExpr(sel.X); obj != nil && s.loggersWithCtx[obj] {
				return true
			}
		}
		expr = sel.X
	}
	return false
}

// isEventWithContext reports whether expr is itself a *zerolog.Event chain
// that already has context attached, either directly via Ctx() in the chain
// or by referencing a tracked Event variable.
func (s *state) isEventWithContext(expr ast.Expr) bool {
	t := s.pass.TypesInfo.TypeOf(expr)
	if t == nil || !isZerologEvent(t) {
		return false
	}
	if s.hasCtxCallInChain(expr, true) {
		return true
	}
	if s.isEventFromVariableWithContext(expr) {
		return true
	}
	return false
}

// isEventFromVariableWithContext walks an Event chain looking for an
// identifier or field reference that is registered in eventsWithCtx. This
// enables propagation across assignments such as
//
//	e1 := log.Info().Ctx(ctx)
//	e2 := e1.Str("k", "v")
//	e2.Msg("...")
func (s *state) isEventFromVariableWithContext(expr ast.Expr) bool {
	for expr != nil {
		if obj := s.objectFromExpr(expr); obj != nil {
			if s.eventsWithCtx[obj] {
				return true
			}
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		expr = sel.X
	}
	return false
}

// objectFromExpr resolves the *types.Object behind a bare identifier or a
// selector expression (struct field). Returns nil for any other shape.
func (s *state) objectFromExpr(expr ast.Expr) types.Object {
	switch x := expr.(type) {
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
// zerologctx applies to the given call. It accepts directives placed on the
// terminal-method line (handles multi-line fluent chains where the chain
// starts on an earlier line) or on the line immediately preceding the chain.
func (s *state) hasNoLintDirective(call *ast.CallExpr, terminalPos token.Pos) bool {
	tokFile := s.pass.Fset.File(call.Pos())
	if tokFile == nil {
		return false
	}
	astFile := s.fileFor(tokFile)
	if astFile == nil {
		return false
	}

	terminalLine := tokFile.Line(terminalPos)
	chainStartLine := tokFile.Line(call.Pos())

	for _, cg := range astFile.Comments {
		for _, c := range cg.List {
			commentLine := tokFile.Line(c.Pos())
			// Accept directives on the terminal-method line (covers
			// end-of-line directives on multi-line chains) or on the line
			// immediately above the chain start.
			if commentLine != terminalLine && commentLine != chainStartLine-1 {
				continue
			}
			if isNoLintComment(c.Text, "zerologctx") {
				return true
			}
		}
	}
	return false
}

// buildFileMap populates s.fileMap eagerly at the start of the analysis pass.
// Returning an error here surfaces FileSet corruption immediately rather than
// silently skipping files during nolint processing (which would cause
// //nolint:zerologctx directives to be unexpectedly ignored).
func (s *state) buildFileMap() error {
	s.fileMap = make(map[*token.File]*ast.File, len(s.pass.Files))
	for _, f := range s.pass.Files {
		pf := s.pass.Fset.File(f.Pos())
		if pf == nil {
			return fmt.Errorf("zerologctx: FileSet.File returned nil for %s; this indicates a corrupted FileSet", f.Name)
		}
		s.fileMap[pf] = f
	}
	return nil
}

// fileFor returns the *ast.File for a token.File, or nil if the file is not
// in the analysed set (e.g. an imported file whose positions happen to fall
// in the same FileSet). buildFileMap must be called before fileFor.
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
	for _, linter := range strings.Split(text, ",") {
		l := strings.TrimSpace(linter)
		if l == linterName || l == "all" {
			return true
		}
	}
	return false
}

// findCtxInScope searches the lexical scopes around pos for a variable that
// satisfies context.Context. Variables literally named "ctx" are preferred;
// otherwise the first matching variable found while walking outwards is
// returned. Returns "", false if no candidate exists or if the package does
// not import context.
func (s *state) findCtxInScope(pos token.Pos) (string, bool) {
	// contextIface is guaranteed non-nil by run()'s early-exit guard.
	// This check is unreachable in normal operation but prevents a panic
	// if findCtxInScope is ever called outside the run() flow.
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

	var fallback string
	for sc := scope; sc != nil; sc = sc.Parent() {
		// Prefer a variable literally named "ctx".
		if obj := sc.Lookup("ctx"); obj != nil {
			if v, ok := obj.(*types.Var); ok && v.Pos() < pos && s.isContextType(v.Type()) {
				return "ctx", true
			}
		}
		if fallback == "" {
			for _, name := range sc.Names() {
				obj := sc.Lookup(name)
				v, ok := obj.(*types.Var)
				if !ok {
					continue
				}
				// Only consider variables declared before the diagnostic site
				if v.Pos() >= pos {
					continue
				}
				if s.isContextType(v.Type()) {
					fallback = name
					break
				}
			}
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}
