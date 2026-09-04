// Package deepchainpkg pins fixpoint convergence. The alias chain in
// zz_decl.go is declared against its own dependency order — l0 depends on l1,
// which depends on l2, and so on — so each collection pass resolves exactly
// one link and the chain is deeper than any fixed pass budget would allow.
// This file additionally sorts before zz_decl.go, putting the use at an
// earlier position than every declaration it depends on.
//
// The context parameter is load-bearing: without a reachable context the
// diagnostic is gated off and the fixture would pass whether or not the chain
// resolved.
package deepchainpkg

import "context"

func useDeepAliasChain(ctx context.Context) {
	_ = ctx
	l0.Info().Msg("context reaches through 14 reverse-ordered alias links")
}
