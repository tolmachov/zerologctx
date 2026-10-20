# Deepen reachable state, call interpretation and function summaries

Status: implemented and independently reviewed.

## Intent and acceptance

Take all three opportunities from the architecture review. Increase locality of
soundness rules and leverage of the existing test surface. Keep the analyzer's
observable behavior unchanged: diagnostic messages and positions, suppression,
suggested edits, exported facts, static-analysis limits and no-configuration contract.

Use three private concrete modules inside the existing `zerologctx` Go package.
Their interface is a semantic contract, not a new Go `interface` declaration.
There is one in-process implementation; no new interchangeable adapter, public
package, dependency or compatibility mechanism is needed.

The public `Analyzer` interface remains the main acceptance test surface. Keep
its package-level lazy SSA construction and the separation of reporting policy.
The work must concentrate rules, rather than merely move functions to new files.

## Reachable state module

The frame owns value lookup, effective context observation, local storage,
event mutation, escape persistence and parameter-write accounting. SSA transfer,
call handling and summary application request semantic operations instead of
sequencing raw map edits and invalidation helpers themselves.

Consolidate a local store into one operation that resolves its destination,
invalidates possible aliases, marks stored reachable values when required,
discards stale aggregate fields and records only actual parameter writes.
Consolidate event postcondition handling so callers cannot accidentally apply a
strong write to a may-alias set. Keep explicit parameter initialization and
terminal parameter observation inside the state module.

Keep the single `invalidateEscape` entry point. It and the internal marking and
widening operations retain all four routes: syntactic address, `locs`, `memLocs`
and nested `elems`. Do not force these different operations into a generic walk:
their side effects and termination rules differ. Escaped roots/events grow
monotonically; widening alone is not a parameter write.

The implementation owns raw frame maps. Solver clients retain clone, reset,
join and equality operations. Event liveness in loops remains a solver concern;
it supplies whether an earlier event is held, while state creation owns seeding
versus joining and must-alias eligibility. Address-set caching remains internal
to state lookup, and shared location sets remain immutable.

Use `state.go` for this ownership; `dataflow.go` retains control-flow solving,
replay, scheduling and orchestration. Internal helpers may remain small, but
clients must not recover the old map-and-ordering interface through accessors.

## Call interpretation module

Use a concrete private call description in `calls.go` instead of the current
four-result tuple. It owns the callable identity, statically known body,
receiver/dispatch value and receiver or binding offsets needed to interpret
arguments. Retain one resolution path for ordinary calls, sink observation,
argument escape and deferred/concurrent effects.

Distinguish arguments aligned to a proven callee from the conservative inputs
that code may reach. Static receivers, synthetic method bindings and invoke
receivers must not require position corrections in each consumer. Include
callback parameter binding in this ownership; ordinary closure captures still
escape and do not become summarized parameters.

Do not widen the recognized-call contract: opaque invokes and dynamic functions
stay opaque. Preserve the current SCC dependency discovery, including callback
and marshaler dependencies, and its ordering. This refactor does not attempt
more aggressive dispatch resolution or new summary inference.

Sink classification still uses canonical Go type identity. Reporting, fix
selection and source indexing remain outside call interpretation. Deferred and
goroutine timing, recovery and sink observations keep their current semantics.

## Function summary module

Use `summary.go` to own construction, proof eligibility, equality, accumulator
join, conservative imported-fact normalization and result/parameter slot meaning.
Consumers access semantic slots rather than coordinate parallel vectors and
sentinel interpretation independently. Call argument alignment belongs to call
interpretation; strong versus weak application belongs to reachable state.

Keep `functionSummary` as the canonical analysis fact, with the existing exported
fields and `String` representation. Do not add a second serialized DTO or change
fact shape. Preserve imported-fact repair, including conservative treatment of
missing escape entries and invalid result identities.

Result bottom means no proven result; parameter-effect bottom means preserved;
result-identity bottom means no exit seen. Conflicting returned parameters lose
identity and propagate escapes. SCC rounds keep joining into their accumulator
without caps. Direct zerolog mutation and a callee's postcondition remain
different operations, including treatment of already escaped locations.

## Sequence and verification

1. Reachable state ownership: add meaningful invariant coverage, redirect state
   mutation and store callers, then remove superseded orchestration helpers.
2. Call interpretation: exercise real SSA call forms, migrate ordinary and later
   call consumers, and remove the tuple and scattered binding arithmetic.
3. Summary algebra: test join and import invariants, migrate producers and
   consumers, then remove redundant vector/sentinel bookkeeping.
4. Review the combined change for rules still duplicated across module interfaces;
   update agent orientation to the final layout and run complete verification.

Work sequentially in the current branch/worktree because the three modules share
transfer and call paths. Do not rename the branch, push or release. Separate
implementation commits are optional and not a prerequisite for review.

Preserve all existing analyzer fixtures, cross-package fact checks, real zerolog
oracle tests and golden edits with compilation. Replace representation-coupled
assertions only after their semantic coverage exists at the new module interface.

Add tests for nested aggregates and pointers, cycles, missing memory at joins,
escape followed by reproof and a later call, may-alias writes, parameter aliasing
and actual-write accounting. Cover static methods, expressions, bound values,
ordinary closures, invokes and dynamic calls with real SSA, including later
sinks and callbacks. Cover summary joins, conflicting identities and escapes,
bottom/proof/top transitions and imported malformed facts.

Run focused tests after each stage. At completion run the entire Required
verification block from CONTRIBUTING, including race coverage of at least 90%,
real custom plugin execution and existing benchmarks. Capture frame join, large
alias CFG and later-sink benchmark baselines before refactoring; investigate
apparent regressions with repeated comparable measurements rather than imposing
an invented numerical gate. Run both supported Go versions before release;
this work does not include a release.

If testing exposes an existing correctness defect, report it separately and add
a regression before fixing it; do not silently fold behavior changes into this
refactor. Changelog changes are needed only for user-visible behavior changes.

## Choices made for this design

- User-selected scope: all three architecture candidates.
- Recommended defaults: private concrete modules, unchanged behavior and facts,
  sequential implementation, current branch, no new dependencies.
- Concrete helper names and test helper organization may be chosen during
  implementation; ownership and semantic guarantees above are fixed.
- The small glossary records existing analyzer terminology. No ADR is warranted:
  this preserves the documented analysis model and introduces no irreversible
  architectural decision.
