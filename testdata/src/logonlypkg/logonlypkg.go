// Package logonlypkg imports only github.com/rs/zerolog/log (the sub-package),
// not github.com/rs/zerolog directly. This exercises the strings.HasPrefix
// branch in scanImports that matches zerolog sub-package imports.
package logonlypkg

import (
	"context"

	"github.com/rs/zerolog/log"
)

func correct() {
	ctx := context.Background()
	log.Info().Ctx(ctx).Msg("correct")
}

func incorrect(ctx context.Context) {
	log.Info().Msg("missing context") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}
