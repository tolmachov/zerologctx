# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Bug fixes

- **Critical:** `loggersWithContext`/`eventsWithContext` are now keyed by
  `*types.Object` instead of by identifier name. Previously, a single
  `logger := log.With().Ctx(ctx).Logger()` anywhere in a package would
  silently mask every other uncontextualised `logger.Info()...` call across
  all files and functions. New regression fixture in
  `testdata/.../edge_cases.go` (`TestCrossFunctionNameCollision` /
  `crossCollisionVictim`).
- `isContextType` now uses `types.Implements` against the canonical
  `context.Context` interface (located via a recursive walk of the package's
  imports) instead of substring matching on the type's printed form and
  method-name-only checks. This fixes false positives where unrelated types
  whose printed form contains `context.Context` (e.g. `map[string]context.Context`)
  were accepted, and false negatives where types with the right method names
  but wrong signatures were also accepted.
- Three sites that detected `*zerolog.Event` via
  `strings.Contains(typeString, "zerolog.Event")` now use a typed
  `*types.Named` package-path check, eliminating accidental matches against
  `zerolog.EventMarshaler`, `Eventual`, and look-alike forks.
- nolint directives placed at the end of multi-line fluent chains
  (`log.Info().\n\tStr("k","v").\n\tMsg("hi") //nolint:zerologctx`) are now
  honoured. Previously the line check used the start of the chain, not the
  terminal-method line.
- The nolint parser now accepts bare `//nolint`, `//nolint:all`, and the
  trailing-reason form `//nolint:zerologctx // because X`, mirroring
  golangci-lint semantics.
- Package-level loggers declared via `var` are now tracked
  (`*ast.ValueSpec` was missing from the inspector node filter), fixing
  false positives on `var globalLoggerWithContext = log.With().Ctx(...).Logger()`.
- Reassigning a tracked logger to a non-context-bearing value now correctly
  clears the prior context fact (previously the map was append-only).

### Features

- Diagnostics now include an `analysis.SuggestedFix` that inserts
  `.Ctx(ctx)` before the terminal method when an in-scope variable
  satisfying `context.Context` is available.
- Added `Print` and `WithLevel` to the recognised log-level methods so that
  `loggerWithCtx.Print().Msg(...)` and similar do not produce false positives.
- The analyzer now short-circuits packages that do not transitively import
  `github.com/rs/zerolog`, eliminating per-package overhead in monorepos.

### Internal cleanup

- Per-pass state (`loggersWithCtx`, `eventsWithCtx`, `contextIface`,
  `fileMap`) consolidated into a single `state` struct; helpers became
  methods.
- `hasCtxInChain` and `hasCtxInContextChain` unified into a single
  `hasCtxCallInChain` parameterised by whether to enforce a `*zerolog.Event`
  receiver.
- Dead `ctxChainCache` removed (recursive calls always bypassed it; the
  cache never hit in practice).
- `hasNoLintDirective` file lookup now uses a precomputed
  `map[*token.File]*ast.File` (was O(N) per diagnostic).
- `logLevelMethods` hoisted to a package-level var (was reallocated on every
  invocation of `isEventFromLoggerWithContext`).
- testdata `zerolog` stub: `Event.Ctx`/`Context.Ctx` now take `context.Context`
  instead of `interface{}`, matching the real library and exposing the
  type-checking branches to realistic inputs.
- `cmd/zerologctx/main_test.go` no longer references `_ = main` as a smoke
  test; it builds the binary in a tempdir and runs it with `-h` to actually
  exercise the CLI entry point.
- `plugin/plugin_test.go` now runs the analyzer returned from `GetAnalyzers`
  through `analysistest` end-to-end (not just identity checks).
- `BenchmarkImplementsContextInterface` (previously `b.Skip`'d) replaced
  with a working `BenchmarkIsContextType` that builds a synthetic
  `*types.Named` to exercise `types.Implements`.

## [1.0.0] - Initial Release

### Features
- Static analysis linter for zerolog that ensures events include context via Ctx()
- Support for all terminal methods: Msg(), Msgf(), MsgFunc(), and Send()
- Custom context type detection (types embedding context.Context)
- Logger with embedded context tracking
- Event variable context tracking through assignments
- Type-safe context validation using method set introspection
- Recursive chain analysis for fluent interfaces with memoization for performance
- nolint directive support with multiple formats
- golangci-lint plugin integration
- Standalone CLI tool
- Comprehensive test suite with 92%+ coverage
- Performance benchmarks
- Enhanced CI pipeline with go vet, staticcheck, and coverage reporting

### Technical Details
- Go version requirement: 1.26.0 (toolchain 1.26.1)
- Zero external dependencies (except golang.org/x/tools)
- Optimized performance with AST caching
- Handles edge cases: struct fields, function returns, custom types, global loggers
- Support for multiple integration patterns: standalone, golangci-lint, library import