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

// Print creates a print level event
func Print() *zerolog.Event {
	return &zerolog.Event{}
}

// Printf creates a printf level event
func Printf(format string, v ...interface{}) {
	// No-op for testing
}

// Trace creates a trace level event
func Trace() *zerolog.Event {
	return &zerolog.Event{}
}

// With adds context to the global logger
func With() *zerolog.Context {
	return &zerolog.Context{}
}

// Ctx returns a sub-logger with the context field
func Ctx(ctx context.Context) *zerolog.Logger {
	l := zerolog.Logger{}
	return &l
}
