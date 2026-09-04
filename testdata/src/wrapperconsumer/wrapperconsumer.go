package wrapperconsumer

import (
	"context"

	"wrappkg"
)

func correct() {
	ctx := context.Background()
	wrappkg.Info().Ctx(ctx).Msg("correct")
}

func incorrect(ctx context.Context) {
	wrappkg.Info().Msg("missing context through wrapper") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// crossPkgVarWithCtx: the imported ctxCarrier fact recognises another
// package's context-bearing logger.
func crossPkgVarWithCtx(ctx context.Context) {
	wrappkg.CtxLogger.Info().Msg("imported logger carries ctx - must not trigger")
}

// crossPkgVarWithoutCtx: an imported logger without a context still triggers.
func crossPkgVarWithoutCtx(ctx context.Context) {
	wrappkg.PlainLogger.Info().Msg("imported logger has no ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// crossPkgFieldWithCtx: facts are published for exported struct fields too.
func crossPkgFieldWithCtx(ctx context.Context) {
	wrappkg.Shared.CtxLogger.Info().Msg("imported field carries ctx - must not trigger")
}

// crossPkgFieldWithoutCtx: and only for the fields that actually carry one.
func crossPkgFieldWithoutCtx(ctx context.Context) {
	wrappkg.Shared.PlainLogger.Info().Msg("imported field has no ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// derivedFromImported: a Logger-to-Logger derivation of an imported
// context-bearing logger keeps the context.
func derivedFromImported(ctx context.Context) {
	l := wrappkg.CtxLogger.Level(0)
	l.Info().Msg("derived from imported ctx logger - must not trigger")
}
