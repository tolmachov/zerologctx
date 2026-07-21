// Package log is a stub implementation of github.com/rs/zerolog/log for testing
package log

import (
	"context"

	"github.com/rs/zerolog"
)

// Default logger used for global methods
var Logger = zerolog.Logger{}

// Info creates an info level event
func Info() *zerolog.Event {
	return &zerolog.Event{}
}

// Error creates an error level event
func Error() *zerolog.Event {
	return &zerolog.Event{}
}

// Err creates an event whose level depends on err, matching the real API.
func Err(err error) *zerolog.Event {
	return &zerolog.Event{}
}

// Debug creates a debug level event
func Debug() *zerolog.Event {
	return &zerolog.Event{}
}

// Warn creates a warn level event
func Warn() *zerolog.Event {
	return &zerolog.Event{}
}

// Fatal creates a fatal level event
func Fatal() *zerolog.Event {
	return &zerolog.Event{}
}

// Panic creates a panic level event
func Panic() *zerolog.Event {
	return &zerolog.Event{}
}

// Log creates a log level event
func Log() *zerolog.Event {
	return &zerolog.Event{}
}

// Print logs at debug level using fmt.Sprint-style arguments. Matches the
// real zerolog API, where Print does NOT return an *Event.
func Print(v ...interface{}) {
	// No-op for testing
}

// Printf logs at debug level using fmt.Sprintf-style arguments; it does not
// return an *Event, matching the real zerolog API.
func Printf(format string, v ...interface{}) {
	// No-op for testing
}

// Trace creates a trace level event
func Trace() *zerolog.Event {
	return &zerolog.Event{}
}

// With returns a context builder seeded from the global logger. The real
// zerolog API returns Context by value.
func With() zerolog.Context {
	return zerolog.Context{}
}

// WithLevel creates an event from the global logger at the given level.
func WithLevel(level zerolog.Level) *zerolog.Event {
	return Logger.WithLevel(level)
}

// Ctx returns the Logger associated with ctx (a lookup); it does NOT attach
// ctx to subsequently created events — the load-bearing distinction the
// analyzer's builderHasCtx/eventHasCtx receiver checks preserve.
func Ctx(ctx context.Context) *zerolog.Logger {
	l := zerolog.Logger{}
	return &l
}
