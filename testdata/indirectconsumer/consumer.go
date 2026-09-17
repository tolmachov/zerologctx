// Package indirectconsumer never imports zerolog, yet it manipulates a zerolog
// value obtained from another package. The analyzer decides whether to look at
// a package by the types it handles, not by what it imports, so this package
// must still be analysed.
package indirectconsumer

import "github.com/tolmachov/zerologctx/testdata/summaryprovider"

func emit() {
	logger := summaryprovider.PlainLogger()
	logger.Info().Msg("a zerolog value reached this package without an import") // want `zerolog output is not proven to carry context before Msg\(\)`
}
