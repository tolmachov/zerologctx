package zerologctx

import (
	"os"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/passes/buildssa"
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
	testdata := analysistest.TestData()
	packagesUnderTest, err := packages.Load(&packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  testdata,
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

	// buildssa runs without InstantiateGenerics, so the benchmark measures the
	// SSA shape the analyzer actually sees.
	program, ssaPackages := ssautil.AllPackages(packagesUnderTest, ssa.BuilderMode(0))
	program.Build()
	ssaPackage := ssaPackages[0]
	fn, ok := ssaPackage.Members["largeAliasCFG"].(*ssa.Function)
	if !ok {
		b.Fatal("largeAliasCFG SSA function not found")
	}
	pass := &analysis.Pass{
		Fset:      packagesUnderTest[0].Fset,
		Files:     packagesUnderTest[0].Syntax,
		Pkg:       packagesUnderTest[0].Types,
		TypesInfo: packagesUnderTest[0].TypesInfo,
	}
	dataflow := newEngine(pass, &buildssa.SSA{Pkg: ssaPackage, SrcFuncs: []*ssa.Function{fn}}, nil)

	b.ReportAllocs()
	for b.Loop() {
		dataflow.solve(fn)
	}
}
