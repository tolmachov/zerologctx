package zerologctx

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer,
		"./strictpkg", "./summaryprovider", "./summaryconsumer", "./indirectconsumer")
}

func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Analyzer, "./fixpkg")
}

func TestSuggestedFixesCompile(t *testing.T) {
	testdata := analysistest.TestData()
	dir := filepath.Join(testdata, "fixpkg")
	fixed, err := os.ReadFile(filepath.Join(dir, "fix.go.golden"))
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(filepath.Join(dir, "fix.go"))
	if err != nil {
		t.Fatal(err)
	}
	packagesUnderTest, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedDeps,
		Dir: testdata,
		Env: append(os.Environ(), "GOWORK=off"),
		Overlay: map[string][]byte{
			abs: fixed,
		},
	}, "./fixpkg")
	if err != nil {
		t.Fatalf("load fixed package: %v", err)
	}
	if len(packagesUnderTest) != 1 {
		t.Fatalf("loaded %d packages, want 1", len(packagesUnderTest))
	}
	for _, loadError := range packagesUnderTest[0].Errors {
		t.Errorf("source with all suggested fixes applied does not compile: %v", loadError)
	}
}

func TestJoinFrameTreatsMissingMemoryAsUnknown(t *testing.T) {
	location := memoryLocation{root: &ssa.Alloc{}}
	safe := newFrame()
	safe.memory[location] = abstractValue{kind: kindLogger, state: stateHasContext}

	for _, test := range []struct {
		name  string
		left  *frame
		right *frame
	}{
		{name: "missing first", left: newFrame(), right: safe},
		{name: "missing second", left: safe.clone(), right: newFrame()},
	} {
		t.Run(test.name, func(t *testing.T) {
			joinFrame(test.left, test.right)
			if got := test.left.memory[location].state; got != stateUnknown {
				t.Fatalf("joined memory state = %s, want unknown", got)
			}
		})
	}
}

func TestStateStrings(t *testing.T) {
	for state, want := range map[ctxState]string{
		stateUnreachable: "unreachable",
		stateHasContext:  "has-context",
		stateNoContext:   "no-context",
		stateUnknown:     "unknown",
	} {
		if got := state.String(); got != want {
			t.Errorf("state %d string = %q, want %q", state, got, want)
		}
	}
}

func TestSummaryFromFactClampsStatesOutsideTheLattice(t *testing.T) {
	fact := functionSummaryFact{
		Results:      []ctxState{stateHasContext, 9},
		ParamEffects: []ctxState{stateUnreachable, stateNoContext, 200},
	}
	summary := summaryFromFact(fact)
	if want := []ctxState{stateHasContext, stateUnknown}; !slices.Equal(summary.results, want) {
		t.Errorf("results = %d, want %d", summary.results, want)
	}
	if want := []ctxState{stateUnreachable, stateNoContext, stateUnknown}; !slices.Equal(summary.effects, want) {
		t.Errorf("effects = %d, want %d", summary.effects, want)
	}
}

func TestJoinStateIsALattice(t *testing.T) {
	if got := joinState(stateHasContext, stateNoContext); got != stateUnknown {
		t.Errorf("joinState(has-context, no-context) = %s, want unknown: the two proofs are incomparable", got)
	}
	states := []ctxState{stateUnreachable, stateHasContext, stateNoContext, stateUnknown}
	for _, a := range states {
		if got := joinState(a, a); got != a {
			t.Errorf("joinState(%s, %s) = %s, want idempotent", a, a, got)
		}
		if got := joinState(stateUnreachable, a); got != a {
			t.Errorf("joinState(unreachable, %s) = %s, want %s: bottom must be the identity", a, got, a)
		}
		if got := joinState(stateUnknown, a); got != stateUnknown {
			t.Errorf("joinState(unknown, %s) = %s, want unknown: top must absorb", a, got)
		}
		for _, b := range states {
			if joinState(a, b) != joinState(b, a) {
				t.Errorf("joinState(%s, %s) is not commutative", a, b)
			}
			for _, c := range states {
				left, right := joinState(joinState(a, b), c), joinState(a, joinState(b, c))
				if left != right {
					t.Errorf("joinState is not associative on (%s, %s, %s): %s vs %s", a, b, c, left, right)
				}
			}
		}
	}
}

func TestJoinValueWidensInsteadOfCollapsing(t *testing.T) {
	logger := abstractValue{kind: kindLogger, state: stateNoContext}
	event := abstractValue{kind: kindEvent, state: stateHasContext}
	nilEvent := abstractValue{kind: kindEvent, state: stateNoContext, nilCtx: true}
	located := abstractValue{
		kind: kindEvent, state: stateUnreachable,
		locs: map[eventLocation]struct{}{{value: &ssa.Alloc{}}: {}},
	}
	aggregate := abstractValue{elems: []abstractValue{logger, event}}
	stored := abstractValue{
		kind: kindLogger, state: stateNoContext,
		memLocs: map[memoryLocation]struct{}{{root: &ssa.Alloc{}}: {}},
	}
	values := []abstractValue{
		{}, topValue, logger, event, nilEvent, located, aggregate, stored,
		{kind: kindBuilder, state: stateUnknown}, unknownValue(kindEvent),
		{kind: kindEvent, state: stateNoContext}, {kind: kindEvent, state: stateUnknown},
		{elems: []abstractValue{topValue, event}},
	}
	for _, a := range values {
		for _, b := range values {
			if got, want := covers(a, b), equalValue(joinValue(a, b), a); got != want {
				t.Errorf("covers(%v, %v) = %t, but joining changes a: %t", a, b, got, !want)
			}
			if !equalValue(joinValue(a, b), joinValue(b, a)) {
				t.Errorf("joinValue(%v, %v) is not commutative", a, b)
			}
			for _, c := range values {
				left, right := joinValue(joinValue(a, b), c), joinValue(a, joinValue(b, c))
				if !equalValue(left, right) {
					t.Errorf("joinValue is not associative on (%v, %v, %v): %v vs %v", a, b, c, left, right)
				}
			}
		}
	}
	if joined := joinValue(logger, event); joined.state != stateUnknown {
		t.Fatalf("joining incomparable kinds gave %s, want unknown: widening must not collapse to the bottom", joined.state)
	}
	if joinValue(nilEvent, abstractValue{kind: kindEvent, state: stateNoContext}).nilCtx {
		t.Error("nilCtx is a must-property: one path without a nil Ctx clears it")
	}
	other := abstractValue{elems: []abstractValue{event, logger}}
	if joined := joinValue(aggregate, other); joined.elems[0].state != stateUnknown {
		t.Errorf("aggregate element joined to %s, want unknown: elements must be joined pointwise", joined.elems[0].state)
	}
	if !joinValue(located, unknownValue(kindEvent)).partialLocs {
		t.Error("joining in a value with no identity must mark the location set partial")
	}
	if _, ok := joinValue(located, located).mustEvent(); !ok {
		t.Error("a complete singleton location set must stay a must-alias")
	}
}

func TestEffectiveStateFailsClosedAtTheLatticeBottom(t *testing.T) {
	f := newFrame()
	location := eventLocation{value: &ssa.Alloc{}}
	f.events[location] = eventState{state: stateUnreachable}
	for name, value := range map[string]abstractValue{
		"logger":        {kind: kindLogger, state: stateUnreachable},
		"builder":       {kind: kindBuilder, state: stateUnreachable},
		"event":         {kind: kindEvent, state: stateUnreachable},
		"located event": {kind: kindEvent, state: stateUnreachable, locs: map[eventLocation]struct{}{location: {}}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := value.effectiveState(f); got != stateUnknown {
				t.Fatalf("effectiveState = %s, want unknown: a tracked value at the bottom proves nothing", got)
			}
		})
	}
	if got := (abstractValue{state: stateUnreachable}).effectiveState(f); got != stateUnreachable {
		t.Fatalf("effectiveState of an untracked value = %s, want unreachable", got)
	}
}

func TestZerologNamedUnaliases(t *testing.T) {
	pkg := types.NewPackage(zerologPkgPath, "zerolog")
	named := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Event", nil), types.NewStruct(nil, nil), nil)
	aliasPkg := types.NewPackage("example.com/app", "app")
	alias := types.NewAlias(types.NewTypeName(token.NoPos, aliasPkg, "Event", nil), named)
	if !zerologNamed(types.NewPointer(alias), "Event") {
		t.Fatal("pointer to an alias of zerolog.Event was not recognised")
	}
}

func TestIsNoLintComment(t *testing.T) {
	for _, text := range []string{
		"//nolint", "//nolint:all", "//nolint:zerologctx", "//nolint:l1, zerologctx // reason",
	} {
		if !isNoLintComment(text, "zerologctx") {
			t.Errorf("%q did not suppress zerologctx", text)
		}
	}
	for _, text := range []string{"// ordinary", "//nolint:other", "//nolintfoo", "/*nolint:zerologctx*/"} {
		if isNoLintComment(text, "zerologctx") {
			t.Errorf("%q unexpectedly suppressed zerologctx", text)
		}
	}
}
