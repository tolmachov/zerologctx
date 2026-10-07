package strictpkg

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// clearExternally is implemented in assembly, so the analyzer has no body to
// read and must prove nothing about it.
func clearExternally(event *zerolog.Event)

func bodylessCalleeProvesNothing(ctx context.Context) {
	event := log.Info().Ctx(ctx)
	clearExternally(event)
	event.Msg("a bodyless callee proves nothing") // want `zerolog output is not proven to carry context before Msg\(\)`
}
