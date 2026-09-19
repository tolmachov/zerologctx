# AGENTS.md

Orientation for coding agents. The authoritative documents are
[README.md](README.md) (user contract), [CONTRIBUTING.md](CONTRIBUTING.md)
(verification and design constraints) and [RELEASING.md](RELEASING.md). This
file points into them; when they disagree, they win.

## What this is

`zerologctx` is a strict, fail-closed `go/analysis` analyzer for
`github.com/rs/zerolog`. It follows zerolog values through SSA dataflow and
reports every output operation not proven to carry a `context.Context`.
Silence has exactly three causes: the context was proven, the sink was
suppressed with `//nolint`, or the sink is unreachable. Unknown provenance is a
diagnostic, never a pass.

## Layout

- `zerologctx.go` — `Analyzer`, `run`, the state lattice and exported summary
  facts. SSA is built per package in `buildPackageSSA` (on top of `ctrlflow`,
  deliberately not `buildssa`) and skipped entirely when
  `usesZerologValues` finds no zerolog-typed value.
- `dataflow.go` — abstract values, frames, the engine, per-SCC summary solving,
  and `invalidateEscape`.
- `recognition.go` — zerolog sink identification via `go/types`
  (`eventSinks`, `loggerSinks`).
- `reporting.go` — diagnostics, `//nolint` handling, and the `.Ctx(expr)`
  suggested fix.
- `cmd/zerologctx/` — `singlechecker` CLI.
- `plugin/` — golangci-lint v2 module plugin.
- `testdata/` — an independent Go module using the real zerolog v1.35.1:
  `strictpkg`, `summaryprovider`/`summaryconsumer`, `indirectconsumer`,
  `fixpkg` (+ `.golden`), `pluginfixture`.
- `scripts/verify-plugin.sh` — builds and runs a custom golangci-lint binary.

## Commands

Fast loop:

```bash
go test ./...
go test -run TestAnalyzer .
```

`testdata/` has its own `go.mod`: fixture dependency changes need
`(cd testdata && go mod tidy)`, and root `go mod tidy` does not touch it.

Before declaring work done, run the full *Required verification* block in
CONTRIBUTING.md. Total coverage must stay at or above 90 percent; CI runs on
Go 1.26.1 and 1.27.1.

## Hard rules

Summarised from *Design constraints* in CONTRIBUTING.md — read that section
before touching `dataflow.go`.

- Uncertainty stays a diagnostic. No "probably safe" heuristics.
- Identify zerolog through canonical package paths and `go/types` objects after
  unaliasing, never by printed type strings.
- `invalidateEscape` is the single invalidation path and must follow all four
  routes: syntactic address, `locs`, `memLocs`, `elems`.
- Postconditions are written only into a location proven to be the one the call
  touched; may-alias sets are widened, never updated.
- The summary fixpoint joins into the accumulator; do not replace the join with
  assignment or add iteration caps.
- Globals and receiver fields are opaque mutable storage; no package-wide
  positive-assignment shortcuts.
- Keep reporting and suggested-fix policy out of SSA transfer logic.
- No configuration surface and no compatibility modes.
- Do not reintroduce a local zerolog stub in `testdata/`.

## Tests

- Fixtures express expectations with `// want` comments and run through
  `analysistest`.
- Add a regression at the abstraction boundary that failed, not only at the
  downstream symptom.
- Suggested fixes are golden-tested (`fixpkg/fix.go.golden`) and the fixed
  source must compile (`TestSuggestedFixesCompile`). Unsafe call forms must stay
  unchanged in the golden output.

## Changes and releases

- User-visible behavior changes go into `[Unreleased]` in CHANGELOG.md.
- Never tag, push, publish a GitHub Release, or close issues from an
  implementation branch; see RELEASING.md.
