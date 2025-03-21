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

	// Correct usage with a custom logger
	logger := zerolog.New(os.Stdout)
	logger.Info().Ctx(ctx).Str("key", "value").Msg("This is correct with custom logger")

	// Correct usage with derived context
	childCtx := context.WithValue(ctx, "key", "value")
	log.Info().Ctx(childCtx).Msg("This is correct with child context")
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

// Helper types for testing

type fakeThing struct{}

func someFunction() *fakeThing {
	return &fakeThing{}
}

func (f *fakeThing) DoSomething() {
	// Some non-logging method
}
