package zerologctx

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

var loadFixtureSSA = sync.OnceValues(func() (*ssa.Package, error) {
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.LoadAllSyntax, Dir: analysistest.TestData(),
		Env: append(os.Environ(), "GOWORK=off"),
	}, "./strictpkg")
	if err != nil {
		return nil, err
	}
	if len(loaded) != 1 {
		return nil, fmt.Errorf("loaded %d packages, want 1", len(loaded))
	}
	if len(loaded[0].Errors) != 0 {
		return nil, loaded[0].Errors[0]
	}
	program, built := ssautil.AllPackages(loaded, ssa.BuilderMode(0))
	program.Build()
	return built[0], nil
})

func fixtureFunction(t testing.TB, name string) *ssa.Function {
	t.Helper()
	pkg, err := loadFixtureSSA()
	if err != nil {
		t.Fatal(err)
	}
	fn := pkg.Func(name)
	if fn == nil {
		t.Fatalf("missing fixture function %s", name)
	}
	return fn
}

func fixtureAllocation(t testing.TB, fn *ssa.Function) *ssa.Alloc {
	t.Helper()
	var allocation *ssa.Alloc
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok {
				allocation = alloc
			}
		}
	}
	if allocation == nil {
		t.Fatalf("allocation missing in %s", fn.Name())
	}
	return allocation
}

// A store through one parameter must invalidate a proof through a possibly
// aliased parameter, without claiming the invalidation was a write to it.
func TestStateStoreOwnsAliasInvalidation(t *testing.T) {
	fn := fixtureFunction(t, "twoLoggerParams")
	first, second, value := fn.Params[1], fn.Params[2], fn.Params[3]
	e := newEngine(nil, nil)
	f := newFrame()
	f.seedParameter(first)
	f.seedParameter(second)
	remember(f, value, abstractValue{kind: kindLogger, state: stateHasContext})
	e.applyPostcondition(f, second, parameterEffect{state: stateHasContext})
	if got, _ := e.observe(second, f); got != stateHasContext {
		t.Fatalf("postcondition = %s, want context", got)
	}
	e.store(f, first, value)
	if got, _ := e.observe(second, f); got != stateUnknown {
		t.Fatalf("aliased proof after store = %s, want unknown", got)
	}
	if got := f.parameterState(first); !got.written || got.escaped {
		t.Fatalf("actual store = %+v, want written without escape", got)
	}
	// A parameter never touched by a store is widened, but not written.
	untouched := newFrame()
	untouched.seedParameter(first)
	untouched.seedParameter(second)
	remember(untouched, value, abstractValue{kind: kindLogger, state: stateNoContext})
	e.store(untouched, second, value)
	if got := untouched.parameterState(first); got.written || got.escaped {
		t.Fatalf("alias widening = %+v, want neither write nor escape", got)
	}
}

// Revisiting a self-reference must terminate without retaining an event proof.
func TestStateEscapeClosesOverCyclicAggregateMemory(t *testing.T) {
	fn := fixtureFunction(t, "stateGraph")
	event := fn.Params[0]
	holder := fixtureAllocation(t, fn)
	e := newEngine(nil, nil)
	f := newFrame()
	f.seedParameter(event)
	updateEvent(f, e.value(f, event), eventState{state: stateHasContext})
	root := memoryLocation{root: holder}
	storeValue(f, root, abstractValue{elems: []abstractValue{
		{memLocs: map[memoryLocation]struct{}{root: {}}}, e.value(f, event),
	}})
	e.invalidateEscape(f, holder)
	if got, _ := e.observe(event, f); got != stateUnknown {
		t.Fatalf("escaped nested event = %s, want unknown", got)
	}
	if got := f.parameterState(event); !got.escaped {
		t.Fatalf("nested parameter = %+v, want escaped", got)
	}
	updateEvent(f, e.value(f, event), eventState{state: stateHasContext})
	if got, _ := e.observe(event, f); got != stateHasContext {
		t.Fatalf("reproof = %s, want context", got)
	}
	forgetEscaped(f)
	if got, _ := e.observe(event, f); got != stateUnknown {
		t.Fatalf("proof after later call = %s, want unknown", got)
	}
}

func TestStateEscapeTraversesEachRoute(t *testing.T) {
	fn := fixtureFunction(t, "stateGraph")
	event, opaque := fn.Params[0], fn.Params[2]
	holder := fixtureAllocation(t, fn)
	for _, route := range []string{"event", "aggregate", "memory", "syntax"} {
		t.Run(route, func(t *testing.T) {
			e := newEngine(nil, nil)
			f := newFrame()
			f.seedParameter(event)
			known := e.value(f, event)
			updateEvent(f, known, eventState{state: stateHasContext})
			input := ssa.Value(event)
			root := memoryLocation{root: holder}
			switch route {
			case "aggregate":
				remember(f, opaque, abstractValue{elems: []abstractValue{{elems: []abstractValue{known}}}})
				input = opaque
			case "memory":
				storeValue(f, root, abstractValue{elems: []abstractValue{topValue, known}})
				remember(f, opaque, abstractValue{memLocs: map[memoryLocation]struct{}{root: {}}})
				input = opaque
			case "syntax":
				storeValue(f, root, abstractValue{elems: []abstractValue{topValue, known}})
				// Deliberately omit abstract locations to isolate syntactic reachability.
				f.values[holder] = topValue
				input = holder
			}
			e.invalidateEscape(f, input)
			if got, _ := e.observe(event, f); got != stateUnknown {
				t.Fatalf("escaped event = %s, want unknown", got)
			}
			if !f.parameterState(event).escaped {
				t.Fatal("reachable event not marked escaped")
			}
		})
	}
}

func TestStateStoreKeepsEscapeAfterSlotOverwrite(t *testing.T) {
	fn := fixtureFunction(t, "storeIntoEscapedSlot")
	slot := fixtureAllocation(t, fn)
	event := fixtureFunction(t, "twoEventParams").Params[1]
	e := newEngine(nil, nil)
	f := newFrame()
	f.seedParameter(event)
	e.invalidateEscape(f, slot)
	e.store(f, slot, event)
	e.store(f, slot, ssa.NewConst(nil, event.Type()))
	updateEvent(f, e.value(f, event), eventState{state: stateHasContext})
	forgetEscaped(f)
	if got, _ := e.observe(event, f); got != stateUnknown {
		t.Fatalf("event once reachable from escaped slot = %s, want unknown", got)
	}
}

func TestStatePostconditionDoesNotProveMayAliases(t *testing.T) {
	fn := fixtureFunction(t, "twoEventParams")
	first, second := fn.Params[1], fn.Params[2]
	e := newEngine(nil, nil)
	f := newFrame()
	f.seedParameter(first)
	f.seedParameter(second)
	joined := joinValue(e.value(f, first), e.value(f, second))
	// Bind the joined identity to an SSA register, as a Phi would.
	remember(f, first, joined)
	e.applyPostcondition(f, first, parameterEffect{state: stateHasContext})
	if got, _ := e.observe(second, f); got != stateUnknown {
		t.Fatalf("may-alias postcondition = %s, want unknown", got)
	}
}

func TestStateEventCreationUsesSolverLiveness(t *testing.T) {
	parameter := fixtureFunction(t, "twoEventParams").Params[1]
	location := eventLocation{value: parameter}
	f := newFrame()
	first := f.seedEvent(location, eventState{state: stateHasContext}, false)
	second := f.seedEvent(location, eventState{state: stateNoContext}, true)
	if second.effectiveState(f) != stateUnknown || first.effectiveState(f) != stateUnknown {
		t.Fatal("a held earlier event must share the joined proof")
	}
	if _, must := second.mustEvent(); must {
		t.Fatal("a reused location must not receive a strong postcondition")
	}
	third := f.seedEvent(location, eventState{state: stateHasContext}, false)
	if third.effectiveState(f) != stateHasContext {
		t.Fatal("an unheld location must be seeded with the new event proof")
	}
	if _, must := third.mustEvent(); !must || f.parameterState(parameter).written {
		t.Fatal("a new event is a must-alias, and seeding is not a parameter write")
	}
}
