package strictpkg

import (
	"context"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func absentMemoryJoin(ctx context.Context, cond bool) {
	var logger zerolog.Logger
	if cond {
		logger = zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	logger.Info().Msg("zero-value predecessor") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func loopRevisit(ctx context.Context) {
	plain := zerolog.New(io.Discard)
	logger := plain.With().Ctx(ctx).Logger()
	for range 2 {
		logger.Info().Msg("later iteration is unsafe") // want `zerolog output is not proven to carry context before Msg\(\)`
		logger = plain
	}
}

func resetAny(value any) {
	logger := value.(*zerolog.Logger)
	*logger = zerolog.New(io.Discard)
}

func boxedPointerAlias(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	resetAny(&logger)
	logger.Info().Msg("boxed pointer was reset") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func eventPair(ctx context.Context) (*zerolog.Event, *zerolog.Event) {
	return log.Info(), log.Info().Ctx(ctx)
}

func tupleResults(ctx context.Context) {
	unsafe, safe := eventPair(ctx)
	unsafe.Msg("first tuple result") // want `zerolog output is not proven to carry context before Msg\(\)`
	safe.Msg("second tuple result")
}

func deferredSink(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	defer event.Msg("observes the final event state") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
	event.Ctx(nil)
}

func deferredMutationDoesNotRunEarly(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	defer event.Ctx(nil)
	event.Msg("still contextual before function exit")
}

func clearContextOnReturn(event *zerolog.Event) {
	defer event.Ctx(nil)
}

func deferredHelperEffect(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearContextOnReturn(event)
	event.Msg("helper cleared context on return") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func clearLoggerOnReturn(logger *zerolog.Logger) {
	*logger = zerolog.New(io.Discard)
}

func loggerClearedByDefer(ctx context.Context) (logger zerolog.Logger) {
	logger = zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	defer clearLoggerOnReturn(&logger)
	return
}

func deferredNamedResult(ctx context.Context) {
	logger := loggerClearedByDefer(ctx)
	logger.Info().Msg("named result changed by defer") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func deferredSinkOnPanic() {
	defer log.Info().Msg("panic still runs defers") // want `zerolog output is not proven to carry context before Msg\(\)`
	panic("boom")
}

func eventFunc(ctx context.Context) {
	log.Info().Ctx(ctx).Func(func(event *zerolog.Event) {
		event.Ctx(nil)
	}).Msg("callback removed context") // want `zerolog output is not proven to carry context before Msg\(\)`

	log.Info().Func(func(event *zerolog.Event) {
		event.Ctx(ctx)
	}).Msg("callback attached context")
}

type box struct{ logger zerolog.Logger }

func resetBox(b *box) { b.logger = zerolog.New(io.Discard) }

func (b *box) reset() { b.logger = zerolog.New(io.Discard) }

func fieldClobberThroughPointer(ctx context.Context) {
	b := box{logger: zerolog.New(io.Discard).With().Ctx(ctx).Logger()}
	resetBox(&b)
	b.logger.Info().Msg("helper replaced the field") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func fieldClobberThroughMethod(ctx context.Context) {
	b := box{logger: zerolog.New(io.Discard).With().Ctx(ctx).Logger()}
	b.reset()
	b.logger.Info().Msg("method replaced the field") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func aggregateStoreKeepsStaleField(ctx context.Context, fresh box) {
	b := box{logger: zerolog.New(io.Discard).With().Ctx(ctx).Logger()}
	b = fresh
	b.logger.Info().Msg("whole-struct assignment replaced the field") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func closureClobbersCapturedLogger(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	reset := func() { logger = zerolog.New(io.Discard) }
	reset()
	logger.Info().Msg("closure replaced the logger") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func goroutineClosureClobbersCapturedEvent(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	go func() { event.Ctx(nil) }()
	event.Msg("goroutine may clear the context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func storeThroughPhiAddress(ctx context.Context, cond bool) {
	contextual := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	plain := zerolog.New(io.Discard)
	target := &contextual
	if cond {
		target = &plain
	}
	*target = zerolog.New(io.Discard)
	contextual.Info().Msg("assignment through an unresolved address") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func summaryEffectThroughLoadedPointer(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	indirect := struct{ pointer *zerolog.Logger }{pointer: &logger}
	clearLoggerOnReturn(indirect.pointer)
	logger.Info().Msg("helper cleared through a loaded pointer") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func goAppliesEffectAtSpawn(ctx context.Context) {
	event := log.Info()
	go attachLocal(event, ctx)
	event.Msg("goroutine postcondition is not established here") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func pingLogger(ctx context.Context, depth int) zerolog.Logger {
	if depth <= 0 {
		return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	return pongLogger(ctx, depth-1)
}

func pongLogger(ctx context.Context, depth int) zerolog.Logger {
	return pingLogger(ctx, depth-1)
}

func mutualRecursion(ctx context.Context) {
	logger := pingLogger(ctx, 4)
	logger.Info().Msg("safe across a multi-function component")
}

func panicLogger() zerolog.Logger { panic("no logger") }

// A call that can only panic makes everything after it unreachable, so the
// output operations there can never execute and none is reported. This is
// reachability, not a proof about context.
func afterAlwaysPanickingCall() {
	unreachable := panicLogger()
	unreachable.Info().Msg("unreachable output")
}

func unreachableBranchDoesNotWeakenTheJoin(ctx context.Context, cond bool) {
	var event *zerolog.Event
	if cond {
		unreachable := panicLogger()
		event = unreachable.Info()
	} else {
		event = log.Info().Ctx(ctx)
	}
	event.Msg("only the reachable branch decides")
}

func builderFinalContextIsNil(ctx context.Context) {
	builder := zerolog.New(io.Discard).With().Ctx(ctx).Ctx(nil)
	logger := builder.Logger()
	logger.Info().Msg("builder's final Ctx was nil") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
}

func eventAndError(ctx context.Context) (*zerolog.Event, error) {
	return log.Info().Ctx(ctx), nil
}

func plainEventAndError() (*zerolog.Event, error) {
	return log.Info(), nil
}

func mixedTupleResults(ctx context.Context) {
	safe, _ := eventAndError(ctx)
	safe.Msg("proven tuple element")
	unsafe, _ := plainEventAndError()
	unsafe.Msg("unproven tuple element") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func eventFuncNilCallback(ctx context.Context) {
	log.Info().Ctx(ctx).Func(nil).Msg("a nil callback cannot change the event")
}

func deferredInterfaceSink() {
	var sink interface{ Msg(string) } = log.Info()
	defer sink.Msg("deferred interface sink") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func resetBuilder(value any) {
	builder := value.(*zerolog.Context)
	*builder = zerolog.New(io.Discard).With()
}

func boxedBuilderAlias(ctx context.Context) {
	builder := zerolog.New(io.Discard).With().Ctx(ctx)
	resetBuilder(&builder)
	logger := builder.Logger()
	logger.Info().Msg("the boxed builder was reset") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func attachThroughUpdateContext(logger *zerolog.Logger, ctx context.Context) {
	logger.UpdateContext(func(builder zerolog.Context) zerolog.Context { return builder.Ctx(ctx) })
}

func updateContextOnParameter(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	attachThroughUpdateContext(&logger, ctx)
	logger.Info().Msg("the helper attached through UpdateContext")
}

func nolintScope(ctx context.Context) {
	//nolint:zerologctx // standalone directive above the sink
	log.Info().Msg("suppressed by the directive above")

	log.Info().Ctx(ctx).Msg("safe")                                     //nolint:zerologctx
	log.Info().Msg("the previous line's directive does not reach here") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func nolintChainStartLine() {
	log.Info(). //nolint:zerologctx
			Str("key", "value").
			Msg("suppressed from the chain's first line")
}

func nolintInteriorChainLine() {
	log.Info().
		Str("key", "value"). //nolint:zerologctx
		Msg("suppressed from an interior chain line")
}
