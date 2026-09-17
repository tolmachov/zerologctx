// Package zerologctx provides a strict static-analysis linter for zerolog.
// Every statically identifiable output operation must be proven to carry a
// context.Context, and unknown provenance is deliberately reported.
//
// Silence has exactly three causes, and no others:
//
//   - the analyzer proved the event or logger carries a context;
//   - the sink was explicitly suppressed with a //nolint directive;
//   - the sink is unreachable, so it can never execute.
//
// See [Analyzer] for the user-facing description of what is checked.
package zerologctx

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
)

// Analyzer is the zerologctx analyzer. See its Doc field for the user-facing
// description.
var Analyzer = &analysis.Analyzer{
	Name: "zerologctx",
	Doc: `reports zerolog output operations that are not proven to carry context.Context

The analyzer follows zerolog values through SSA control flow, aliases, local
memory, stores and loads, Phi nodes, reassignment, promoted methods, method
expressions, method values, statically resolved interface calls, and function
summaries within and across packages. At a control-flow join, context is proven
only when every reachable path proves it.

Checked sinks: (*zerolog.Event).Msg, Msgf, MsgFunc and Send; zerolog.Logger
Print, Printf, Println and Write; package-level log.Print and log.Printf.

Ctx is order-sensitive: the last call wins, and a final Ctx(nil) has its own
diagnostic. Anything the analyzer cannot follow - a value captured by a
closure, one written into a slice, map, channel or global, a receiver field, an
opaque call - stays unknown and is reported. A goroutine establishes nothing
where it is spawned.

Suppress an individual sink with //nolint:zerologctx, either at the end of a
line the call spans or on a line of its own directly above it. Bare //nolint
and //nolint:all also suppress.

The analyzer has no configuration.`,
	Requires:  []*analysis.Analyzer{buildssa.Analyzer},
	Run:       run,
	FactTypes: []analysis.Fact{new(functionSummaryFact)},
}

type valueKind uint8

const (
	kindOther valueKind = iota
	kindLogger
	kindEvent
	kindBuilder
)

// ctxState is the four-point lattice the whole analysis rests on:
// stateUnreachable is the bottom, stateHasContext and stateNoContext are the
// two incomparable proofs, and stateUnknown is the top. Ordering matters:
// the bottom must be the zero value so that an absent map entry reads as
// "nothing known yet" rather than as a proof.
type ctxState uint8

const (
	stateUnreachable ctxState = iota
	stateHasContext
	stateNoContext
	stateUnknown
)

// joinState is the least upper bound. The bottom is a two-sided identity and
// the top absorbs, so the join is commutative, associative and idempotent -
// properties the worklist relies on for its result to be independent of the
// order in which predecessors are merged. Treating the bottom as an identity
// is only sound because it appears exclusively where a path is genuinely
// unreachable; every other "nothing proven" is the top.
func joinState(a, b ctxState) ctxState {
	if a == stateUnreachable {
		return b
	}
	if b == stateUnreachable || a == b {
		return a
	}
	return stateUnknown
}

func (s ctxState) String() string {
	switch s {
	case stateUnreachable:
		return "unreachable"
	case stateHasContext:
		return "has-context"
	case stateNoContext:
		return "no-context"
	default:
		return "unknown"
	}
}

// functionSummaryFact carries a function's proven postconditions across
// package boundaries. Both vectors hold raw ctxState values in one encoding:
// stateUnreachable means nothing was proven for that entry — for a result it
// is no proof, for a parameter it is "the callee preserves the argument". An
// entry is never implicitly safe, and a function with no proof at all exports
// no fact, so a missing fact reads as unknown.
type functionSummaryFact struct {
	Results      []ctxState
	ParamEffects []ctxState
}

func (*functionSummaryFact) AFact() {}

func (f *functionSummaryFact) String() string {
	return fmt.Sprintf("zerolog context summary results=%v effects=%v", f.Results, renderEffects(f.ParamEffects))
}

// renderEffects spells out what the bottom state means for a parameter, so the
// one encoding does not read as two.
func renderEffects(effects []ctxState) []string {
	rendered := make([]string, len(effects))
	for idx, effect := range effects {
		rendered[idx] = effect.String()
		if effect == stateUnreachable {
			rendered[idx] = "preserved"
		}
	}
	return rendered
}

func run(pass *analysis.Pass) (any, error) {
	ssaResult, ok := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	if !ok {
		return nil, fmt.Errorf("zerologctx: buildssa analyzer result missing")
	}
	hasZerolog, contextIface := scanImports(pass.Pkg)
	if !hasZerolog {
		return nil, nil
	}
	// contextIface may be nil: export data records only the imports a package's
	// API needs, so context can be absent from the graph of a package that
	// nonetheless logs. Only suggested fixes consult it.
	sources, err := newSourceIndex(pass, contextIface)
	if err != nil {
		return nil, err
	}
	engine := newEngine(pass, ssaResult, sources)
	engine.solveSummaries()
	engine.report()
	engine.exportSummaries()
	return nil, nil
}

func scanImports(pkg *types.Package) (bool, *types.Interface) {
	seen := map[*types.Package]bool{}
	var has bool
	var contextIface *types.Interface
	var visit func(*types.Package)
	visit = func(p *types.Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		if p.Path() == zerologPkgPath || strings.HasPrefix(p.Path(), zerologPkgPath+"/") {
			has = true
		}
		if p.Path() == "context" {
			if obj := p.Scope().Lookup("Context"); obj != nil {
				contextIface, _ = obj.Type().Underlying().(*types.Interface)
			}
		}
		for _, imp := range p.Imports() {
			visit(imp)
		}
	}
	visit(pkg)
	return has, contextIface
}
