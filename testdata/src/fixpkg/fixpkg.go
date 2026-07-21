// Package fixpkg pins the suggested-fix machinery end-to-end: which context
// variable findCtxInScope selects and where the TextEdit inserts the Ctx
// call. fixpkg.go.golden holds the expected post-fix source.
package fixpkg

import (
	"context"

	"github.com/rs/zerolog/log"
)

// preferCtxName: a variable literally named ctx wins over other candidates.
func preferCtxName(ctx, reqCtx context.Context) {
	_ = reqCtx
	log.Info().Msg("fix must insert ctx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// skipUninitializedVar: `var c context.Context` (nil) is the nearest
// preceding candidate but must be skipped in favour of reqCtx.
func skipUninitializedVar() {
	reqCtx := context.Background()
	_ = reqCtx
	var c context.Context
	_ = c
	log.Info().Msg("fix must insert reqCtx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

// nearestPreceding: the innermost scope's nearest preceding candidate wins
// over an outer-scope parameter.
func nearestPreceding(outer context.Context) {
	_ = outer
	inner := context.Background()
	_ = inner
	log.Info().Msg("fix must insert inner") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}
