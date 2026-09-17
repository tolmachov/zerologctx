package strictpkg

import (
	"context"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Event = zerolog.Event

func strictWithoutCandidate() {
	log.Info().Msg("strict") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func inlineContext(ctx context.Context) {
	log.Info().Ctx(ctx).Msg("safe")
	log.Info().Ctx(ctx).Ctx(nil).Msg("last nil") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
	log.Info().Ctx(nil).Ctx(ctx).Msg("last valid")
	event := log.Info()
	event.Ctx(nil)
	event.Msg("separated last nil") // want `zerolog output's final Ctx\(\) argument is nil before Msg\(\)`
	event.Ctx(ctx)
	event.Msg("separated last valid")
}

func duplicateAssignment(ctx context.Context) {
	plain := zerolog.New(io.Discard)
	contextual := plain.With().Ctx(ctx).Logger()
	var logger zerolog.Logger
	logger, logger = contextual, plain
	logger.Info().Msg("final plain") // want `zerolog output is not proven to carry context before Msg\(\)`
	logger, logger = plain, contextual
	logger.Info().Msg("final contextual")
}

func conditional(ctx context.Context, cond bool) {
	logger := zerolog.New(io.Discard)
	if cond {
		logger = logger.With().Ctx(ctx).Logger()
	}
	logger.Info().Msg("not safe on every path") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func branchStartsWithoutTrackedValues(cond bool) {
	if cond {
		log.Info().Msg("reachable sink") // want `zerolog output is not proven to carry context before Msg\(\)`
	}
}

func loopJoin(ctx context.Context, count int) {
	logger := zerolog.New(io.Discard)
	for range count {
		logger = logger.With().Ctx(ctx).Logger()
	}
	logger.Info().Msg("loop may not execute") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func eventAliases(ctx context.Context) {
	event := log.Info()
	alias := event
	alias.Ctx(ctx)
	event.Msg("alias mutation is visible")

	plain := log.Info()
	(*zerolog.Event).Msg(plain, "method expression") // want `zerolog output is not proven to carry context before Msg\(\)`
	(*zerolog.Event).Msg(event, "safe method expression")
	emit := plain.Msg
	emit("method value") // want `zerolog output is not proven to carry context before Msg\(\)`
	safeEmit := event.Msg
	safeEmit("safe method value")
	var unknownInterface interface{ Msg(string) } = plain
	unknownInterface.Msg("interface sink") // want `zerolog output is not proven to carry context before Msg\(\)`
	var safeInterface interface{ Msg(string) } = event
	safeInterface.Msg("safe interface sink")
}

func aliasType() {
	var event *Event = log.Info()
	event.Msg("alias type") // want `zerolog output is not proven to carry context before Msg\(\)`
}

type wrapper struct{ *zerolog.Event }

func promoted(event *zerolog.Event) {
	w := wrapper{Event: event}
	w.Msg("promoted unknown") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func promotedSafe(ctx context.Context) {
	w := wrapper{Event: log.Info().Ctx(ctx)}
	w.Msg("promoted safe")
}

type holder struct {
	event  *zerolog.Event
	logger zerolog.Logger
}

func independentInstances(ctx context.Context) {
	safe := holder{
		event:  log.Info().Ctx(ctx),
		logger: zerolog.New(io.Discard).With().Ctx(ctx).Logger(),
	}
	unsafe := holder{event: log.Info(), logger: zerolog.New(io.Discard)}
	safe.event.Msg("safe event field")
	safe.logger.Info().Msg("safe logger field")
	unsafe.event.Msg("unsafe event field")          // want `zerolog output is not proven to carry context before Msg\(\)`
	unsafe.logger.Info().Msg("unsafe logger field") // want `zerolog output is not proven to carry context before Msg\(\)`
}

var mutableGlobal = zerolog.New(io.Discard)
var ExportedGlobal = zerolog.New(io.Discard).With().Ctx(context.Background()).Logger()

type Service struct {
	Logger zerolog.Logger
}

func mutableStorage(service *Service) {
	mutableGlobal.Info().Msg("mutable global")           // want `zerolog output is not proven to carry context before Msg\(\)`
	ExportedGlobal.Info().Msg("exported mutable global") // want `zerolog output is not proven to carry context before Msg\(\)`
	service.Logger.Info().Msg("exported receiver field") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func attachLocal(event *zerolog.Event, ctx context.Context) {
	event.Ctx(ctx)
}

func attachConditionally(event *zerolog.Event, ctx context.Context, cond bool) {
	if cond {
		event.Ctx(ctx)
	}
}

func helperMutation(ctx context.Context) {
	event := log.Info()
	attachLocal(event, ctx)
	event.Msg("safe local summary effect")

	closureAttach := func(target *zerolog.Event) { target.Ctx(ctx) }
	closureEvent := log.Info()
	closureAttach(closureEvent)
	closureEvent.Msg("safe closure effect")

	conditionalEvent := log.Info()
	attachConditionally(conditionalEvent, ctx, false)
	conditionalEvent.Msg("conditional effect remains unknown") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func loggerFromClosure(ctx context.Context) zerolog.Logger {
	build := func() zerolog.Logger {
		return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	return build()
}

func chooseLogger(ctx context.Context, cond bool) zerolog.Logger {
	if cond {
		return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	return zerolog.New(io.Discard)
}

func recursiveLogger(ctx context.Context, depth int) zerolog.Logger {
	if depth <= 0 {
		return zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	}
	return recursiveLogger(ctx, depth-1)
}

func helperReturns(ctx context.Context, cond bool) {
	safe := loggerFromClosure(ctx)
	safe.Info().Msg("safe closure return")
	unknown := chooseLogger(ctx, cond)
	unknown.Info().Msg("unknown join return") // want `zerolog output is not proven to carry context before Msg\(\)`
	recursive := recursiveLogger(ctx, 3)
	recursive.Info().Msg("safe recursive summary")
}

func updateContext(ctx context.Context) {
	safe := zerolog.New(io.Discard)
	safe.UpdateContext(func(builder zerolog.Context) zerolog.Context {
		return builder.Ctx(ctx)
	})
	safe.Info().Msg("safe callback summary")

	unknown := zerolog.New(io.Discard)
	callback := func(builder zerolog.Context) zerolog.Context { return builder }
	if ctx == nil {
		callback = nil
	}
	unknown.UpdateContext(callback)
	unknown.Info().Msg("unknown callback") // want `zerolog output is not proven to carry context before Msg\(\)`
}

func directOutputs(ctx context.Context) {
	plain := zerolog.New(io.Discard)
	plain.Print("print")       // want `zerolog output is not proven to carry context before Print\(\)`
	plain.Printf("%s", "x")    // want `zerolog output is not proven to carry context before Printf\(\)`
	plain.Println("println")   // want `zerolog output is not proven to carry context before Println\(\)`
	_, _ = plain.Write(nil)    // want `zerolog output is not proven to carry context before Write\(\)`
	log.Print("global")        // want `zerolog output is not proven to carry context before Print\(\)`
	log.Printf("%s", "global") // want `zerolog output is not proven to carry context before Printf\(\)`

	contextual := plain.With().Ctx(ctx).Logger()
	contextual.Print("safe")
	var plainWriter io.Writer = plain
	_, _ = plainWriter.Write(nil) // want `zerolog output is not proven to carry context before Write\(\)`
	var safeWriter io.Writer = contextual
	_, _ = safeWriter.Write(nil)
}

func completeEventSinkSet(ctx context.Context) {
	plain := log.Info()
	plain.Msgf("%s", "msgf")                      // want `zerolog output is not proven to carry context before Msgf\(\)`
	plain.MsgFunc(func() string { return "msg" }) // want `zerolog output is not proven to carry context before MsgFunc\(\)`
	plain.Send()                                  // want `zerolog output is not proven to carry context before Send\(\)`
	defer plain.Msg("deferred")                   // want `zerolog output is not proven to carry context before Msg\(\)`
	go plain.Msg("concurrent")                    // want `zerolog output is not proven to carry context before Msg\(\)`
	log.Info().Ctx(ctx).Msgf("%s", "safe")
	log.Info().Ctx(ctx).MsgFunc(func() string { return "safe" })
	log.Info().Ctx(ctx).Send()
	log.Info().Ctx(ctx).Str("key", "value").Msg("safe event derivation")

	contextual := zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	derived := contextual.Output(io.Discard).
		Level(zerolog.InfoLevel).
		Sample(nil).
		Hook(zerolog.HookFunc(func(*zerolog.Event, zerolog.Level, string) {}))
	derived.Info().Msg("safe logger derivations")
}

func suppressed() {
	log.Info().Msg("suppressed")   //nolint:zerologctx // intentional
	log.Print("suppressed direct") //nolint:zerologctx
	event := log.Info()
	(*zerolog.Event).Msg(event, "suppressed expression") //nolint:zerologctx
	emit := event.Msg
	emit("suppressed value") //nolint:zerologctx
	logger := zerolog.New(io.Discard)
	logger.Print("suppressed logger") //nolint:zerologctx
	var writer io.Writer = logger
	_, _ = writer.Write(nil) //nolint:zerologctx
}
