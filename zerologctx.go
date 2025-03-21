// Package zerologctx provides a linter that ensures zerolog events
// include context using the Ctx() method before terminal operations.
//
// The linter analyzes Go code to detect zerolog Event chains and ensures
// that the Ctx(ctx) method is called before terminal methods like Msg()
// or Send(). This helps maintain consistent context propagation in logs.
package zerologctx

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the main entry point for the zerologctx linter.
// It checks whether zerolog events include context via Ctx() before
// terminal operations like Msg() or Send().
var Analyzer = &analysis.Analyzer{
	Name: "zerologctx",
	Doc: `Ensures zerolog events include context via the Ctx() method.
This analyzer reports whenever a zerolog event uses terminal methods like
Msg() or Send() without calling Ctx(ctx) first in the chain.`,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// terminalMethods defines the zerolog Event methods that produce output
// and should be preceded by Ctx() in the method chain.
var terminalMethods = map[string]bool{
	"Msg":  true, // log.Info().Msg("message")
	"Send": true, // log.Info().Send()
}

// run implements the main analysis logic for the zerologctx linter.
func run(pass *analysis.Pass) (interface{}, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// We're only interested in method call expressions
	nodeFilter := []ast.Node{
		(*ast.CallExpr)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)

		// Check if this is a method call (has a selector)
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}

		// Skip if we can't determine the type
		typeInfo := pass.TypesInfo.Types[sel.X]
		if typeInfo.Type == nil {
			return
		}

		// Get the type as a string and check if it's a zerolog Event
		typeString := typeInfo.Type.String()
		if !strings.Contains(typeString, "zerolog.Event") {
			return
		}

		// Check if the method is a terminal logging method
		methodName := sel.Sel.Name
		if !terminalMethods[methodName] {
			return
		}

		// Check if .Ctx(ctx) was called anywhere in the chain
		if !hasCtxInChain(pass, sel.X) {
			// Check for //nolint:zerologctx directive
			if !hasNoLintDirective(pass, call) {
				// Report the issue with a helpful message
				pass.Reportf(
					call.Pos(),
					"zerolog event missing .Ctx(ctx) before %s() - context should be included for proper log correlation",
					methodName,
				)
			}
		}
	})

	return nil, nil
}

// hasCtxInChain walks up the method call chain to check if Ctx(context) appears.
// It recursively examines the preceding method calls in a fluent interface chain.
//
// For a chain like log.Info().Str("key", "val").Ctx(ctx).Msg("message"),
// it checks if Ctx() with a context argument appears anywhere before Msg().
func hasCtxInChain(pass *analysis.Pass, expr ast.Expr) bool {
	// The expression must be a function call
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}

	// The function must be a method (have a selector)
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	// Check if this is a Ctx() call with at least one argument
	if sel.Sel.Name == "Ctx" && len(call.Args) > 0 {
		// We must verify that the argument is actually a context.Context
		arg := call.Args[0]
		argType := pass.TypesInfo.TypeOf(arg)

		// Check various forms of context types
		if argType != nil && isContextType(argType.String()) {
			return true
		}
	}

	// If this method wasn't Ctx(), continue checking up the chain
	return hasCtxInChain(pass, sel.X)
}

// isContextType checks if a type string represents a context.Context type.
// It handles various ways the context type might appear in the type system.
func isContextType(typeStr string) bool {
	return typeStr == "context.Context" ||
		strings.Contains(typeStr, "context.Context") ||
		strings.HasSuffix(typeStr, ".Context")
}

// hasNoLintDirective checks if there's a nolint directive for zerologctx on the node.
// It looks at file comments around the position of the node to detect directives.
func hasNoLintDirective(pass *analysis.Pass, call *ast.CallExpr) bool {
	// Get position info for the node
	pos := call.Pos()
	file := pass.Fset.File(pos)
	if file == nil {
		return false
	}

	// Get comment groups from file AST
	nodeFile := -1
	for i, f := range pass.Files {
		if file == pass.Fset.File(f.Pos()) {
			nodeFile = i
			break
		}
	}

	if nodeFile == -1 {
		return false
	}

	// Check comment groups for end-of-line comments
	nodeLine := file.Line(pos)
	for _, commentGroup := range pass.Files[nodeFile].Comments {
		// Check if any comment in the group is on the same line as the node
		for _, comment := range commentGroup.List {
			commentLine := file.Line(comment.Pos())

			// Only consider comments on the same line or the line before
			if commentLine != nodeLine && commentLine != nodeLine-1 {
				continue
			}

			// Check if it contains a nolint directive for this linter
			commentText := comment.Text
			if strings.Contains(commentText, "//nolint:zerologctx") ||
				strings.Contains(commentText, "// nolint:zerologctx") ||
				strings.Contains(commentText, "//nolint: zerologctx") ||
				strings.Contains(commentText, "// nolint: zerologctx") {
				return true
			}
		}
	}

	return false
}
