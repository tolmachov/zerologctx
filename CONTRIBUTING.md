# Contributing

`zerologctx` is a strict analyzer. A change is correct only when every reported
safe result is proven for all reachable paths; uncertainty must remain a
diagnostic.

## Prerequisites

- Go 1.26 or newer; CI builds on 1.26.1 and 1.27.1;
- `staticcheck` v0.8.1;
- `govulncheck` v1.8.0;
- `golangci-lint` v2.13.2 when changing plugin integration.

Install pinned tools:

```bash
go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
```

## Required verification

```bash
test -z "$(gofmt -s -l .)"
go mod tidy -diff
go mod verify
(cd testdata && go mod tidy -diff && go mod verify)
go build ./...
go vet ./...
staticcheck ./...
govulncheck ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
./scripts/verify-plugin.sh
go test -run '^$' -bench . -benchtime 1x ./...
```

Total statement coverage must remain at least 90 percent. Run the test and race
suites on both supported Go patch versions before release.

## Test layout

Fixtures live in the independent module under `testdata/` and use the real,
pinned `github.com/rs/zerolog v1.35.1`. Do not reintroduce a local zerolog stub.

- `strictpkg` covers control flow, aliases, memory, sink forms, summaries, and
  suppressions;
- `summaryprovider` and `summaryconsumer` cover exported analysis facts;
- `indirectconsumer` handles a zerolog value without importing zerolog and must
  still be analysed;
- `fixpkg` verifies suggested edits and recompiles the fully fixed source;
- `pluginfixture` is executed by a real custom golangci-lint binary.

Add regressions at the abstraction boundary that failed, not merely at a
downstream symptom. Suggested fixes must be golden-tested and type-checked.
Unsafe call forms must explicitly remain unchanged in the golden output.

## Design constraints

- Zerolog identity comes from canonical package paths and `go/types` objects,
  after alias removal; do not match printed type strings.
- The dataflow lattice is `unreachable`, `has-context`, `no-context`, and
  `unknown`; joins are safe only when every reachable predecessor is safe.
- Function summaries are solved per strongly connected component and may
  export only proven postconditions.
- Globals and receiver fields are opaque mutable storage. Do not add
  package-wide positive-assignment shortcuts.
- `invalidateEscape` is the single invalidation path, and it must stay closed
  over all four ways an abstract value reaches state: a syntactic address, the
  event identities in `locs`, the memory locations in `memLocs`, and everything
  nested in `elems`. Following fewer of them preserves a proof the escape
  destroyed.
- A postcondition may only be written into a location proven to be the one the
  call touched — a syntactic address, or a complete singleton `locs` set. A
  may-alias set is widened, never updated.
- The summary fixpoint terminates because the accumulator ascends, not because
  it is capped. Every round joins into what is already known, so a slot moves
  at most twice. Do not replace that join with an assignment.
- Keep reporting and suggested-fix policy separate from SSA transfer logic.
- There is no compatibility mode or configuration surface.

Update [CHANGELOG.md](CHANGELOG.md) for user-visible behavior changes. Before a
release, follow [RELEASING.md](RELEASING.md).
