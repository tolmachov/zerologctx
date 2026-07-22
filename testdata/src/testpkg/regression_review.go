// Package testpkg — regression fixtures from the full project review: each
// function pins one previously misbehaving class of false positives or false
// negatives in the analyzer.
package testpkg

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// reviewMutationStyleCtx: a standalone `e.Ctx(ctx)` statement mutates the
// Event in place (real zerolog semantics), so the later terminal call has
// context.
func reviewMutationStyleCtx() {
	ctx := context.Background()
	e := log.Info()
	e.Ctx(ctx)
	e.Msg("ctx attached via mutating statement - should NOT trigger")
}

// reviewEventFromCtxLogger: an Event variable created from a context-bearing
// logger keeps the context.
func reviewEventFromCtxLogger() {
	ctx := context.Background()
	ctxLogger := log.With().Ctx(ctx).Logger()
	e := ctxLogger.Info()
	e.Msg("event from ctx logger - should NOT trigger")
}

// reviewDerivedLoggers: Logger-returning derivations of a context-bearing
// logger keep the embedded context.
func reviewDerivedLoggers() {
	ctx := context.Background()
	ctxLogger := log.With().Ctx(ctx).Logger()

	sub := ctxLogger.With().Str("component", "x").Logger()
	sub.Info().Msg("sub-logger keeps ctx - should NOT trigger")

	leveled := ctxLogger.Level(zerolog.InfoLevel)
	leveled.Info().Msg("leveled logger keeps ctx - should NOT trigger")

	ctxLogger.Level(zerolog.WarnLevel).Info().Msg("inline derived logger - should NOT trigger")
}

// reviewLoggerAliasing: plain aliasing of a logger variable propagates (or
// does not invent) the context fact.
func reviewLoggerAliasing() {
	ctx := context.Background()
	ctxLogger := log.With().Ctx(ctx).Logger()
	alias := ctxLogger
	alias.Info().Msg("alias of ctx logger - should NOT trigger")

	plain := zerolog.New(nil)
	plainAlias := plain
	plainAlias.Info().Msg("alias of plain logger - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewLoggerErr: Logger.Err is an Event-producing method and keeps the
// logger's embedded context.
func reviewLoggerErr() {
	ctx := context.Background()
	err := errors.New("boom")
	ctxLogger := log.With().Ctx(ctx).Logger()
	ctxLogger.Err(err).Msg("Err event from ctx logger - should NOT trigger")

	log.Err(err).Msg("global Err without ctx - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewLogCtxDerivedLogger: log.Ctx(ctx) is a Logger lookup and does not
// attach context to events, even when a child logger is derived from it.
func reviewLogCtxDerivedLogger() {
	ctx := context.Background()
	l := log.Ctx(ctx).With().Str("k", "v").Logger()
	l.Info().Msg("log.Ctx-derived logger - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewBuilderVariable: a zerolog.Context builder stored in a variable keeps
// its Ctx fact through to Logger().
func reviewBuilderVariable() {
	ctx := context.Background()
	b := log.With().Ctx(ctx)
	l := b.Logger()
	l.Info().Msg("builder variable - should NOT trigger")
}

// reviewTupleClearsFact: a tuple reassignment invalidates a previously
// recorded context fact for its targets.
func reviewTupleClearsFact() {
	ctx := context.Background()
	logger := log.With().Ctx(ctx).Logger()
	logger.Info().Msg("before tuple reassignment - should NOT trigger")

	var err error
	logger, err = getLoggerAndErr()
	_ = err
	logger.Info().Msg("after tuple reassignment - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewParenChain: parentheses inside the fluent chain are transparent.
func reviewParenChain() {
	ctx := context.Background()
	(log.Info().Ctx(ctx)).Str("k", "v").Msg("parenthesised chain - should NOT trigger")
}

// reviewNilCtxArg: Ctx with an untyped nil argument gets a dedicated message
// instead of the misleading "missing .Ctx(ctx)".
func reviewNilCtxArg() {
	log.Info().Ctx(nil).Msg("nil ctx") // want "zerolog event calls Ctx\\(\\) with a non-context argument before Msg\\(\\) - pass a context.Context for proper log correlation"
}

// reviewNolintPreviousLineEOL: an end-of-line nolint belongs to its own
// statement and must not suppress the diagnostic on the next line.
func reviewNolintPreviousLineEOL() {
	ctx := context.Background()
	log.Info().Ctx(ctx).Msg("fine") //nolint:zerologctx
	log.Info().Msg("previous line's EOL nolint does not apply") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewNolintChainStartLine: a nolint on the chain-start line of a
// multi-line chain (the line the diagnostic points at) suppresses it.
func reviewNolintChainStartLine() {
	log.Info(). //nolint:zerologctx
			Str("k", "v").
			Msg("suppressed via chain-start line nolint")
}

// reviewNolintInteriorChainLine: a nolint on an interior line of a multi-line
// chain also suppresses the diagnostic.
func reviewNolintInteriorChainLine() {
	log.Info().
		Str("k", "v"). //nolint:zerologctx
		Msg("suppressed via interior chain line nolint")
}

// reviewLoggerReassignClears: a plain reassignment records factNone, and the
// nearest-preceding lookup makes it win for later uses.
func reviewLoggerReassignClears() {
	ctx := context.Background()
	logger := log.With().Ctx(ctx).Logger()
	logger.Info().Msg("before plain reassignment - should NOT trigger")

	logger = zerolog.New(nil)
	logger.Info().Msg("after plain reassignment - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewBuilderReassignClears: reassigning a builder variable without Ctx
// clears its fact before Logger() is called.
func reviewBuilderReassignClears() {
	ctx := context.Background()
	b := log.With().Ctx(ctx)
	b = log.With().Str("k", "v")
	l := b.Logger()
	l.Info().Msg("builder reassigned without ctx - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewEventReassignClearsMutationFact: reassigning the Event variable
// discards the fact recorded by an earlier mutating Ctx statement.
func reviewEventReassignClearsMutationFact() {
	ctx := context.Background()
	e := log.Info()
	e.Ctx(ctx)
	e = log.Error()
	e.Msg("event reassigned after mutating Ctx - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// reviewMutationThroughChain: a mutating statement attaches context even when
// Ctx is not the first call of the discarded chain (exercises the
// chainRootObject walk).
func reviewMutationThroughChain() {
	ctx := context.Background()
	e := log.Info()
	e.Str("k", "v").Ctx(ctx)
	e.Msg("ctx attached through a chained mutating statement - should NOT trigger")
}

// reviewMutationParenthesised: parentheses around a mutating statement are
// transparent.
func reviewMutationParenthesised() {
	ctx := context.Background()
	e := log.Info()
	(e.Ctx(ctx))
	e.Msg("parenthesised mutating statement - should NOT trigger")
}

// FieldHolder pins positive tracking of struct-field assignments (the
// Selections branch of objectFromExpr). A dedicated type keeps the shared
// per-field fact from leaking into the App fixtures in edge_cases.go.
type FieldHolder struct {
	logger zerolog.Logger
}

// reviewFieldAssignTracked: assigning a ctx logger to a struct field is
// tracked via the field's object.
func reviewFieldAssignTracked() {
	ctx := context.Background()
	h := &FieldHolder{}
	h.logger = log.With().Ctx(ctx).Logger()
	h.logger.Info().Msg("field assignment tracked - should NOT trigger")
}

// reviewPointerToCtxLogger: pointers and address-taking are transparent for
// logger tracking.
func reviewPointerToCtxLogger() {
	ctx := context.Background()
	ctxLogger := log.With().Ctx(ctx).Logger()
	p := &ctxLogger
	p.Info().Msg("pointer to ctx logger - should NOT trigger")
	(&ctxLogger).Info().Msg("inline address-of - should NOT trigger")
}

// reviewClosureUsesLaterAssignment: a use that precedes every recorded
// assignment falls back to the earliest fact (the documented approximation
// for closures).
func reviewClosureUsesLaterAssignment() {
	var l zerolog.Logger
	f := func() {
		l.Info().Msg("closure body precedes the assignment - should NOT trigger")
	}
	l = log.With().Ctx(context.Background()).Logger()
	f()
}

// reviewNilCtxMidChain: the dedicated non-context-argument message also fires
// when Ctx(nil) is not the last call before the terminal.
func reviewNilCtxMidChain() {
	log.Info().Ctx(nil).Str("k", "v").Msg("nil ctx mid-chain") // want "zerolog event calls Ctx\\(\\) with a non-context argument before Msg\\(\\) - pass a context.Context for proper log correlation"
}

// reviewVarTupleDecl: a multi-name var declaration backed by a single call is
// treated like a tuple assignment — the logger is not tracked.
func reviewVarTupleDecl() {
	ctx := context.Background()
	_ = ctx
	var vlogger, verr = getLoggerAndErr()
	_ = verr
	vlogger.Info().Msg("var tuple declaration - must trigger") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}
