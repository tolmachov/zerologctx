package wrapperconsumer

import (
	"context"

	"wrappkg"
)

func correct() {
	ctx := context.Background()
	wrappkg.Info().Ctx(ctx).Msg("correct")
}

func incorrect(ctx context.Context) {
	wrappkg.Info().Msg("missing context through wrapper") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}
