// Package testpkg contains additional edge case tests for the zerologctx analyzer.
package testpkg

import (
	"context"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// App represents an application with a logger field
type App struct {
	logger zerolog.Logger
}

// TestStructLoggers tests loggers stored in struct fields
func TestStructLoggers() {
	ctx := context.Background()
	app := &App{
		logger: zerolog.New(os.Stdout),
	}

	// This should trigger - logger from struct field without context
	app.logger.Info().Msg("Missing context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// This should NOT trigger - context added
	app.logger.Info().Ctx(ctx).Msg("With context")

	// Struct logger with embedded context via composite literal
	appWithCtx := &App{
		logger: zerolog.New(os.Stdout).With().Ctx(ctx).Logger(),
	}
	// The analyzer tracks struct fields via SelectorExpr for assignments, but
	// composite literal initialization bypasses handleAssign, so the field is
	// not tracked here. This is a documented known limitation.
	appWithCtx.logger.Info().Msg("Composite literal not tracked") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// getLogger returns a logger (function call)
func getLogger() zerolog.Logger {
	return zerolog.New(os.Stdout)
}

// getLoggerWithContext returns a logger with embedded context
func getLoggerWithContext(ctx context.Context) zerolog.Logger {
	return zerolog.New(os.Stdout).With().Ctx(ctx).Logger()
}

// TestFunctionLoggers tests loggers returned from functions
func TestFunctionLoggers() {
	ctx := context.Background()

	// This should trigger - logger from function without context
	getLogger().Info().Msg("Missing context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// This should NOT trigger - context added
	getLogger().Info().Ctx(ctx).Msg("With context")

	// Logger with embedded context from function.
	// The analyzer cannot infer context from function return values, only from
	// visible variable declarations and assignments, so this is a known false
	// positive: the diagnostic below is expected.
	getLoggerWithContext(ctx).Info().Msg("Missing context from func return") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestInvalidContextType verifies that wrong-type arguments to Ctx() are
// rejected by the Go type system before the linter even runs. The stub's
// Ctx(ctx context.Context) signature matches real zerolog, so passing a
// non-context value such as a string would be a compile error. Untyped nil
// does compile and gets a dedicated diagnostic — see reviewNilCtxArg in
// regression_review.go. The defensive isContextType check in the analyzer is
// exercised by the custom-context fixtures in custom_context.go.
func TestInvalidContextType() {}

// TestVariableChains tests more complex variable chains
func TestVariableChains() {
	ctx := context.Background()

	// Build event step by step
	// BUG #2 FIXED: The linter now tracks context through variable assignments
	event1 := log.Info()
	event2 := event1.Str("key", "value")
	event3 := event2.Ctx(ctx)
	event3.Msg("This should be OK - context added in chain")

	// Build event without context
	ev1 := log.Error()
	ev2 := ev1.Str("error", "test")
	ev2.Msg("This should trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Short variable declaration with context logger
	ctxLogger := log.With().Ctx(ctx).Logger()
	ctxLogger.Info().Msg("Should NOT trigger - logger has context")

	// Multiple assignment (edge case)
	logger1, logger2 := zerolog.New(os.Stdout), zerolog.New(os.Stderr)
	logger1.Info().Msg("Should trigger")       // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	logger2.Error().Msg("Should also trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestDiscard tests the Discard() terminal method
func TestDiscard() {
	// Note: Discard() is not actually a method in zerolog.Event
	// This test is commented out
	// ctx := context.Background()
	// log.Info().Discard()
	// log.Info().Ctx(ctx).Discard()
}

// TestNestedCalls tests nested and complex call patterns
func TestNestedCalls() {
	ctx := context.Background()

	// Event created inline
	func() {
		log.Info().Msg("Anonymous function without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	}()

	// Event with context in anonymous function
	func() {
		log.Info().Ctx(ctx).Msg("Anonymous function with context")
	}()

	// Defer with logging
	defer log.Info().Msg("Deferred log without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	defer log.Info().Ctx(ctx).Msg("Deferred log with context")
}

// TestChainWithMultipleCtxCalls tests if multiple Ctx() calls are handled
func TestChainWithMultipleCtxCalls() {
	ctx := context.Background()
	ctx2 := context.Background()

	// Multiple Ctx() calls - last one should win
	// This is technically valid (though weird)
	log.Info().Ctx(ctx).Str("key", "val").Ctx(ctx2).Msg("Multiple contexts")

	// First Ctx() call should be enough
	log.Info().Ctx(ctx).Str("key", "val").Msg("Single context")
}

// TestSendMethod tests Send() terminal method
func TestSendMethod() {
	ctx := context.Background()

	// Send without context - should trigger
	log.Info().Str("action", "test").Send() // want "zerolog event missing .Ctx\\(ctx\\) before Send\\(\\) - context should be included for proper log correlation"

	// Send with context - should NOT trigger
	log.Info().Ctx(ctx).Str("action", "test").Send()
}

// TestWithFields tests With() method patterns
func TestWithFields() {
	ctx := context.Background()

	// With() creates a child logger
	childLogger := log.With().Str("component", "test").Logger()
	childLogger.Info().Msg("Child logger without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// With() and Ctx() together
	childLoggerWithCtx := log.With().Ctx(ctx).Str("component", "test").Logger()
	childLoggerWithCtx.Info().Msg("Child logger with context - should NOT trigger")
}

// TestLogLevels tests different log levels
func TestLogLevels() {
	ctx := context.Background()

	// All log levels without context - should trigger
	log.Trace().Msg("Trace without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Debug().Msg("Debug without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Info().Msg("Info without context")   // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Warn().Msg("Warn without context")   // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Error().Msg("Error without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Fatal and Panic might be special cases where context is less critical?
	log.Fatal().Msg("Fatal without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Panic().Msg("Panic without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// WithLevel without context - should trigger
	log.WithLevel(zerolog.InfoLevel).Msg("WithLevel without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Note: Print is not exercised here — in the real zerolog API Print takes
	// fmt.Sprint-style arguments and returns nothing, so Print().Msg() does
	// not exist.

	// With context - should NOT trigger
	log.Trace().Ctx(ctx).Msg("Trace with context")
	log.Debug().Ctx(ctx).Msg("Debug with context")
	log.Info().Ctx(ctx).Msg("Info with context")
	log.Warn().Ctx(ctx).Msg("Warn with context")
	log.Error().Ctx(ctx).Msg("Error with context")
	log.WithLevel(zerolog.InfoLevel).Ctx(ctx).Msg("WithLevel with context")
}

// TestSampleMethod tests Sample() which might affect logging
func TestSampleMethod() {
	// Note: Sample() is not directly available on the global log
	// This test is commented out
	// ctx := context.Background()
	// log.Sample(zerolog.Often).Info().Msg("Sampled without context")
	// log.Sample(zerolog.Often).Info().Ctx(ctx).Msg("Sampled with context")
}

// TestLogMethod tests Log() as a level
func TestLogMethod() {
	ctx := context.Background()

	// Log() is a special level method
	log.Log().Msg("Log without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	log.Log().Ctx(ctx).Msg("Log with context")
}

// TestLogCtxIsLogger pins the load-bearing distinction between
// log.Ctx(ctx) (which returns a Logger and does NOT attach context to a
// subsequently created Event) and event.Ctx(ctx) (which does). The first
// form must still be flagged.
func TestLogCtxIsLogger() {
	ctx := context.Background()

	// log.Ctx(ctx) returns a Logger; the resulting Event has no Ctx() call,
	// so the terminal Msg() must be reported.
	log.Ctx(ctx).Info().Msg("log.Ctx is a Logger lookup, not Event ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// The fix is to call Ctx on the Event, not the Logger.
	log.Ctx(ctx).Info().Ctx(ctx).Msg("now correct - Ctx on the Event")
}

// TestCrossFunctionNameCollision is a regression test for the facts table
// being keyed by *types.Object rather than by identifier name. Two functions
// can both declare a variable named `logger` (one with embedded context, one
// without) without one polluting the other.
func TestCrossFunctionNameCollision() {
	// This function's `logger` has embedded context.
	ctx := context.Background()
	logger := log.With().Ctx(ctx).Logger()
	logger.Info().Msg("OK - this logger has context")
}

// crossCollisionVictim verifies that the `logger` declared in
// TestCrossFunctionNameCollision above does not pollute this scope.
func crossCollisionVictim() {
	ctx := context.Background()
	_ = ctx
	logger := zerolog.New(os.Stdout)
	logger.Info().Msg("MUST trigger - this logger does not have context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestInterfacePattern tests logger stored in interface
func TestInterfacePattern() {
	ctx := context.Background()

	var logger interface{} = zerolog.New(os.Stdout)

	// This won't trigger because type info won't show zerolog.Event
	// But let's test anyway
	if zl, ok := logger.(zerolog.Logger); ok {
		zl.Info().Msg("Logger from interface without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
		zl.Info().Ctx(ctx).Msg("Logger from interface with context")
	}
}

// TestReassignment tests logger reassignment scenarios
func TestReassignment() {
	ctx := context.Background()

	// Initial logger without context
	logger := zerolog.New(os.Stdout)
	logger.Info().Msg("First logger without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Reassign with context logger
	logger = zerolog.New(os.Stdout).With().Ctx(ctx).Logger()
	// BUG: The analyzer detects reassignment correctly and knows this logger has context
	// But test expectation was wrong - this does NOT trigger
	logger.Info().Msg("After reassignment with context logger")
}

// TestPointerLogger tests logger behind pointer
func TestPointerLogger() {
	ctx := context.Background()

	type LoggerHolder struct {
		Logger *zerolog.Logger
	}

	logger := zerolog.New(os.Stdout)
	holder := &LoggerHolder{Logger: &logger}

	// Dereferenced pointer logger
	(*holder.Logger).Info().Msg("Pointer logger without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	(*holder.Logger).Info().Ctx(ctx).Msg("Pointer logger with context")
}

// TestNilContext: a typed-nil context.Context value still has a Ctx() call
// in the chain with a context.Context-typed argument, so the analyzer treats
// it as satisfied. (Whether nil propagation is wise at runtime is the user's
// problem; the linter's job is to enforce that Ctx() was called, not to
// validate runtime nil-safety.)
func TestNilContext() {
	var nilCtx context.Context
	log.Info().Ctx(nilCtx).Msg("nil context value - should NOT trigger")
}

// TestGlobalLogger tests global logger patterns
var globalLogger = zerolog.New(os.Stdout)
var globalLoggerWithContext = zerolog.New(os.Stdout).With().Ctx(context.Background()).Logger()

func TestGlobalLoggers() {
	ctx := context.Background()

	// Global logger without context
	globalLogger.Info().Msg("Global logger without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Global logger with context in call
	globalLogger.Info().Ctx(ctx).Msg("Global logger with context in call")

	// Global logger with embedded context — tracked via ValueSpec.
	globalLoggerWithContext.Info().Msg("Global logger with embedded context - should NOT trigger")
}

// TestFindCtxFallback exercises the fallback path in findCtxInScope where no
// variable named "ctx" is in scope. The analyzer should still emit a diagnostic
// and the suggested fix should reference the non-"ctx" variable (reqCtx).
func TestFindCtxFallback() {
	reqCtx := context.Background()
	log.Info().Msg("only reqCtx in scope - fallback path") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Info().Ctx(reqCtx).Msg("reqCtx used correctly")
}

// getLoggerAndErr returns a logger and an error (multi-return helper for
// TestTupleAssignment).
func getLoggerAndErr() (zerolog.Logger, error) {
	return zerolog.New(os.Stdout), nil
}

// TestTupleAssignment verifies that a multi-LHS single-RHS assignment (tuple
// return) does not crash the analyzer. The tracker records factNone for
// tuple-assignment targets (the RHS cannot be classified per target), so the
// subsequent Msg() call correctly triggers; adding Ctx() inline still
// satisfies the check.
func TestTupleAssignment() {
	ctx := context.Background()

	// Multi-return: logger not tracked, so Info().Msg() triggers.
	logger, err := getLoggerAndErr()
	_ = err
	logger.Info().Msg("tuple assign - not tracked, triggers") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Explicit Ctx() on the Event still satisfies the check.
	logger.Info().Ctx(ctx).Msg("tuple assign with inline Ctx - should NOT trigger")
}
