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
opaque call - stays unknown and is reported. Once a variable's address has gone
somewhere the analyzer cannot follow, a proof about it lasts only until the
next call or write through an untracked pointer. A goroutine establishes
nothing where it is spawned. A sink in a goroutine or a deferred call is judged
against every state from its statement through function exit.

Suppress an individual sink with //nolint:zerologctx, either at the end of a
line the call spans or on a line of its own directly above it. Bare //nolint
and //nolint:all also suppress.

The analyzer has no configuration.`,
	Requires:  []*analysis.Analyzer{ctrlflow.Analyzer},
	Run:       run,
	FactTypes: []analysis.Fact{new(functionSummary)},
}

func run(pass *analysis.Pass) (any, error) {
	hasZerolog, contextIface := scanImports(pass.Pkg)
	if !hasZerolog {
		return nil, nil
	}
	// Being in zerolog's import graph is not the same as using it. Most
	// packages of a large repository never refer to zerolog, and building SSA
	// is by far the most expensive thing this analyzer does; for those
	// packages it produces nothing to report and nothing to export.
	if !refersToZerolog(pass.TypesInfo) {
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

// refersToZerolog reports whether the package's own code names anything
// declared by zerolog or its log package, or holds a value of a zerolog type.
// The first catches the log package's sinks, which take no zerolog value, and
// methods promoted from an embedded zerolog field; the second catches a value
// obtained from elsewhere without naming zerolog at all.
func refersToZerolog(info *types.Info) bool {
	for _, object := range info.Uses {
		if pkg := object.Pkg(); pkg != nil && (pkg.Path() == zerologPkgPath || pkg.Path() == zerologLogPath) {
			return true
		}
	}
	for _, typeAndValue := range info.Types {
		if kindOf(typeAndValue.Type) != kindOther {
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
