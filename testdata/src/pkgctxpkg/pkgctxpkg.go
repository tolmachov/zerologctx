// Package pkgctxpkg pins package-level context candidates: they are usable
// regardless of declaration order, unlike locals which must precede the call.
// A dedicated package keeps the package-level candidate from becoming the
// fallback for fixpkg's deliberately candidate-free fixtures.
package pkgctxpkg

import (
	"context"

	"github.com/rs/zerolog/log"
)

// declaredLater uses a context declared below it; the fix must still name it.
func declaredLater() {
	log.Info().Msg("fix must insert pkgCtx") // want "zerolog event missing .Ctx\\(ctx\\) before Msg\\(\\) - context should be included for proper log correlation"
}

var pkgCtx = context.Background()
