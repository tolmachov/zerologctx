package testdata

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func correctUsage() {
	ctx := context.Background()
	
	// Correct: Using Ctx with the context
	log.Info().Ctx(ctx).Msg("This is correct")
	
	// Correct: Using Ctx with the context and other fields
	log.Info().Ctx(ctx).Str("key", "value").Msg("This is also correct")
	
	// Correct: Using Ctx with err
	err := error(nil)
	log.Error().Ctx(ctx).Err(err).Msg("This is also correct")
}

func incorrectUsage() {
	ctx := context.Background()
	
	// Incorrect: Missing Ctx
	log.Info().Msg("This is incorrect") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\)"
	
	// Incorrect: Missing Ctx with other fields
	log.Error().Str("key", "value").Msg("This is also incorrect") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\)"
	
	// Incorrect usage with a custom logger
	logger := zerolog.New(zerolog.NewConsoleWriter())
	logger.Info().Str("key", "value").Msg("This is incorrect too") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\)"
	
	// This is a non-logging function and should not trigger the linter
	someFunction().DoSomething()
	
	// These are not terminal logging methods and should not trigger the linter
	log.Info().Ctx(ctx).Str("key", "value")
	log.Info().Int("count", 1)
}

type fakeThing struct{}

func someFunction() *fakeThing {
	return &fakeThing{}
}

func (f *fakeThing) DoSomething() {
	// Some non-logging method
}