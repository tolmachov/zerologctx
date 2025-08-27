// Package testpkg contains test cases for the zerologctx analyzer.
package testpkg

import (
	"context"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// correctUsage demonstrates proper usage patterns that should not trigger the linter.
func correctUsage() {
	ctx := context.Background()

	// Basic correct usage with Ctx before Msg
	log.Info().Ctx(ctx).Msg("This is correct")

	// Correct usage with multiple fields in between
	log.Info().Ctx(ctx).Str("key", "value").Int("count", 42).Msg("This is also correct")

	// Correct usage with error
	err := error(nil)
	log.Error().Ctx(ctx).Err(err).Msg("This is also correct")

	// Correct usage with Send() instead of Msg()
	log.Info().Ctx(ctx).Str("action", "test").Send()

	// Correct usage with Msgf() formatted message
	log.Info().Ctx(ctx).Msgf("User %s logged in at %d", "alice", 123456)
	log.Error().Ctx(ctx).Err(err).Msgf("Failed to process request: %v", err)

	// Correct usage with MsgFunc() for lazy evaluation
	log.Info().Ctx(ctx).MsgFunc(func() string { return "Expensive computation result" })

	// Correct usage with a custom logger
	logger := zerolog.New(os.Stdout)
	logger.Info().Ctx(ctx).Str("key", "value").Msg("This is correct with custom logger")

	// Correct usage with derived context
	childCtx := context.WithValue(ctx, "key", "value")
	log.Info().Ctx(childCtx).Msg("This is correct with child context")

	// Correct usage: context was added to logger in a separate statement
	// Logger created with context embedded
	loggerWithCtx := log.With().Ctx(ctx).Logger()
	loggerWithCtx.Info().Msg("This should NOT trigger - context is in the logger")
	loggerWithCtx.Error().Str("error", "test").Msg("Context already in logger")

	// Another variation with custom logger
	customLoggerWithCtx := zerolog.New(os.Stdout).With().Ctx(ctx).Logger()
	customLoggerWithCtx.Info().Msg("Custom logger with embedded context")
	customLoggerWithCtx.Debug().Int("id", 123).Send()
}

// incorrectUsage demonstrates patterns that should trigger the linter.
func incorrectUsage() {
	// Create context but deliberately don't use it in some logs
	_ = context.Background()

	// Basic incorrect usage: Missing Ctx
	log.Info().Msg("This is incorrect") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Incorrect usage with fields but no context
	log.Error().Str("key", "value").Msg("Missing context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Incorrect with Send() instead of Msg()
	log.Info().Str("action", "test").Send() // want "zerolog event missing .Ctx\\(ctx\\) before Send\\(\\) - context should be included for proper log correlation"

	// Incorrect with Msgf() formatted message
	log.Info().Msgf("User %s logged in", "alice") // want "zerolog event missing .Ctx\\(ctx\\) before Msgf\\(\\) - context should be included for proper log correlation"

	// Incorrect with MsgFunc() for lazy evaluation
	log.Error().MsgFunc(func() string { return "Missing context" }) // want "zerolog event missing .Ctx\\(ctx\\) before MsgFunc\\(\\) - context should be included for proper log correlation"

	// Incorrect usage with a custom logger
	logger := zerolog.New(zerolog.NewConsoleWriter())
	logger.Info().Str("key", "value").Msg("Custom logger without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

}

// edgeCases demonstrates cases that should NOT trigger the linter.
func edgeCases() {
	ctx := context.Background()

	// This is a non-logging function and should not trigger the linter
	someFunction().DoSomething()

	// These are not terminal logging methods and should not trigger the linter
	log.Info().Ctx(ctx).Str("key", "value") // No terminal method, just building the event
	log.Info().Int("count", 1)              // No terminal method

	// Calling fields but no terminal method
	event := log.Info().Str("something", "value")
	// Later using the event with context
	event.Ctx(ctx).Msg("Using saved event")
}

// nolintDirectives demonstrates the use of nolint directives to suppress linter warnings
func nolintDirectives() {
	// These log events are missing Ctx() but have nolint directives to suppress warnings

	// Single line nolint directive
	//nolint:zerologctx
	log.Info().Msg("This should not trigger a warning due to nolint directive")

	// Single line nolint directive with space
	// nolint:zerologctx
	log.Info().Msg("This should not trigger a warning due to nolint directive with space")

	// Single line nolint directive with space after colon
	//nolint: zerologctx
	log.Info().Msg("This should not trigger a warning due to nolint directive with space after colon")

	// Single line nolint directive with spaces
	// nolint: zerologctx
	log.Info().Msg("This should not trigger a warning due to nolint directive with spaces")

	// End-of-line nolint directives
	log.Info().Msg("This should not trigger a warning") //nolint:zerologctx
	log.Info().Msg("This should not trigger a warning") // nolint:zerologctx
	log.Info().Msg("This should not trigger a warning") //nolint: zerologctx
	log.Info().Msg("This should not trigger a warning") // nolint: zerologctx

	// This should trigger a warning - comment is not a nolint directive
	// Just a regular comment
	log.Info().Msg("This should trigger a warning") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// This should trigger a warning - wrong linter name
	//nolint:someotherlinter
	log.Info().Msg("This should trigger a warning") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// This should trigger a warning - wrong linter name in end-of-line comment
	log.Info().Msg("This should trigger a warning") //nolint:someotherlinter // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"

	// Test multiple linters in nolint directives
	//nolint:linter1,zerologctx,linter2
	log.Info().Msg("Multiple linters with zerologctx - should not trigger")

	//   nolint: another1, zerologctx, another2
	log.Error().Str("test", "value").Msg("Multiple linters with spaces - should not trigger")

	log.Debug().Msg("Multiple linters inline") //nolint:foo,zerologctx,bar

	log.Warn().Msg("Multiple linters without zerologctx") //nolint:foo,bar,baz // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// Helper types for testing

type fakeThing struct{}

func someFunction() *fakeThing {
	return &fakeThing{}
}

func (f *fakeThing) DoSomething() {
	// Some non-logging method
}
