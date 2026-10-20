package zerologctx

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

// BenchmarkFrameJoin exercises every map a frame carries, including the
// location sets inside the joined values: a frame holding only bare SSA values
// skips most of joinValue.
func BenchmarkFrameJoin(b *testing.B) {
	left, right := newFrame(), newFrame()
	for range 1024 {
		alloc := &ssa.Alloc{}
		memory := memoryLocation{root: alloc}
		event := eventLocation{value: alloc}
		left.values[alloc] = abstractValue{
			kind: kindEvent, state: stateHasContext,
			locs: map[eventLocation]struct{}{event: {}}, memLocs: map[memoryLocation]struct{}{memory: {}},
		}
		right.values[alloc] = abstractValue{
			kind: kindEvent, state: stateNoContext,
			locs: map[eventLocation]struct{}{{value: alloc, index: 1}: {}},
		}
		left.memory[memory] = abstractValue{kind: kindLogger, state: stateHasContext}
		right.memory[memory] = abstractValue{kind: kindLogger, state: stateNoContext}
		left.events[event] = eventState{state: stateHasContext, nilCtx: true}
		right.events[event] = eventState{state: stateNoContext}
		right.writes[&ssa.Parameter{}] = true
	}
	joined := left.clone()
	b.ReportAllocs()
	for b.Loop() {
		joinFrame(joined, right)
	}
}

func BenchmarkSSAEngineLargeAliasCFG(b *testing.B) {
	dataflow, fn := strictpkgFunction(b, "largeAliasCFG")
	b.ReportAllocs()
	for b.Loop() {
		dataflow.solve(fn)
	}
}

// BenchmarkLaterSinksLargeCFG measures the judgement of deferred and
// concurrent sinks, which reads every state after their statement.
func BenchmarkLaterSinksLargeCFG(b *testing.B) {
	dataflow, fn := strictpkgFunction(b, "largeDeferCFG")
	_, frames := dataflow.solve(fn)
	b.ReportAllocs()
	for b.Loop() {
		dataflow.collectFindings(fn, frames)
	}
}

// BenchmarkEventHeldAliases exercises stale register aliases next to unrelated
// defers: the later-call scan should be linear, not repeated for every alias.
func BenchmarkEventHeldAliases(b *testing.B) {
	dataflow, fn := strictpkgFunction(b, "eventHeldAliases")
	_, frames := dataflow.solve(fn)
	var site *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok {
				if callee := call.Common().StaticCallee(); callee != nil && callee.Name() == "Info" {
					site = call
				}
			}
		}
	}
	if site == nil {
		b.Fatal("event creation site missing")
	}
	current := frames.out[site.Block()]
	location := eventLocation{value: site}
	if dataflow.eventHeld(current, location) {
		b.Fatal("unrelated defers retained a stale event")
	}
	b.ReportAllocs()
	for b.Loop() {
		dataflow.eventHeld(current, location)
	}
}

// strictpkgFunction loads the strictpkg fixture and returns an engine over the
// named function alone. The analyzer builds SSA without InstantiateGenerics,
// so the benchmarks measure the SSA shape it actually sees.
func strictpkgFunction(b *testing.B, name string) (*engine, *ssa.Function) {
	b.Helper()
	fn := fixtureFunction(b, name)
	pass := &analysis.Pass{
		// No other package was analysed, so none has exported a fact.
		ImportObjectFact: func(types.Object, analysis.Fact) bool { return false },
	}
	dataflow := newEngine(pass, []*ssa.Function{fn})
	dataflow.solveSummaries()
	return dataflow, fn
}
