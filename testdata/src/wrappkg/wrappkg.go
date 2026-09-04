package wrappkg

import (
	"context"
	"os"

	"github.com/rs/zerolog"
)

func NewLogger() zerolog.Logger {
	return zerolog.New(os.Stdout)
}

func Info() *zerolog.Event {
	return NewLogger().Info()
}

// CtxLogger is an exported package-level logger with an embedded context. The
// ctxCarrier fact has to carry that across the package boundary.
var CtxLogger = zerolog.New(os.Stdout).With().Ctx(context.Background()).Logger()

// PlainLogger has no context, so importers must keep getting diagnostics for
// it — the fact mechanism must not suppress by merely being present.
var PlainLogger = zerolog.New(os.Stdout)

// Cfg exposes both through exported fields, initialised by composite literal.
type Cfg struct {
	CtxLogger   zerolog.Logger
	PlainLogger zerolog.Logger
}

// Shared pins field facts crossing the package boundary.
var Shared = Cfg{
	CtxLogger:   zerolog.New(os.Stdout).With().Ctx(context.Background()).Logger(),
	PlainLogger: zerolog.New(os.Stdout),
}

// unexportedCtxLogger cannot be named from another package, so no fact is
// published for it; within this package it is tracked as usual.
var unexportedCtxLogger = zerolog.New(os.Stdout).With().Ctx(context.Background()).Logger()

func useUnexported(ctx context.Context) {
	unexportedCtxLogger.Info().Msg("still tracked inside its own package")
}
