// Package deepchainpkg pins fixpoint convergence for a dependency chain that
// runs against the traversal order: this file sorts before zz_decl.go, so
// every link of the alias chain below is seen before the declaration it
// depends on. Each collection pass resolves exactly one link, so the chain is
// deeper than any fixed pass budget would allow — the loop must be bounded by
// the fact lattice itself, not by a constant.
package deepchainpkg

func useDeepAliasChain() {
	l0.Info().Msg("context reaches through 14 reverse-ordered alias links")
}
