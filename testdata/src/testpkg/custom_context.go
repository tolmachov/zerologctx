// Package testpkg contains tests for custom context types
package testpkg

import (
	"context"

	"github.com/rs/zerolog/log"
)

// CustomContext embeds context.Context, similar to the tasks.Context in the real codebase
type CustomContext struct {
	context.Context
	userID int64
}

// NewCustomContext creates a new CustomContext
func NewCustomContext(ctx context.Context, userID int64) *CustomContext {
	return &CustomContext{
		Context: ctx,
		userID:  userID,
	}
}

// TestCustomContextType tests that custom context types that embed context.Context are recognized
func TestCustomContextType() {
	ctx := NewCustomContext(context.Background(), 123)

	// This should NOT trigger - custom context type that embeds context.Context should be recognized
	log.Info().Str("key", "value").Ctx(ctx).Msg("with custom context")

	// This should trigger - missing context
	log.Error().Str("key", "value").Msg("without context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// TestCustomContextPointer tests pointer to custom context
func TestCustomContextPointer() {
	ctx := NewCustomContext(context.Background(), 456)

	// Should work with pointer to custom context
	log.Info().Str("user", "test").Ctx(ctx).Msg("pointer to custom context")
}
