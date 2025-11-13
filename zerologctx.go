// Package zerologctx provides a linter that ensures zerolog events
// include context using the Ctx() method before terminal operations.
//
// The linter analyzes Go code to detect zerolog Event chains and ensures
// that the Ctx(ctx) method is called before terminal methods like Msg(),
// Msgf(), MsgFunc() or Send(). This helps maintain consistent context propagation in logs.
package zerologctx

import (
	"go/ast"
	"go/types"
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
Msg(), Msgf(), MsgFunc() or Send() without calling Ctx(ctx) first in the chain.`,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// terminalMethods defines the zerolog Event methods that produce output
// and should be preceded by Ctx() in the method chain.
var terminalMethods = map[string]bool{
	"Msg":     true, // log.Info().Msg("message")
	"Msgf":    true, // log.Info().Msgf("message %d", 42)
	"MsgFunc": true, // log.Info().MsgFunc(func() string { return "message" })
	"Send":    true, // log.Info().Send()
}

// run implements the main analysis logic for the zerologctx linter.
func run(pass *analysis.Pass) (interface{}, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Track loggers that have context embedded
	loggersWithContext := make(map[string]bool)

	// Track Event variables that have context in their chain
	// This allows proper tracking of context through variable assignments
	eventsWithContext := make(map[string]bool)

	// Cache for hasCtxInChain results to improve performance
	// This memoization prevents redundant traversal of the same AST subtrees
	ctxChainCache := make(map[ast.Expr]bool)

	// First pass: identify loggers created with context and Event variables with context
	nodeFilter := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.CallExpr)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.AssignStmt:
			// Handle assignments - can be multiple (e.g., a, b := ...)
			for i, lhs := range node.Lhs {
				if i >= len(node.Rhs) {
					// Short circuit for assignments like a, b = c, d where we run out of RHS
					break
				}

				ident, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}

				rhs := node.Rhs[i]
				if len(node.Rhs) == 1 && len(node.Lhs) > 1 {
					// Multiple assignment from single expression (e.g., a, b := fn())
					// We can't easily track this, skip
					continue
				}

				// Check if this is a logger with context: loggerWithCtx := log.With().Ctx(ctx).Logger()
				if isLoggerWithContext(pass, rhs) {
					loggersWithContext[ident.Name] = true
					continue
				}

				// Check if this is an Event with context in its chain
				// e.g., event := log.Info().Ctx(ctx)
				if isEventWithContext(pass, rhs, eventsWithContext) {
					eventsWithContext[ident.Name] = true
					continue
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
			if hasCtxInChainCached(pass, sel.X, ctxChainCache) {
				hasContext = true
			}

			// If not, check if the event came from a logger with embedded context
			if !hasContext {
				hasContext = isEventFromLoggerWithContext(pass, sel.X, loggersWithContext)
			}

			// If not, check if the event came from a variable that has context
			// e.g., tracking context through variables
			if !hasContext {
				hasContext = isEventFromVariableWithContext(sel.X, eventsWithContext)
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

// hasCtxInChainCached is a wrapper around hasCtxInChain that uses memoization
// to avoid redundant traversal of the same AST subtrees.
func hasCtxInChainCached(pass *analysis.Pass, expr ast.Expr, cache map[ast.Expr]bool) bool {
	// Check if result is already cached
	if result, ok := cache[expr]; ok {
		return result
	}

	// Compute the result
	result := hasCtxInChain(pass, expr)

	// Cache the result
	cache[expr] = result

	return result
}

// hasCtxInChain walks up the method call chain to check if Ctx(context) appears.
// It recursively examines the preceding method calls in a fluent interface chain.
//
// For a chain like log.Info().Str("key", "val").Ctx(ctx).Msg("message"),
// it checks if Ctx() with a context argument appears anywhere before Msg().
//
// IMPORTANT: This only counts Ctx() calls on *zerolog.Event, not on zerolog.Logger.
// log.Ctx(ctx) returns a Logger and doesn't add context to the Event.
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

		if argType != nil && isContextType(pass, argType) {
			// IMPORTANT: Also verify that Ctx() is being called on a *zerolog.Event
			// and not on a zerolog.Logger. log.Ctx(ctx) returns a Logger, which
			// doesn't guarantee the Event will have context.
			receiverType := pass.TypesInfo.Types[sel.X]
			if receiverType.Type != nil {
				receiverTypeStr := receiverType.Type.String()
				// Only count it if called on *zerolog.Event
				if strings.Contains(receiverTypeStr, "zerolog.Event") {
					return true
				}
			}
		}
	}

	// If this method wasn't Ctx(), continue checking up the chain
	return hasCtxInChain(pass, sel.X)
}

// isContextType checks if a type represents or implements context.Context.
// It handles direct context.Context types, pointers, and types that embed context.Context.
func isContextType(pass *analysis.Pass, typ types.Type) bool {
	if typ == nil {
		return false
	}

	// Get the string representation for quick checks
	typeStr := typ.String()

	// Exact match for context.Context
	if typeStr == "context.Context" {
		return true
	}

	// Pointer to context.Context
	if typeStr == "*context.Context" {
		return true
	}

	// Contains context.Context anywhere (handles vendored or module paths)
	// e.g., "github.com/some/vendor/context.Context"
	if strings.Contains(typeStr, "context.Context") {
		return true
	}

	// Check if the type implements context.Context interface
	// This handles custom types that embed context.Context (e.g., *tasks.Context)
	// We look for a method set that includes context.Context methods: Deadline, Done, Err, Value

	// For pointer types, check the underlying type's method set
	if ptr, ok := typ.(*types.Pointer); ok {
		// Check if pointer implements the interface
		if implementsContextInterface(ptr) {
			return true
		}
		// Also check the element type
		if implementsContextInterface(ptr.Elem()) {
			return true
		}
	}

	// For named types, check if they implement the interface
	if implementsContextInterface(typ) {
		return true
	}

	return false
}

// implementsContextInterface checks if a type has all the methods of context.Context.
// The context.Context interface requires four methods: Deadline, Done, Err, and Value.
func implementsContextInterface(typ types.Type) bool {
	// Get the method set for the type
	methodSet := types.NewMethodSet(typ)

	// context.Context requires these four methods
	requiredMethods := map[string]bool{
		"Deadline": false,
		"Done":     false,
		"Err":      false,
		"Value":    false,
	}

	// Check which required methods are present
	for i := 0; i < methodSet.Len(); i++ {
		method := methodSet.At(i)
		methodName := method.Obj().Name()
		if _, ok := requiredMethods[methodName]; ok {
			requiredMethods[methodName] = true
		}
	}

	// Verify all required methods are present
	for _, found := range requiredMethods {
		if !found {
			return false
		}
	}

	return true
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
		if argType != nil && isContextType(pass, argType) {
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

// isEventWithContext checks if an expression represents an Event with context in its chain.
// This is used to track Event variables like: event := log.Info().Ctx(ctx)
func isEventWithContext(pass *analysis.Pass, expr ast.Expr, eventsWithContext map[string]bool) bool {
	// Check if this expression has type *zerolog.Event
	typeInfo := pass.TypesInfo.Types[expr]
	if typeInfo.Type == nil {
		return false
	}

	typeString := typeInfo.Type.String()
	if !strings.Contains(typeString, "zerolog.Event") {
		return false
	}

	// Check if the expression is a call chain with Ctx()
	if hasCtxInChain(pass, expr) {
		return true
	}

	// Check if the expression references a variable that has context
	if isEventFromVariableWithContext(expr, eventsWithContext) {
		return true
	}

	return false
}

// isEventFromVariableWithContext checks if an expression is or references a variable
// that has context tracked in eventsWithContext map.
// This handles cases like: event1 := log.Info().Ctx(ctx); event2 := event1.Str("k", "v"); event2.Msg("text")
func isEventFromVariableWithContext(expr ast.Expr, eventsWithContext map[string]bool) bool {
	// Walk up the chain looking for identifiers
	for expr != nil {
		// Check if this is a direct identifier reference
		if ident, ok := expr.(*ast.Ident); ok {
			if eventsWithContext[ident.Name] {
				return true
			}
			return false
		}

		// Check if this is a method call on something
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}

		// Check if the receiver is an identifier with context
		if ident, ok := sel.X.(*ast.Ident); ok {
			if eventsWithContext[ident.Name] {
				return true
			}
		}

		// Continue walking up the chain
		expr = sel.X
	}

	return false
}
