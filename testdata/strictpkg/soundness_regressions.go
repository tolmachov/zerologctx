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
	defer event.Msg("may run before or after the context is cleared") // want `zerolog output is not proven to carry context before Msg\(\)`
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

// A self-recursive function reads its own summary, so its first round is not
// final: the recursive call reads a summary still at the bottom, and only a
// second round joins in the no-context return. helperAfterSelf is a later
// local call, which must not erase that the function calls itself.
func selfRecursiveSecondRound(ctx context.Context, depth int) zerolog.Logger {
	var logger zerolog.Logger
	if depth > 0 {
		logger = selfRecursiveSecondRound(ctx, depth-1)
		helperAfterSelf()
	} else {
		logger = zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	logger.Info().Msg("own result is joined with a no-context return") // want `zerolog output is not proven to carry context before Msg\(\)`
	return zerolog.New(io.Discard)
}

func helperAfterSelf() {}

var dropHookContext bool

// A function that passes itself as a hook reads its own summary through a
// function value rather than a call. The second branch puts the sink in a
// block of its own, where a stale first-round state would still be visible.
func selfHook(event *zerolog.Event) {
	if dropHookContext {
		event.Ctx(nil)
		return
	}
	event.Ctx(context.Background()).Func(selfHook)
	if dropHookContext {
		println()
	}
	event.Msg("own effect may drop the context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func useSelfHook(ctx context.Context) {
	log.Info().Ctx(ctx).Func(selfHook).Msg("hook may drop the context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func inspectOnly(event *zerolog.Event) bool { return event.Enabled() }

// A helper that never writes its parameter preserves what the caller proved.
func untouchedParameterKeepsProof(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	inspectOnly(event)
	event.Msg("helper never writes the event")
}

// A function without an explicit exit still runs its goroutines, and its
// defers on a runtime panic: both are judged against every state after their
// statement, and a context proven there and never cleared holds throughout.
func goroutineSinkInEndlessLoop(ctx context.Context, work func()) {
	defer log.Info().Msg("runs if work panics") // want `zerolog output is not proven to carry context before Msg\(\)`
	event := log.Info().Ctx(ctx)
	go event.Msg("proven at every later state")
	for {
		work()
		log.Info().Ctx(ctx).Msg("proven")
	}
}

func unprovenSinkInEndlessLoop() {
	for {
		log.Info().Msg("inside endless loop") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

type messenger interface{ Msg(msg string) }

// Whether a deferred or concurrent call is a sink is decided at its statement,
// where the receiver behind the interface is known, not at the exit.
func goroutineInterfaceSinkInEndlessLoop() {
	var output messenger = log.Info()
	go output.Msg("dispatched through an interface") // want `zerolog output is not proven to carry context before Msg\(\)`
	for {
	}
}

func goroutineInterfaceSinkOffTheExitPath(cond bool) {
	if cond {
		var output messenger = log.Info()
		go output.Msg("receiver absent from the exit state") // want `zerolog output is not proven to carry context before Msg\(\)`
		for {
		}
	}
	panic("exit without the receiver")
}

type contextDropper interface{ Drop() }

type heldEvent struct{ event *zerolog.Event }

func (h *heldEvent) Drop() { h.event.Ctx(nil) }

// A local struct that escapes behind an interface takes everything it holds
// with it.
func interfaceHidesHeldEvent(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	var dropper contextDropper = &heldEvent{event: event}
	dropper.Drop()
	event.Msg("dropped through the interface") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func channelHidesHeldEvent(ctx context.Context, ch chan any) {
	event := log.Info().Ctx(ctx)
	ch <- any(&heldEvent{event: event})
	event.Msg("sent behind an interface") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A goroutine may run at any moment after its statement, so a context attached
// later proves nothing about what it logs.
func goroutineSeesStateBeforeLaterCtx(ctx context.Context, logger zerolog.Logger) {
	event := logger.Info()
	go event.Msg("may run before Ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
	event.Ctx(ctx)
}

// A deferred call runs on a panic anywhere after its statement, not only at
// the explicit exits.
func deferRunsOnPanicBeforeCtx(ctx context.Context, work func()) {
	event := log.Info()
	defer event.Msg("runs if work panics") // want `zerolog output is not proven to carry context before Msg\(\)`
	work()
	event.Ctx(ctx)
}

var initializedByClosure = func() int {
	log.Info().Msg("package initializer") // want `zerolog output is not proven to carry context before Msg\(\)`
	return 0
}()

func init() {
	log.Info().Msg("declared init") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A nil context attached before the statement holds at every later state.
func deferredNilCtxHoldsThroughout(work func()) {
	defer log.Info().Ctx(nil).Msg("nil at every later state") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
	work()
}

// A goroutine spawned in a loop may still be waiting when the next iteration
// clears the context, before this one's statement is reached again.
func goroutineSeesNextIteration(ctx context.Context, n int) {
	event := log.Info().Ctx(ctx)
	for range n {
		event.Ctx(nil)
		event.Ctx(ctx)
		go event.Msg("the next iteration clears the context first") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

// Deferred calls run last in, first out, so one registered after a deferred
// sink runs before it, on every exit and every panic.
func deferredSinkAfterDeferredClear(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	defer event.Msg("the later defer runs first") // want `zerolog output is not proven to carry context before Msg\(\)`
	defer event.Ctx(nil)
}

type dropOnMarshal struct{}

func (dropOnMarshal) MarshalZerologObject(event *zerolog.Event) { event.Ctx(nil) } // want MarshalZerologObject:"zerolog context summary results=\\[\\] effects=\\[preserved no-context\\]"

// A zerolog interface dispatches to whatever implements it, which is not
// zerolog's own code.
func zerologInterfaceRunsUserCode(ctx context.Context, marshaler zerolog.LogObjectMarshaler) {
	event := log.Info().Ctx(ctx)
	marshaler.MarshalZerologObject(event)
	event.Msg("user code behind a zerolog interface") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A deferred call registered after a deferred sink runs before it on a panic
// too, not only at the explicit exits this function does not have.
func deferredClearOnPanicInEndlessLoop(ctx context.Context, work func()) {
	event := log.Info().Ctx(ctx)
	defer event.Msg("the later defer runs first on a panic") // want `zerolog output is not proven to carry context before Msg\(\)`
	defer event.Ctx(nil)
	for {
		work()
	}
}

// A package-level log sink takes no zerolog value, so a function holding only
// that is analysed all the same.
func packageLevelSinkOnly(message string) {
	log.Print(message) // want `zerolog output is not proven to carry context before Print\(\)`
}
