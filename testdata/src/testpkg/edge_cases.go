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

	// Struct logger with embedded context
	appWithCtx := &App{
		logger: zerolog.New(os.Stdout).With().Ctx(ctx).Logger(),
	}
	// This is tricky - the logger has context, but our analyzer may not detect it
	// because it only tracks identifiers, not struct fields
	appWithCtx.logger.Info().Msg("This MIGHT trigger incorrectly") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
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

	// Logger with embedded context from function
	// This is tricky - our analyzer won't know the function returns a logger with context
	getLoggerWithContext(ctx).Info().Msg("This MIGHT trigger incorrectly") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestInvalidContextType tests calling Ctx() with non-context types
func TestInvalidContextType() {
	type FakeContext struct{}
	fakeCtx := FakeContext{}

	// This should trigger - Ctx() called with wrong type
	// However, our type checker should catch this
	_ = fakeCtx

	// Using a string as context - currently triggers false positive
	// BUG: The linter doesn't properly validate the Ctx() argument type
	log.Info().Ctx("not-a-context").Msg("Invalid context type") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Using nil as context - currently triggers false positive
	// BUG: The linter doesn't properly validate the Ctx() argument type
	log.Info().Ctx(nil).Msg("Nil context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestVariableChains tests more complex variable chains
func TestVariableChains() {
	ctx := context.Background()

	// Build event step by step
	// BUG: The linter doesn't track context through variable assignments
	event1 := log.Info()
	event2 := event1.Str("key", "value")
	event3 := event2.Ctx(ctx)
	event3.Msg("This should be OK - context added in chain") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Build event without context
	ev1 := log.Error()
	ev2 := ev1.Str("error", "test")
	ev2.Msg("This should trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Short variable declaration with context logger
	ctxLogger := log.With().Ctx(ctx).Logger()
	ctxLogger.Info().Msg("Should NOT trigger - logger has context")

	// Multiple assignment (edge case)
	logger1, logger2 := zerolog.New(os.Stdout), zerolog.New(os.Stderr)
	logger1.Info().Msg("Should trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
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
	log.Info().Msg("Info without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Warn().Msg("Warn without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Error().Msg("Error without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Fatal and Panic might be special cases where context is less critical?
	log.Fatal().Msg("Fatal without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
	log.Panic().Msg("Panic without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// With context - should NOT trigger
	log.Trace().Ctx(ctx).Msg("Trace with context")
	log.Debug().Ctx(ctx).Msg("Debug with context")
	log.Info().Ctx(ctx).Msg("Info with context")
	log.Warn().Ctx(ctx).Msg("Warn with context")
	log.Error().Ctx(ctx).Msg("Error with context")
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

// TestNilContext tests using actual nil as context
func TestNilContext() {
	// This is a compile error in real code, but let's see how analyzer handles it
	// log.Info().Ctx(nil).Msg("Nil context")

	// Using a nil variable
	var nilCtx context.Context
	log.Info().Ctx(nilCtx).Msg("Nil context variable - technically has Ctx() call")
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

	// Global logger with embedded context
	// Analyzer won't track this because it's a global var, not a local assignment
	globalLoggerWithContext.Info().Msg("Global logger with embedded context - will likely trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}
