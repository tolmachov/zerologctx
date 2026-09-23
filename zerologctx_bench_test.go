package zerologctx

import (
	"go/types"
	"os"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
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
		left.events[event] = eventState{state: stateHasContext, ctxWasNil: true}
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

// strictpkgFunction loads the strictpkg fixture and returns an engine over the
// named function alone. The analyzer builds SSA without InstantiateGenerics,
// so the benchmarks measure the SSA shape it actually sees.
func strictpkgFunction(b *testing.B, name string) (*engine, *ssa.Function) {
	b.Helper()
	packagesUnderTest, err := packages.Load(&packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  analysistest.TestData(),
		Env:  append(os.Environ(), "GOWORK=off"),
	}, "./strictpkg")
	if err != nil {
		b.Fatal(err)
	}
	if len(packagesUnderTest) != 1 {
		b.Fatalf("loaded %d packages, want 1", len(packagesUnderTest))
	}
	for _, loadError := range packagesUnderTest[0].Errors {
		b.Fatalf("load strictpkg: %v", loadError)
	}
	program, ssaPackages := ssautil.AllPackages(packagesUnderTest, ssa.BuilderMode(0))
	program.Build()
	fn, ok := ssaPackages[0].Members[name].(*ssa.Function)
	if !ok {
		b.Fatalf("%s SSA function not found", name)
	}
	pass := &analysis.Pass{
		Fset:      packagesUnderTest[0].Fset,
		Files:     packagesUnderTest[0].Syntax,
		Pkg:       packagesUnderTest[0].Types,
		TypesInfo: packagesUnderTest[0].TypesInfo,
		// No other package was analysed, so none has exported a fact.
		ImportObjectFact: func(types.Object, analysis.Fact) bool { return false },
	}
	dataflow := newEngine(pass, []*ssa.Function{fn})
	dataflow.solveSummaries()
	return dataflow, fn
}
