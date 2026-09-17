package pluginfixture

import "github.com/rs/zerolog/log"

func outputWithoutContext() {
	log.Info().Msg("plugin integration fixture")
}
