package deepchainpkg

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// l14 carries the context; l0..l13 are Logger-to-Logger derivations of it,
// declared so that each one depends on the next.
var (
	l0  = l1.Level(zerolog.InfoLevel)
	l1  = l2.Level(zerolog.InfoLevel)
	l2  = l3.Level(zerolog.InfoLevel)
	l3  = l4.Level(zerolog.InfoLevel)
	l4  = l5.Level(zerolog.InfoLevel)
	l5  = l6.Level(zerolog.InfoLevel)
	l6  = l7.Level(zerolog.InfoLevel)
	l7  = l8.Level(zerolog.InfoLevel)
	l8  = l9.Level(zerolog.InfoLevel)
	l9  = l10.Level(zerolog.InfoLevel)
	l10 = l11.Level(zerolog.InfoLevel)
	l11 = l12.Level(zerolog.InfoLevel)
	l12 = l13.Level(zerolog.InfoLevel)
	l13 = l14.Level(zerolog.InfoLevel)
	l14 = log.With().Ctx(context.Background()).Logger()
)
