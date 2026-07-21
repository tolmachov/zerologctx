// Package testpkg — declares the package-level context-bearing logger used
// by aa_order_use.go, which sorts (and is traversed) earlier.
package testpkg

import (
	"context"

	"github.com/rs/zerolog/log"
)

var pkgOrderLogger = log.With().Ctx(context.Background()).Logger()
