package zerologctx

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

type sourceIndex struct {
	pass          *analysis.Pass
	files         map[*token.File]*ast.File
	lines         map[*ast.File]*lineIndex
	callsByLparen map[token.Pos]*ast.CallExpr
	// contextIface is nil when context.Context is not reachable from the import
	// graph. Only suggested fixes need it; the analysis itself does not.
	contextIface *types.Interface
	// nils is built on first use, which is never for a package with no fixes.
	nils map[*types.Var]*nilFacts
}

// lineIndex answers the two line-oriented questions nolint processing asks:
// which comments sit on a line, and where the earliest code byte on that line
// is. The latter replaces re-reading the file from disk. It has to account for
// closing tokens too — the ")" of a multi-line call and the "}" of a block end
// a node without starting one, and a directive after them shares their line.
type lineIndex struct {
	comments  map[int][]*ast.Comment
	firstCode map[int]token.Pos
}

func newSourceIndex(pass *analysis.Pass, contextIface *types.Interface) (*sourceIndex, error) {
	i := &sourceIndex{
		pass: pass, files: make(map[*token.File]*ast.File, len(pass.Files)),
		lines:         make(map[*ast.File]*lineIndex, len(pass.Files)),
		callsByLparen: make(map[token.Pos]*ast.CallExpr), contextIface: contextIface,
	}
	for _, f := range pass.Files {
		tf := pass.Fset.File(f.Pos())
		if tf == nil {
			return nil, fmt.Errorf("zerologctx: corrupted FileSet for %s", f.Name)
		}
		i.files[tf] = f
		lines := &lineIndex{comments: make(map[int][]*ast.Comment), firstCode: make(map[int]token.Pos)}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case nil:
				return false
			case *ast.Comment, *ast.CommentGroup:
				return false
			case *ast.CallExpr:
				i.callsByLparen[n.Lparen] = n
			}
			lines.mark(tf, n.Pos())
			if end := n.End(); end > n.Pos() {
				lines.mark(tf, end-1)
			}
			return true
		})
		for _, group := range f.Comments {
			for _, comment := range group.List {
				line := tf.Line(comment.Pos())
				lines.comments[line] = append(lines.comments[line], comment)
			}
		}
		i.lines[f] = lines
	}
	return i, nil
}

func (e *engine) report() {
	for _, fn := range e.ssa.SrcFuncs {
		for _, finding := range e.findings[fn] {
			call := e.sources.callAt(finding.pos)
			if finding.state == stateHasContext || e.sources.hasNoLint(call) {
				continue
			}
			nilCtx := finding.nilCtx
			message := "zerolog output is not proven to carry context before " + finding.spec.name + "()"
			if nilCtx {
				message = "zerolog output's final Ctx() argument is nil before " + finding.spec.name + "()"
			}
			diagnostic := analysis.Diagnostic{Pos: finding.pos, Message: message}
			if selector := e.fixTarget(finding.spec, call); selector != nil && !nilCtx {
				if ctx := e.sources.contextExpr(finding.pos); ctx != "" {
					diagnostic.SuggestedFixes = []analysis.SuggestedFix{{
						Message: "Attach context before " + finding.spec.name + "()",
						TextEdits: []analysis.TextEdit{{
							Pos: selector.Sel.Pos(), End: selector.Sel.Pos(), NewText: []byte("Ctx(" + ctx + ")."),
						}},
					}}
				}
			}
			e.pass.Report(diagnostic)
		}
	}
}

// fixTarget returns the selector an inserted Ctx() call would precede, or nil
// when no safe edit exists. Method expressions, method values, promoted
// receivers and the direct Print/Write APIs all have no such position, so
// their diagnostics carry no fix.
func (e *engine) fixTarget(spec sinkSpec, call *ast.CallExpr) *ast.SelectorExpr {
	if spec.kind != sinkEvent || call == nil {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	selection := e.pass.TypesInfo.Selections[selector]
	if selection == nil || selection.Kind() != types.MethodVal {
		return nil
	}
	if kindOf(e.pass.TypesInfo.TypeOf(selector.X)) != kindEvent {
		return nil
	}
	return selector
}

func isAstNil(expression ast.Expr) bool {
	identifier, ok := ast.Unparen(expression).(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func (i *sourceIndex) contextExpr(pos token.Pos) string {
	pass := i.pass
	if i.contextIface == nil {
		return ""
	}
	tf := pass.Fset.File(pos)
	f := i.files[tf]
	if f == nil {
		return ""
	}
	// Scopes is optional driver output and is needed only to name a context
	// expression, so its absence costs the fix, never the diagnostic.
	fileScope := pass.TypesInfo.Scopes[f]
	if fileScope == nil {
		return ""
	}
	scope := fileScope.Innermost(pos)
	if scope == nil {
		return ""
	}
	// Nearest declaration wins, and inside one scope the name "ctx" wins over
	// the rest. types.Scope.Names is sorted, so the choice is deterministic.
	for sc := scope; sc != nil && sc != types.Universe; sc = sc.Parent() {
		if expression := i.candidateNamed(sc, scope, "ctx", pos); expression != "" {
			return expression
		}
		for _, name := range sc.Names() {
			if name == "ctx" || name == "_" {
				continue
			}
			if expression := i.candidateNamed(sc, scope, name, pos); expression != "" {
				return expression
			}
		}
	}
	return ""
}

// candidateNamed returns the expression to insert for one name, or "" when the
// name is not a usable context at pos: declared later, shadowed by an inner
// declaration, wrongly typed, or provably nil.
func (i *sourceIndex) candidateNamed(sc, scope *types.Scope, name string, pos token.Pos) string {
	variable, ok := sc.Lookup(name).(*types.Var)
	if !ok {
		return ""
	}
	if _, resolved := scope.LookupParent(name, pos); resolved != variable {
		return ""
	}
	return i.contextCandidate(variable, name, pos)
}

func (i *sourceIndex) contextCandidate(variable *types.Var, name string, pos token.Pos) string {
	t := unalias(variable.Type())
	expression := ""
	if types.Implements(t, i.contextIface) {
		expression = name
	} else if _, pointer := t.(*types.Pointer); !pointer && types.Implements(types.NewPointer(t), i.contextIface) {
		expression = "&" + name
	}
	if expression == "" || i.definitelyNil(variable, pos) {
		return ""
	}
	return expression
}

// nilFacts is what one variable's declaration and assignments say about its
// nil-ness, independent of any particular call site.
type nilFacts struct {
	declaredNil    bool
	declarationPos token.Pos
	assignments    []token.Pos
}

// nilIndex walks the package once and answers for every variable. The previous
// shape walked every file twice per candidate expression, which made naming a
// context quadratic in the size of the package.
func (i *sourceIndex) nilIndex() map[*types.Var]*nilFacts {
	if i.nils != nil {
		return i.nils
	}
	pass := i.pass
	i.nils = make(map[*types.Var]*nilFacts)
	declare := func(variable *types.Var, pos token.Pos, nil_ bool) {
		facts := i.nils[variable]
		if facts == nil {
			facts = &nilFacts{}
			i.nils[variable] = facts
		}
		facts.declaredNil, facts.declarationPos = nil_, pos
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				for idx, identifier := range node.Names {
					variable, ok := pass.TypesInfo.Defs[identifier].(*types.Var)
					if !ok {
						continue
					}
					switch {
					case len(node.Values) == 0:
						declare(variable, node.Pos(), true)
					case len(node.Values) == len(node.Names):
						declare(variable, node.Pos(), isDefinitelyNilExpr(pass, node.Values[idx]))
					default:
						declare(variable, node.Pos(), false)
					}
				}
			case *ast.AssignStmt:
				defines := node.Tok == token.DEFINE && len(node.Lhs) == len(node.Rhs)
				for idx, expression := range node.Lhs {
					identifier, ok := ast.Unparen(expression).(*ast.Ident)
					if !ok {
						continue
					}
					if variable, ok := pass.TypesInfo.Defs[identifier].(*types.Var); ok {
						if defines {
							declare(variable, node.Pos(), isDefinitelyNilExpr(pass, node.Rhs[idx]))
						}
						i.recordAssignment(variable, node.Pos())
					}
					if variable, ok := pass.TypesInfo.Uses[identifier].(*types.Var); ok {
						i.recordAssignment(variable, node.Pos())
					}
				}
			}
			return true
		})
	}
	return i.nils
}

func (i *sourceIndex) recordAssignment(variable *types.Var, pos token.Pos) {
	facts := i.nils[variable]
	if facts == nil {
		facts = &nilFacts{}
		i.nils[variable] = facts
	}
	facts.assignments = append(facts.assignments, pos)
}

// definitelyNil deliberately answers only when nil is certain by source
// position. Ambiguous control flow suppresses this predicate (and may still
// permit a fix); the analyzer itself never treats a candidate expression as
// proof of context. Source position is not execution order, so an assignment
// written below a sink inside a loop still counts as unknown, not as nil.
func (i *sourceIndex) definitelyNil(variable *types.Var, pos token.Pos) bool {
	facts := i.nilIndex()[variable]
	if facts == nil || !facts.declaredNil {
		return false
	}
	packageScoped := variable.Parent() == i.pass.Pkg.Scope()
	for _, assignment := range facts.assignments {
		if assignment == facts.declarationPos {
			continue
		}
		if packageScoped || assignment < pos {
			return false
		}
	}
	return true
}

func isDefinitelyNilExpr(pass *analysis.Pass, expression ast.Expr) bool {
	expression = ast.Unparen(expression)
	if isAstNil(expression) {
		return true
	}
	conversion, ok := expression.(*ast.CallExpr)
	return ok && len(conversion.Args) == 1 && pass.TypesInfo.Types[conversion.Fun].IsType() && isAstNil(conversion.Args[0])
}

func (i *sourceIndex) callAt(pos token.Pos) *ast.CallExpr { return i.callsByLparen[pos] }

func (i *sourceIndex) hasNoLint(call *ast.CallExpr) bool {
	if call == nil {
		return false
	}
	tf := i.pass.Fset.File(call.Pos())
	if tf == nil {
		return false
	}
	f := i.files[tf]
	if f == nil {
		return false
	}
	lines := i.lines[f]
	start, end := tf.Line(call.Pos()), tf.Line(call.End())
	for line := start; line <= end; line++ {
		for _, comment := range lines.comments[line] {
			if isNoLintComment(comment.Text, "zerologctx") {
				return true
			}
		}
	}
	for _, comment := range lines.comments[start-1] {
		if lines.standalone(tf, comment) && isNoLintComment(comment.Text, "zerologctx") {
			return true
		}
	}
	return false
}

// mark records that the line holding pos contains a code byte there.
func (l *lineIndex) mark(tf *token.File, pos token.Pos) {
	if !pos.IsValid() {
		return
	}
	line := tf.Line(pos)
	if previous, seen := l.firstCode[line]; !seen || pos < previous {
		l.firstCode[line] = pos
	}
}

// standalone reports whether the comment occupies its line alone. A directive
// that shares a line with code applies to that line, not to the next one.
func (l *lineIndex) standalone(tf *token.File, comment *ast.Comment) bool {
	code, ok := l.firstCode[tf.Line(comment.Pos())]
	return !ok || code > comment.Pos()
}

func isNoLintComment(commentText, linterName string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(commentText, "//"))
	if idx := strings.Index(text, "//"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	if !strings.HasPrefix(text, "nolint") {
		return false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "nolint"))
	if text == "" {
		return true
	}
	if !strings.HasPrefix(text, ":") {
		return false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, ":"))
	for name := range strings.SplitSeq(text, ",") {
		name = strings.TrimSpace(name)
		if name == linterName || name == "all" {
			return true
		}
	}
	return false
}
