# Changelog

All notable changes are documented here. The project follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning](https://semver.org/).

## [Unreleased]

First public release. Nothing has shipped before it, so everything below is new.

### Added

- `zerologctx.Analyzer`, the `zerologctx` command, and a golangci-lint v2
  module plugin. Every statically identifiable zerolog output operation must be
  proven to carry a `context.Context`; missing and unknown provenance are both
  reported, whether or not a context is in scope.
- Checked sinks: `(*zerolog.Event).Msg`, `Msgf`, `MsgFunc` and `Send`;
  `zerolog.Logger` `Print`, `Printf`, `Println` and `Write`; package-level
  `log.Print` and `log.Printf`.
- Provenance through SSA control flow, aliases, local memory, stores and loads,
  `Phi` nodes, reassignment, promoted methods, method expressions, method
  values and statically resolved interface dispatch. At a join, context is
  proven only when every reachable path proves it.
- Function summaries carrying proven result and pointer-mutation
  postconditions, solved per strongly connected component and exported as
  analysis facts across package boundaries. A function with nothing proven
  exports no fact, and a missing fact reads as unknown.
- Order-sensitive `Ctx`: the last call wins, and a final `Ctx(nil)` has its own
  diagnostic.
- Suggested fixes that insert `.Ctx(expr)` before an ordinary Event selector
  call. The expression comes from the nearest enclosing scope offering a usable
  candidate, preferring one named `ctx`, and is never shadowed, declared later,
  wrongly typed or provably nil. Method expressions, method values, promoted
  receivers and the direct Print/Write APIs are reported without an edit.
- Suppression with `//nolint:zerologctx`, `//nolint:all` or bare `//nolint`,
  either on a line the call spans or on a line of its own directly above it.
- Fail-closed treatment of everything the analyzer cannot follow: values
  captured by closures, escaping method values, writes through unresolved
  addresses, stores into slices, maps, channels and globals, aggregates passed
  by value, mutable globals, receiver fields and opaque calls all stay unknown
  and are reported.
- Goroutines establish nothing where they are spawned, and a sink a goroutine
  carries is judged against the state at function exit, so a later mutation
  cannot be missed.
- Postconditions are applied only to a location proven to be the one the call
  touched; a may-alias set is widened instead.

### Notes for tooling authors

- The analyzer has no settings. A plugin `settings` block is rejected rather
  than ignored.
- It requires only `buildssa`, and needs `LoadModeTypesInfo`.
- `context.Context` need not be reachable from a package's import graph. When
  it is absent, diagnostics are still emitted; only suggested fixes are
  withheld.

[Unreleased]: https://github.com/tolmachov/zerologctx/compare/main...HEAD
