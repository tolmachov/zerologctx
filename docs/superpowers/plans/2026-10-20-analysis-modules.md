# Analysis modules implementation plan

> **For agentic workers:** Use superpowers:executing-plans, sequentially.

**Goal:** Concentrate reachable-state, call-shape and summary invariants without changing analyzer behavior.

**Architecture:** Private concrete modules in the current package. State owns reads,
writes and escape; calls own receiver/binding conventions; summaries own slot algebra.

**Tech Stack:** Go 1.26+, go/analysis, x/tools SSA, real zerolog fixtures.

**Spec:** `docs/superpowers/specs/2026-10-20-analysis-modules-design.md`

## Global constraints

- Preserve diagnostics, suggested edits, fact fields and formatting, lazy SSA and fail-closed semantics.
- Keep four-route escape closure, immutable shared sets, strong-write certainty and write/widen distinctions.
- Use accumulator joins without iteration caps; no dependencies or public interfaces added.
- Work in the existing worktree/branch. No push, merge, release or branch rename.
- Execute inline as already described to and authorized by the user; no additional approval checkpoint.

## Review focus

- Cyclic aggregate pointers must terminate and invalidate every reachable event.
- Escaped slots storing new aliases must retain escape across reproof and later calls.
- Bound method callbacks must align their event parameter after the receiver.
- Opaque invokes must invalidate dispatch inputs without acquiring a static summary.
- Conflicting returned-parameter identities must propagate escapes through summary joins.

### Task 1: Reachable state

**Files:** `state.go`, `dataflow.go`, `state_test.go`.
**Interface:** frame semantic operations for register lookup/remembering, `store`,
`observe`, parameter initialization/observation, event creation and postconditions;
retain `invalidateEscape`, clone/reset/join/equality behavior. Engine supplies event
liveness and uses state operations rather than coordinating writes and raw maps.

- [x] Add state-interface tests for nested reachable state, cycles, alias stores,
  persistent escape and actual parameter-write accounting; run before implementation.
- [x] Move state implementation and enforce store/postcondition ordering inside it.
- [x] Redirect transfer, callback and summary clients to semantic state operations.
- [x] Run focused state tests and `go test ./...`; preserve all fixture expectations.

### Task 2: Call interpretation

**Files:** `calls.go`, `calls_test.go`, `dataflow.go`.
**Interface:** `resolveCall(*ssa.CallCommon) callSite`; the concrete call description
owns identity, inputs, receiver and callback argument alignment. No opaque call gains
a statically resolved body. State operations from Task 1 apply its reachable inputs.

- [x] Add real-SSA tests for static methods, method expressions/values, invokes,
  dynamic calls and callback binding, including receiver escape; run before implementation.
- [x] Replace the tuple resolver with the call description and shared input traversal.
- [x] Centralize bound callback positions, retaining SCC and later-call behavior.
- [x] Run call tests and `go test ./...`.

### Task 3: Summary algebra

**Files:** `summary.go`, `summary_test.go`, `zerologctx.go`, `dataflow.go`.
**Interface:** canonical `functionSummary` owns result/parameter semantic slots,
construction, accumulation, conservative normalization and proof eligibility.
State owns postcondition application; call interpretation supplies argument alignment.

- [x] Add join tests for conflicting identities/escape propagation, preservation,
  unknown/missing imported slots and accumulation laws; run before implementation.
- [x] Concentrate summary representation and algebra; migrate consumers from raw vectors.
- [x] Run summary tests, cross-package fixtures and `go test ./...`.

### Task 4: Integration and verification

**Files:** final ownership map in `AGENTS.md`, approved design status; verification artifacts ignored.

- [x] Inspect final clients for leaked ordering and obsolete helpers; update orientation.
- [x] Run CONTRIBUTING's complete Required verification block and compare benchmarks
  with `.context/architecture-baseline.txt`; investigate measured regressions.
- [x] Obtain a fresh whole-change review and address concrete findings; rerun affected checks.
- [x] Report changes and verification without publishing or committing unrelated work.

## Execution result

Implemented sequentially and reviewed independently. The review's solver/state
liveness ownership finding was corrected and re-reviewed without blockers.
Existing diagnostics, facts and golden edits remain unchanged.

Verification passed: formatting, both module checks, build, vet, staticcheck
0.8.1, govulncheck 1.8.0, real zerolog oracle, race coverage, custom plugin and
benchmarks. Race tests ran on Go 1.26.1 and local Go 1.27.3; coverage is 96.8%.
Staticcheck used the cached Go 1.26.1 toolchain because its export reader cannot
consume the local Go 1.27.3 format. The release-only Go 1.27.1 matrix was not run.
Short benchmark comparisons show no material regression and unchanged allocation
counts for large alias CFG solving and later-sink observation.

The subsequent simplify review removed repeated later-call operand scans and
duplicated initialization and fixture setup. A targeted benchmark improved from
2709 ns/op and 216 allocations to 585.8 ns/op and 3 allocations, with a direct
regression for retention through a deferred register.
