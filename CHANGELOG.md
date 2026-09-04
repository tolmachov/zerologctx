# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Bug fixes

- **Suggested fixes could produce code that does not compile.** The fix
  candidate was chosen by object and type, but the emitted text was a bare
  name that was never checked against what that name resolves to at the
  insertion point. Three separate shapes were affected: a candidate shadowed
  at the call site (`.Ctx(ctx)` where `ctx` is a local `string`), a value
  whose context methods use pointer receivers (`.Ctx(v)` instead of
  `.Ctx(&v)`), and a blank receiver field (`.Ctx(r._)`). Candidates are now
  resolved through `types.Scope.LookupParent` at the call position, values
  that satisfy `context.Context` only through their pointer are rendered as
  `&v`, and blank fields are skipped.
- **A context declared without an initializer suppressed the diagnostic
  entirely.** `var ctx context.Context` followed by `ctx = context.Background()`
  was treated as a permanently nil variable, which gated off the report rather
  than just the fix. Only variables that are never assigned anywhere in the
  package now count as nil.
- **A deep dependency chain running against the traversal order failed the
  whole package.** The fact-collection fixpoint was capped at 10 passes and
  returned an error beyond that, so a chain of package-level logger aliases
  declared in reverse order made the analyzer report
  `fact propagation did not converge` instead of any diagnostics. The lattice
  is finite and every write ascends it, so the loop needs no budget; a
  non-monotone rewrite — the only way it could spin — is now detected directly
  in `factTable.set`.
- Reporting is no longer gated on being able to produce a fix. A context that
  is reachable but cannot be named at the call site is reported without a
  `SuggestedFix` rather than being silently skipped.
- `gofmt -s` compliance restored for `testdata/.../regression_review.go`; the
  CI formatting gate was failing on `main`.

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

- `findCtxInScope` split into `reachableCtx` (is a context reachable — the
  report gate) and the `fixExprFor`/`ctxExpr` pair (how to write it — the fix),
  so the two questions can no longer answer each other by accident.
- `isContextType` split into `implementsContext` (exact) and
  `addressableContext` (only `*T` satisfies the interface). `callArgIsContext`
  now uses the exact form.
- New `TestSuggestedFixesCompile` type-checks `fixpkg.go.golden` with
  `go/packages`. Comparing the fixed source against a golden file only proves
  the text is what was expected; it is what let three classes of uncompilable
  fix ship green.
- Removed `BenchmarkAnalyzer` (it timed `go/packages` loading, not the
  analyzer), the tautological `TestPluginAnalyzerEndToEnd` (it re-ran the same
  analyzer pointer `TestGetAnalyzers` had just asserted identity for), the
  unreferenced `testdata/examples.go` duplicate, and `docs/golangci-lint.md`,
  whose golangci-lint configuration was invalid and contradicted README.

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