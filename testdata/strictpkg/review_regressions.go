package strictpkg

import (
	"context"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func outputDropsContext(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger().Output(io.Discard)
	logger.Info().Msg("Output rebuilds the logger with New") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func updateContextNeverAttaches(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	logger.UpdateContext(func(builder zerolog.Context) zerolog.Context { return builder.Ctx(ctx) })
	logger.Info().Msg("UpdateContext copies fields back, never the context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func createDictIsANewEvent(ctx context.Context) {
	event := log.Info()
	dict := event.CreateDict()
	dict.Ctx(ctx)
	event.Dict("k", dict).Msg("only the dict got a context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func embedObjectRunsUserCode(ctx context.Context) {
	log.Info().Ctx(ctx).EmbedObject(dropOnMarshal{}).Msg("the marshaler cleared ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
	log.Info().Ctx(ctx).Object("k", dropOnMarshal{}).Msg("the marshaler cleared ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func loopCreatedEventKeepsItsOwnState(ctx context.Context, n int) {
	logger := zerolog.New(io.Discard)
	saved := logger.Info().Ctx(ctx)
	for range n {
		current := logger.Info().Ctx(ctx)
		saved.Msg("an older event from the same call site") // want `zerolog output is not proven to carry context before Msg\(\)`
		current.Ctx(nil)
		saved = current
	}
}

func closureCalledAfterReproof(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	reset := func() { logger = zerolog.New(io.Discard) }
	logger = logger.With().Ctx(ctx).Logger()
	reset()
	logger.Info().Msg("the closure reset the captured logger") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func deferredClosureRewritesResult(ctx context.Context) (logger zerolog.Logger) {
	defer func() { logger = zerolog.New(io.Discard) }()
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func useDeferredClosureResult(ctx context.Context) {
	logger := deferredClosureRewritesResult(ctx)
	logger.Info().Msg("the deferred closure replaced the result") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var escapedLogger *zerolog.Logger

func clobberEscapedLogger() { *escapedLogger = zerolog.New(io.Discard) }

func globalAliasClobbered(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	escapedLogger = &logger
	logger = logger.With().Ctx(ctx).Logger()
	clobberEscapedLogger()
	logger.Info().Msg("a global alias was written through") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func identityPointer(logger *zerolog.Logger) *zerolog.Logger { return logger }

func pointerResultAlias(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	alias := identityPointer(&logger)
	*alias = zerolog.New(io.Discard)
	logger.Info().Msg("written through a returned alias") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func storeThroughReturnedAlias(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	plain := logger
	alias := identityPointer(&logger)
	logger = logger.With().Ctx(ctx).Logger()
	*alias = plain
	logger.Info().Msg("written through the alias the callee returned") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func sliceOfPointersAlias(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	pointers := []*zerolog.Logger{&logger}
	*pointers[0] = zerolog.New(io.Discard)
	logger.Info().Msg("written through a slice element") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func pointerInStructSeesLaterStore(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	holder := struct{ logger *zerolog.Logger }{logger: &logger}
	logger = zerolog.New(io.Discard)
	holder.logger.Info().Msg("the pointee was replaced") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func phiPointerSeesLaterStore(ctx context.Context, cond bool) {
	first := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	second := first
	target := &first
	if cond {
		target = &second
	}
	first = zerolog.New(io.Discard)
	target.Info().Msg("the pointee was replaced") // want `zerolog output is not proven to carry context before Msg\(\)`
}

type eventHolder struct{ event *zerolog.Event }

func untrackedEventAlias(ctx context.Context, holder *eventHolder) {
	event := holder.event.Ctx(ctx)
	holder.event.Ctx(nil)
	event.Msg("another alias of the same event cleared it") // want `zerolog output is not proven to carry context before Msg\(\)`
}

type loggerHolder struct{ logger zerolog.Logger }

func makeLoggerHolder() loggerHolder { return loggerHolder{logger: zerolog.New(io.Discard)} }

func overwriteThroughLoadedStruct(target *zerolog.Logger) {
	holder := makeLoggerHolder()
	*target = holder.logger
}

func callerOfOverwrite(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	overwriteThroughLoadedStruct(&logger)
	logger.Info().Msg("the callee overwrote the logger") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func recoveredResult(ctx context.Context, work func()) (logger zerolog.Logger) {
	defer func() { _ = recover() }()
	logger = zerolog.New(io.Discard)
	work()
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func useRecoveredResult(ctx context.Context, work func()) {
	logger := recoveredResult(ctx, work)
	logger.Info().Msg("a recovered panic returns the plain logger") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func loggerFromContextIsNotProof(ctx context.Context) {
	zerolog.Ctx(ctx).Info().Msg("retrieved, not attached") // want `zerolog output is not proven to carry context before Msg\(\)`
	log.Ctx(ctx).Info().Msg("retrieved, not attached")     // want `zerolog output is not proven to carry context before Msg\(\)`
	log.Ctx(ctx).Info().Ctx(ctx).Msg("safe")
}

func escapesThroughSelectSend(ctx context.Context, done chan struct{}) {
	event := log.Info().Ctx(ctx)
	events := make(chan *zerolog.Event, 1)
	select {
	case events <- event:
	case <-done:
	}
	clearContextOnReturn(<-events)
	event.Msg("sent from a select case") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func deferredInterfaceCallDropsContext(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	defer event.Msg("Drop runs first") // want `zerolog output is not proven to carry context before Msg\(\)`
	var dropper contextDropper = &heldEvent{event: event}
	defer dropper.Drop()
}

type genericEmitter[T any] struct{ event *zerolog.Event }

func (g genericEmitter[T]) emit() {
	g.event.Msg("a sink in a generic method") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func clearGeneric[T any](event *zerolog.Event, _ T) { event.Ctx(nil) }

func genericHelperClears(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearGeneric(event, 0)
	event.Msg("the generic helper cleared ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func loopLoggingIsProven(ctx context.Context, n int) {
	for range n {
		log.Info().Ctx(ctx).Msg("each iteration's event is the only one its site denotes")
	}
}

// A deferred call in a loop holds the event of every earlier iteration, so the
// site's events share one identity and a Ctx on the newest is not proven.
func deferredSinkInLoop(ctx context.Context, n int) {
	for range n {
		event := log.Info().Ctx(ctx)
		defer event.Msg("an earlier iteration's event shares the site") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

func keepLogger(logger *zerolog.Logger) {
	escapedLogger = logger
	logger.Info().Ctx(context.Background()).Msg("kept")
}

func helperKeepsAlias(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	keepLogger(&logger)
	logger = logger.With().Ctx(ctx).Logger()
	logger.Info().Msg("no call since the context was attached")
	clobberEscapedLogger()
	logger.Info().Msg("the helper's alias was written through") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func untrackedEventChainIsProven(ctx context.Context, holder *eventHolder) {
	holder.event.Ctx(ctx).Msg("the chain itself carries the context")
}

func trackedAliasOfUntrackedEvent(ctx context.Context, holder *eventHolder) {
	event := log.Info()
	holder.event = event
	contextual := holder.event.Ctx(ctx)
	event.Ctx(nil)
	contextual.Msg("the escaped event was cleared through its tracked alias") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func createDictKeepsParentContext(ctx context.Context) {
	parent := log.Info().Ctx(ctx)
	dict := parent.CreateDict()
	parent.Ctx(nil)
	dict.Msg("the dict was seeded before the parent changed")
}

type keepOnMarshal struct{}

func (keepOnMarshal) MarshalZerologObject(event *zerolog.Event) { event.Str("k", "v") }

func marshalerSummaries(ctx context.Context, marshaler zerolog.LogObjectMarshaler) {
	log.Info().Ctx(ctx).Object("k", keepOnMarshal{}).Msg("the marshaler keeps the context")
	log.Info().Ctx(ctx).EmbedObject(&keepOnMarshal{}).Msg("the marshaler keeps the context")
	log.Info().Ctx(ctx).Object("k", nil).Msg("a nil marshaler is never called")
	log.Info().Ctx(ctx).Object("k", marshaler).Msg("an unknown marshaler") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var heldEvents = make(chan *zerolog.Event, 1)

func callbackKeepsEvent(ctx context.Context) {
	event := log.Info().Ctx(ctx).Func(func(event *zerolog.Event) { heldEvents <- event })
	clearContextOnReturn(<-heldEvents)
	event.Msg("the callback let the event escape") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func deferWithoutRecover(ctx context.Context) (logger zerolog.Logger) {
	defer func() { log.Info().Ctx(ctx).Msg("no recover here") }()
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func useDeferWithoutRecover(ctx context.Context) {
	logger := deferWithoutRecover(ctx)
	logger.Info().Msg("no deferred call can stop a panic")
}

func untrackedEventChainInLoop(ctx context.Context, holder *eventHolder, n int) {
	for range n {
		holder.event.Ctx(ctx).Msg("each iteration proves its own chain")
	}
}

func errFieldOnUntrackedChain(ctx context.Context, holder *eventHolder, err error) {
	holder.event.Ctx(ctx).Err(err).Msg("Err takes an error, not the event")
}

var escapedEvent *zerolog.Event

type clearingError struct{}

func (clearingError) Error() string {
	escapedEvent.Ctx(nil)
	return "cleared"
}

// Code zerolog runs on its own while it builds or writes an event - an Error
// or String method, a writer, a hook, a sampler - is not followed: this is
// the boundary the README documents, and this case sits beyond it.
func errorMethodClearsEscapedEvent(ctx context.Context) {
	event := log.Info()
	escapedEvent = event
	event.Ctx(ctx).Err(clearingError{}).Msg("beyond the analyzer's boundary")
}

func deferredBuiltin(ctx context.Context, done chan struct{}) zerolog.Logger {
	defer close(done)
	return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
}

func useDeferredBuiltin(ctx context.Context, done chan struct{}) {
	logger := deferredBuiltin(ctx, done)
	logger.Info().Msg("a deferred builtin never recovers a panic")
}

func paramEventChain(ctx context.Context, event *zerolog.Event) {
	event.Ctx(ctx).Msg("a chain on a parameter event")
}

func AttachAndReturn(event *zerolog.Event, ctx context.Context) *zerolog.Event { return event.Ctx(ctx) } // want AttachAndReturn:"zerolog context summary results=\\[has-context\\] returns=\\[1\\] effects=\\[has-context preserved\\] escapes=\\[false false\\]"

func resultIsTheArgument(ctx context.Context) {
	event := log.Info()
	returned := AttachAndReturn(event, ctx)
	event.Ctx(nil)
	returned.Msg("the result is the argument, which was cleared") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
}

func argumentIsTheResult(ctx context.Context) {
	event := log.Info()
	returned := AttachAndReturn(event, ctx)
	event.Ctx(ctx)
	returned.Ctx(nil)
	event.Msg("the argument is the result, which was cleared") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
}

func AddFields(event *zerolog.Event, id string) *zerolog.Event { return event.Str("id", id) } // want AddFields:"zerolog context summary results=\\[unknown\\] returns=\\[1\\] effects=\\[preserved preserved\\] escapes=\\[false false\\]"

func decorateInPlace(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	AddFields(event, "x")
	event.Msg("the helper returns the event it decorated")
}

func boxEvent(event *zerolog.Event) []*zerolog.Event { return []*zerolog.Event{event} }

func resultReachesTheArgument(ctx context.Context) {
	event := log.Info()
	boxed := boxEvent(event)
	event.Ctx(ctx)
	boxed[0].Ctx(nil)
	event.Msg("the result holds the argument") // want `zerolog output is not proven to carry context before Msg\(\)`
}

type clearer struct{}

func (clearer) Clear(event *zerolog.Event) { event.Ctx(nil) } // want Clear:"zerolog context summary results=\\[\\] returns=\\[\\] effects=\\[preserved no-context\\] escapes=\\[false false\\]"

func boundMethodCallback(ctx context.Context) {
	log.Info().Ctx(ctx).Func(clearer{}.Clear).Msg("the bound method cleared ctx") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var keptEvent *zerolog.Event

func clearKeptEvent() { keptEvent.Ctx(nil) }

func unknownCallbackMayKeepEvent(ctx context.Context, callback func(*zerolog.Event), marshaler zerolog.LogObjectMarshaler) {
	event := log.Info()
	event.Func(callback)
	event.Ctx(ctx)
	clearKeptEvent()
	event.Msg("the callback may have kept the event") // want `zerolog output is not proven to carry context before Msg\(\)`

	other := log.Info()
	other.EmbedObject(marshaler)
	other.Ctx(ctx)
	clearKeptEvent()
	other.Msg("the marshaler may have kept the event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func attachThenClearKept(event *zerolog.Event) {
	event.Ctx(context.Background())
	keptEvent.Ctx(nil)
}

func callbackIntoEscapedReceiver() {
	event := log.Info()
	keptEvent = event
	event.Func(attachThenClearKept).Msg("the callback cleared it through the escaped alias") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func replaceEvent(target **zerolog.Event, ctx context.Context) { *target = log.Info().Ctx(ctx) }

func replacingIsNotMutating(ctx context.Context) {
	event := log.Info()
	original := event
	replaceEvent(&event, ctx)
	original.Msg("the original event never got a context") // want `zerolog output is not proven to carry context before Msg\(\)`
	event.Msg("a pointer to a pointer is not followed")    // want `zerolog output is not proven to carry context before Msg\(\)`
}

func stash(target **zerolog.Event, event *zerolog.Event) { *target = event }

func stashedParameterEscapes(ctx context.Context) {
	var holder *zerolog.Event
	event := log.Info()
	stash(&holder, event)
	event.Ctx(ctx)
	holder.Ctx(nil)
	event.Msg("holder is the event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func twoLoggerParams(ctx context.Context, first, second *zerolog.Logger, plain zerolog.Logger) {
	*second = second.With().Ctx(ctx).Logger()
	*first = plain
	second.Info().Msg("first may be second") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func twoEventParams(ctx context.Context, first, second *zerolog.Event) {
	first.Ctx(ctx)
	second.Ctx(nil)
	first.Msg("second may be first") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func paramMayBeAGlobal(ctx context.Context, event *zerolog.Event) {
	event.Ctx(ctx)
	keptEvent.Ctx(nil)
	event.Msg("the global may be the parameter") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var escapedSlot **zerolog.Event

func storeIntoEscapedSlot(ctx context.Context) {
	var slot *zerolog.Event
	escapedSlot = &slot
	event := log.Info().Ctx(ctx)
	slot = event
	(*escapedSlot).Ctx(nil)
	event.Msg("the escaped slot now reaches the event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func callbackClearsAnEscapedEvent(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	event := logger.Info()
	keptEvent = event
	event.Ctx(ctx)
	logger.Info().Ctx(ctx).Func(func(*zerolog.Event) { clearKeptEvent() }).Send()
	event.Msg("the callback cleared the escaped event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func updateContextCallbackClearsAnEscapedEvent(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	event := log.Info()
	keptEvent = event
	event.Ctx(ctx)
	logger.UpdateContext(func(builder zerolog.Context) zerolog.Context {
		clearKeptEvent()
		return builder
	})
	event.Msg("the callback cleared the escaped event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func doWork() {}

// Code a function calls is assumed to reach its parameters' targets only
// through the arguments it is passed, as every summary assumes; a global alias
// the caller set up is the caller's to account for.
func attachThenCallThenLog(ctx context.Context, logger *zerolog.Logger, event *zerolog.Event) {
	*logger = logger.With().Ctx(ctx).Logger()
	event.Ctx(ctx)
	doWork()
	logger.Info().Msg("a call does not reach the parameter")
	event.Msg("a call does not reach the parameter")
}

func loggerParamMayBeAGlobal(ctx context.Context, logger *zerolog.Logger, plain zerolog.Logger) {
	*logger = logger.With().Ctx(ctx).Logger()
	*escapedLogger = plain
	logger.Info().Msg("the global may be the parameter's target") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func attachThenClobber(logger *zerolog.Logger, ctx context.Context) {
	*logger = logger.With().Ctx(ctx).Logger()
	clobberEscapedLogger()
}

func effectIntoEscapedArgument(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	escapedLogger = &logger
	attachThenClobber(&logger, ctx)
	logger.Info().Msg("the callee's call wrote through the alias this function made") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func callbackAttachesThenCallsAClear(ctx context.Context) {
	logger := zerolog.New(io.Discard)
	event := logger.Info()
	keptEvent = event
	event.Func(func(event *zerolog.Event) {
		event.Ctx(ctx)
		clearKeptEvent()
	}).Msg("the callback's call cleared the escaped event") // want `zerolog output is not proven to carry context before Msg\(\)`
}
