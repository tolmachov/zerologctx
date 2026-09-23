package strictpkg

import (
	"context"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// A postcondition may only be written into a location the analyzer proved the
// argument denotes. Writing it into every member of a may-alias set proves
// something about values the call never touched.

func attachEvent(event *zerolog.Event, ctx context.Context) { event.Ctx(ctx) }

func summaryEffectOnAliasedEvents(ctx context.Context, cond bool) {
	first := log.Info()
	second := log.Info()
	chosen := first
	if cond {
		chosen = second
	}
	attachEvent(chosen, ctx)
	first.Msg("only one of the two was attached")  // want `zerolog output is not proven to carry context before Msg\(\)`
	second.Msg("only one of the two was attached") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func directCtxOnAliasedEvents(ctx context.Context, cond bool) {
	first := log.Info()
	second := log.Info()
	chosen := first
	if cond {
		chosen = second
	}
	chosen.Ctx(ctx)
	first.Msg("only one of the two was attached")  // want `zerolog output is not proven to carry context before Msg\(\)`
	second.Msg("only one of the two was attached") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func summaryEffectOnAliasedLoggers(ctx context.Context, cond bool) {
	contextual := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	other := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	target := &contextual
	if cond {
		target = &other
	}
	clearLoggerOnReturn(target)
	contextual.Info().Msg("a may-alias set carries no postcondition") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var sharedLogger *zerolog.Logger

func attachLoggerContext(logger *zerolog.Logger, ctx context.Context) {
	*logger = logger.With().Ctx(ctx).Logger()
}

func summaryEffectOnPartialAliasSet(ctx context.Context, cond bool) {
	local := zerolog.New(io.Discard)
	target := &local
	if cond {
		target = sharedLogger
	}
	attachLoggerContext(target, ctx)
	local.Info().Msg("the alias set does not account for every path") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func dropContext(zerolog.Context) zerolog.Context { return zerolog.New(io.Discard).With() }

func updateContextThroughAliasSet(ctx context.Context, cond bool) {
	contextual := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	other := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	target := &contextual
	if cond {
		target = &other
	}
	target.UpdateContext(dropContext)
	contextual.Info().Msg("UpdateContext through a may-alias address") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// Writing a tracked value into memory the analyzer cannot name is an escape.
// Widening the destination is not enough: the value itself walks out.

var stashedEvent *zerolog.Event

func clearStashedEvent() { stashedEvent.Ctx(nil) }

func escapesThroughGlobal(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	stashedEvent = event
	clearStashedEvent()
	event.Msg("stored into a global") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func clearAllEvents(events ...*zerolog.Event) {
	for _, event := range events {
		event.Ctx(nil)
	}
}

func escapesThroughVariadicPack(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearAllEvents(event)
	event.Msg("a variadic pack is still an escape") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func escapesThroughMap(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	byKey := map[string]*zerolog.Event{}
	byKey["key"] = event
	clearContextOnReturn(byKey["key"])
	event.Msg("stored into a map") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func escapesThroughChannel(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	events := make(chan *zerolog.Event, 1)
	events <- event
	clearContextOnReturn(<-events)
	event.Msg("sent on a channel") // want `zerolog output is not proven to carry context before Msg\(\)`
}

type eventBox struct{ event *zerolog.Event }

func clearBoxed(box eventBox) { box.event.Ctx(nil) }

func escapesInsideStructByValue(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearBoxed(eventBox{event: event})
	event.Msg("escaped inside a struct passed by value") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func clearOpaque(value any) { value.(*zerolog.Event).Ctx(nil) }

func escapesThroughInterfaceBox(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearOpaque(event)
	event.Msg("escaped through an interface") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func opaqueCalleeMayClearEvent(ctx context.Context, sink func(*zerolog.Event)) {
	event := log.Info().Ctx(ctx)
	sink(event)
	event.Msg("an opaque callee may have cleared the context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A closure the analyzer does not follow may run anywhere, including inside a
// range-over-func loop body or after the closure value has left the function.

func runYield(yield func(int) bool) {
	for index := range 3 {
		if !yield(index) {
			return
		}
	}
}

func rangeOverFuncClobbersCapture(ctx context.Context) {
	logger := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	for range runYield {
		logger = zerolog.New(io.Discard)
	}
	logger.Info().Msg("the loop body replaced the logger") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func callLater(attach func(context.Context) *zerolog.Event) { attach(nil) }

func boundMethodEscapes(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	callLater(event.Ctx)
	event.Msg("a bound method value escaped") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A goroutine runs at an unknown moment, so a sink it carries observes every
// mutation the spawning function performs afterwards.
func goSinkObservesLaterMutation(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	go event.Msg("the goroutine may run before or after the context is cleared") // want `zerolog output is not proven to carry context before Msg\(\)`
	event.Ctx(nil)
}

type eventPairStruct struct{ first, second *zerolog.Event }

func assertAggregateElement(ctx context.Context) {
	pair := eventPairStruct{first: log.Info().Ctx(ctx), second: log.Info()}
	var boxed any = pair
	event, ok := boxed.(*zerolog.Event)
	_ = ok
	event.Msg("an aggregate is not the asserted value") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A nil final Ctx must hold on every path before it is reported as such.
func nilCtxOnOnlyOnePath(cond bool) {
	logger := zerolog.New(io.Discard)
	event := logger.Info()
	if cond {
		event.Ctx(nil)
	} else {
		event.Str("key", "value")
	}
	event.Msg("only one path passed a nil context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func pingPlain(depth int) zerolog.Logger {
	if depth <= 0 {
		return zerolog.New(io.Discard)
	}
	return pongPlain(depth - 1)
}

func pongPlain(depth int) zerolog.Logger { return pingPlain(depth - 1) }

func mutualRecursionWithoutContext() {
	logger := pingPlain(4)
	logger.Info().Msg("no branch of the cycle proves a context") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func plainEvent() *zerolog.Event { return log.Info() }

func summarizedEventResult() {
	event := plainEvent()
	event.Msg("a helper returned an unproven event") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func nolintOnAClosingLine(cond bool) {
	if cond {
		_ = 1
	} //nolint:zerologctx
	log.Info().Msg("a directive sharing a line with code does not reach here") // want `zerolog output is not proven to carry context before Msg\(\)`
}

// A method value stored in a field is called through that field, so nothing
// links the call back to a zerolog method. This is the dynamic-call boundary:
// a sink the analyzer cannot recognise is a sink it cannot report.
type emitter struct{ emit func(string) }

func methodValueInAField() {
	event := log.Info()
	holder := emitter{emit: event.Msg}
	holder.emit("outside the analyzer's boundary")
}

// buildssa does not instantiate generics, so a generic body is analysed once
// with unknown type arguments and its sink is judged there.
func emitThrough[T any](event *zerolog.Event, _ T) {
	event.Msg("a sink inside a generic function") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func genericSink(ctx context.Context) {
	emitThrough(log.Info(), 1)
	emitThrough(log.Info().Ctx(ctx), "value")
}
