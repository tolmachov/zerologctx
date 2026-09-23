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
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/types/typeutil"
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
where it is spawned. A sink in a goroutine or a deferred call is judged against
every state from its statement through function exit.

Suppress an individual sink with //nolint:zerologctx, either at the end of a
line the call spans or on a line of its own directly above it. Bare //nolint
and //nolint:all also suppress.

The analyzer has no configuration.`,
	Requires:  []*analysis.Analyzer{ctrlflow.Analyzer},
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
	hasZerolog, contextIface := scanImports(pass.Pkg)
	if !hasZerolog {
		return nil, nil
	}
	// Being in zerolog's import graph is not the same as using it. Most
	// packages of a large repository never name a zerolog value, and building
	// SSA for them is what made the analyzer unaffordable: it is by far the
	// most expensive thing this analyzer does, and for those packages it
	// produces nothing to report and nothing to export.
	if !usesZerologValues(pass.TypesInfo) {
		return nil, nil
	}
	srcFuncs, err := buildPackageSSA(pass)
	if err != nil {
		return nil, err
	}
	engine := newEngine(pass, srcFuncs)
	engine.solveSummaries()
	// contextIface may be nil: export data records only the imports a package's
	// API needs, so context can be absent from the graph of a package that
	// nonetheless logs. Only suggested fixes consult it.
	if err := engine.report(contextIface); err != nil {
		return nil, err
	}
	engine.exportSummaries()
	return nil, nil
}

// usesZerologValues reports whether the package's own code manipulates a
// zerolog Logger, Event or Context. It asks about types rather than imports,
// because a package can use a value obtained from elsewhere without naming
// zerolog itself.
func usesZerologValues(info *types.Info) bool {
	for _, typeAndValue := range info.Types {
		if kindOf(typeAndValue.Type) != kindOther {
			return true
		}
	}
	for _, object := range info.Defs {
		if object != nil && kindOf(object.Type()) != kindOther {
			return true
		}
	}
	return false
}

// buildPackageSSA builds SSA for this package alone. Requiring buildssa would
// hand the same job to the driver, which then does it for every package in the
// graph whether or not this analyzer needs it.
func buildPackageSSA(pass *analysis.Pass) ([]*ssa.Function, error) {
	cfgs, ok := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)
	if !ok {
		return nil, fmt.Errorf("zerologctx: ctrlflow analyzer result missing")
	}
	program := ssa.NewProgram(pass.Fset, ssa.BuilderMode(0))
	// Without this, code after a call that can only panic looks reachable.
	program.SetNoReturn(cfgs.NoReturn)
	for _, imported := range pass.Pkg.Imports() {
		program.CreatePackage(imported, nil, nil, true)
	}
	ssaPackage := program.CreatePackage(pass.Pkg, pass.Files, pass.TypesInfo, false)
	ssaPackage.Build()

	var funcs []*ssa.Function
	var addWithAnons func(*ssa.Function)
	addWithAnons = func(fn *ssa.Function) {
		funcs = append(funcs, fn)
		for _, anon := range fn.AnonFuncs {
			addWithAnons(anon)
		}
	}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			decl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			object, ok := pass.TypesInfo.Defs[decl.Name].(*types.Func)
			if !ok {
				continue
			}
			if fn := program.FuncValue(object); fn != nil {
				addWithAnons(fn)
			}
		}
	}
	// Package-level variable initializers, and every closure they contain, run
	// in the synthetic package initializer, which no declaration names.
	addWithAnons(ssaPackage.Func("init"))
	return funcs, nil
}

func scanImports(pkg *types.Package) (bool, *types.Interface) {
	var has bool
	var contextIface *types.Interface
	for _, p := range typeutil.Dependencies(pkg) {
		if p.Path() == zerologPkgPath || strings.HasPrefix(p.Path(), zerologPkgPath+"/") {
			has = true
		}
		if p.Path() == "context" {
			if obj := p.Scope().Lookup("Context"); obj != nil {
				contextIface, _ = obj.Type().Underlying().(*types.Interface)
			}
		}
	}
	return has, contextIface
}
