// Package testpkg — this file's name sorts before zz_order_decl.go, so the
// use below is traversed before the package-level declaration it refers to.
// The two-phase (collect facts, then check) traversal must still recognise
// the context-bearing logger.
package testpkg

func reviewUsePkgLevelDeclaredLater() {
	pkgOrderLogger.Info().Msg("pkg-level ctx logger declared in a later file - should NOT trigger")
}

// pkgOrderAlias aliases a package-level logger declared in a later file; its
// fact can only be computed on the second fixpoint pass, pinning the
// collectFacts loop (a single collection pass would leave it untracked).
var pkgOrderAlias = pkgOrderLogger

func reviewFixpointAliasOfAlias() {
	pkgOrderAlias.Info().Msg("alias of later-declared ctx logger - should NOT trigger")
}
