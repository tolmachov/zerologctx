// Package zerologctx provides a linter that ensures zerolog events
// include context using the Ctx() method before terminal operations.
//
// The linter analyzes Go code to detect zerolog Event chains and ensures
// that the Ctx(ctx) method is called before terminal methods like Msg(),
// Msgf() or Send(). This helps maintain consistent context propagation in logs.
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
Msg(), Msgf() or Send() without calling Ctx(ctx) first in the chain.`,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// terminalMethods defines the zerolog Event methods that produce output
// and should be preceded by Ctx() in the method chain.
var terminalMethods = map[string]bool{
	"Msg":  true, // log.Info().Msg("message")
	"Msgf": true, // log.Info().Msgf("message %d", 42)
	"Send": true, // log.Info().Send()
}

// run implements the main analysis logic for the zerologctx linter.
func run(pass *analysis.Pass) (interface{}, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Track loggers that have context embedded
	loggersWithContext := make(map[string]bool)

	// First pass: identify loggers created with context
	nodeFilter := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.CallExpr)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.AssignStmt:
			// Look for assignments like: loggerWithCtx := log.With().Ctx(ctx).Logger()
			if len(node.Lhs) == 1 && len(node.Rhs) == 1 {
				if ident, ok := node.Lhs[0].(*ast.Ident); ok {
					if isLoggerWithContext(pass, node.Rhs[0]) {
						loggersWithContext[ident.Name] = true
					}
				}
			}
		case *ast.CallExpr:
			// Check if this is a method call (has a selector)
			sel, ok := node.Fun.(*ast.SelectorExpr)
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

			// Check if the logger that created this event has context
			hasContext := false

			// First check if .Ctx(ctx) was called in the event chain
			if hasCtxInChain(pass, sel.X) {
				hasContext = true
			}

			// If not, check if the event came from a logger with embedded context
			if !hasContext {
				hasContext = isEventFromLoggerWithContext(pass, sel.X, loggersWithContext)
			}

			if !hasContext {
				// Check for //nolint:zerologctx directive
				if !hasNoLintDirective(pass, node) {
					// Report the issue with a helpful message
					pass.Reportf(
						node.Pos(),
						"zerolog event missing .Ctx(ctx) before %s() - context should be included for proper log correlation",
						methodName,
					)
				}
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
			if isNoLintComment(comment.Text, "zerologctx") {
				return true
			}
		}
	}

	return false
}

// isNoLintComment checks if a comment is a nolint directive for the specified linter.
// It handles various formats like:
// - //nolint:zerologctx
// - // nolint: zerologctx
// - //nolint:linter1,zerologctx,linter2
// - //   nolint: another1, zerologctx, another2
func isNoLintComment(commentText, linterName string) bool {
	// Remove // prefix
	text := strings.TrimPrefix(commentText, "//")

	// Trim spaces
	text = strings.TrimSpace(text)

	// Check if it starts with "nolint"
	if !strings.HasPrefix(text, "nolint") {
		return false
	}

	// Remove "nolint" prefix
	text = strings.TrimPrefix(text, "nolint")

	// Check for colon
	if !strings.HasPrefix(text, ":") {
		return false
	}

	// Remove colon and trim spaces
	text = strings.TrimPrefix(text, ":")
	text = strings.TrimSpace(text)

	// Split by comma and check each linter
	linters := strings.Split(text, ",")
	for _, linter := range linters {
		if strings.TrimSpace(linter) == linterName {
			return true
		}
	}

	return false
}

// isLoggerWithContext checks if an expression creates a logger with embedded context.
// It looks for patterns like: log.With().Ctx(ctx).Logger() or zerolog.New(...).With().Ctx(ctx).Logger()
func isLoggerWithContext(pass *analysis.Pass, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	// Check if this is a .Logger() call
	if sel.Sel.Name != "Logger" {
		return false
	}

	// Walk up the chain to see if .Ctx() was called
	return hasCtxInContextChain(pass, sel.X)
}

// hasCtxInContextChain checks if .Ctx() was called in a Context chain (With().Ctx().Logger())
func hasCtxInContextChain(pass *analysis.Pass, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	// Check if this is a Ctx() call with at least one argument
	if sel.Sel.Name == "Ctx" && len(call.Args) > 0 {
		arg := call.Args[0]
		argType := pass.TypesInfo.TypeOf(arg)
		if argType != nil && isContextType(argType.String()) {
			return true
		}
	}

	// Continue checking up the chain
	return hasCtxInContextChain(pass, sel.X)
}

// isEventFromLoggerWithContext checks if an event was created from a logger that has context embedded
func isEventFromLoggerWithContext(pass *analysis.Pass, expr ast.Expr, loggersWithContext map[string]bool) bool {
	// Walk up the event chain to find the logger that created it
	for expr != nil {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			break
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}

		// Check if this is a logger method that creates an event (Info, Error, Debug, etc.)
		logLevelMethods := map[string]bool{
			"Info": true, "Error": true, "Debug": true, "Warn": true,
			"Fatal": true, "Panic": true, "Trace": true, "Log": true,
		}

		if logLevelMethods[sel.Sel.Name] {
			// Check if the logger has context
			if ident, ok := sel.X.(*ast.Ident); ok {
				if loggersWithContext[ident.Name] {
					return true
				}
			}
		}

		// Continue walking up the chain
		expr = sel.X
	}

	return false
}
