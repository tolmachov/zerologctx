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
- Checked sinks: Event methods `Msg`, `Msgf`, `MsgFunc` and `Send`; Logger
  methods `Print`, `Printf`, `Println` and `Write`; package-level `log.Print`
  and `log.Printf`, including in a package that holds no zerolog value of its
  own.
- A model of zerolog v1.35.1 checked against the library itself: `Output`
  drops the logger's context, `UpdateContext` neither attaches nor removes one,
  `CreateDict` returns a new event seeded from its parent, and `Func`, `Object`
  and `EmbedObject` hand the event to user code whose summary decides the
  result.
- Provenance through SSA control flow, aliases, local memory, stores and loads,
  `Phi` nodes, reassignment, promoted methods, method expressions, method
  values and statically resolved interface dispatch. At a join, context is
  proven only when every reachable path proves it.
- Function summaries carrying proven result and pointer-mutation
  postconditions, which parameter each result returns, and whether each
  pointer parameter escapes, solved per
  strongly connected component and exported as analysis facts across package
  boundaries. A function with nothing proven exports no fact, and a missing
  fact reads as unknown. A path that resumes after a recovered panic
  contributes to the summary.
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
  by value, mutable globals, receiver fields, opaque calls, and `Event.Func`,
  `Object` and `EmbedObject` callbacks without a provable summary all stay
  unknown and are reported.
- Escape tracking: once a variable's or an event's address has gone somewhere
  the analyzer cannot follow, a proof about it lasts only until the next call
  or the next write through an untracked pointer. Parameters may alias each
  other and globals, so a function's own write through another parameter, a
  global or an untracked pointer widens what its parameters denote. A pointer
  is read through at every use, so a later store to its target is never
  missed.
- A call site in a loop that creates an event while an earlier iteration's
  event is still held gives neither a proof meant for the other.
- Package-level `log.Print` and `log.Printf` take no context and are always
  reported.
- Goroutines establish nothing where they are spawned. A sink a goroutine or a
  deferred call carries is judged against every state from its statement
  through function exit, with the function's other deferred and goroutine
  calls applied:
  the goroutine may run at any of them, and the deferred call runs on a panic
  at any of them, so a context attached later proves nothing.
- A local struct escaping behind an interface or through a channel takes the
  zerolog values it holds with it; a call through a zerolog interface runs
  user code and is opaque.
- Package-level variable initializers, including the closures they call, are
  analysed like any function.
- Postconditions are applied only to a location proven to be the one the call
  touched; a may-alias set is widened instead.

### Notes for tooling authors

- The analyzer has no settings. A plugin `settings` block is rejected rather
  than ignored.
- It requires only `ctrlflow`, and needs `LoadModeTypesInfo`. SSA is built
  per package by the analyzer itself, and only for packages that refer to
  zerolog or handle a zerolog value.
- `context.Context` need not be reachable from a package's import graph. When
  it is absent, diagnostics are still emitted; only suggested fixes are
  withheld.

[Unreleased]: https://github.com/tolmachov/zerologctx/commits/main
