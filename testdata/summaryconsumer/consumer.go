package summaryconsumer

import (
	"context"
	"io"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tolmachov/zerologctx/testdata/summaryprovider"
)

func consume(ctx context.Context) {
	logger := summaryprovider.ContextLogger(ctx)
	logger.Info().Msg("safe cross-package summary")
	event := log.Info()
	summaryprovider.Attach(event, ctx)
	event.Msg("safe cross-package effect")
	logger = zerolog.New(io.Discard)
	summaryprovider.AttachLogger(&logger, ctx)
	logger.Info().Msg("safe logger pointer effect")
	builder := zerolog.New(io.Discard).With()
	summaryprovider.AttachBuilder(&builder, ctx)
	logger = builder.Logger()
	logger.Info().Msg("safe builder pointer effect")
	logger = zerolog.New(io.Discard)
	logger.UpdateContext(summaryprovider.AttachBackground)
	logger.Info().Msg("UpdateContext never attaches the callback's context") // want `zerolog output is not proven to carry context before Msg\(\)`
	logger = zerolog.New(io.Discard).With().Ctx(ctx).Logger()
	summaryprovider.ResetAny(&logger)
	logger.Info().Msg("boxed pointer reset across package") // want `zerolog output is not proven to carry context before Msg\(\)`

	logger = summaryprovider.Outer(ctx)
	logger.Info().Msg("safe regardless of declaration order")
	logger = summaryprovider.PlainLogger()
	logger.Info().Msg("proven to carry no context") // want `zerolog output is not proven to carry context before Msg\(\)`

	keptAway := zerolog.New(io.Discard)
	kept := summaryprovider.KeepLogger(&keptAway, ctx)
	kept.Info().Msg("safe result of an escaping callee")
	keptAway = keptAway.With().Ctx(ctx).Logger()
	summaryprovider.ResetKept()
	keptAway.Info().Msg("written through the alias the callee kept") // want `zerolog output is not proven to carry context before Msg\(\)`

	log.Info().Ctx(ctx).Func(summaryprovider.Clearer{}.Clear).Msg("the imported bound method cleared it") // want `zerolog output is not proven to carry context before Msg\(\)`
}
