// Benchmarks for the zerologctx analyzer
package zerologctx

import (
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// BenchmarkAnalyzer benchmarks the analyzer on the test cases.
// This helps identify performance bottlenecks and track performance over time.
func BenchmarkAnalyzer(b *testing.B) {
	testdata := analysistest.TestData()
	b.ResetTimer()

	for b.Loop() {
		analysistest.Run(b, testdata, Analyzer, "testpkg")
	}
}

// BenchmarkIsNoLintComment benchmarks the nolint comment parsing function.
func BenchmarkIsNoLintComment(b *testing.B) {
	testCases := []struct {
		name     string
		comment  string
		linter   string
		expected bool
	}{
		{"simple", "//nolint:zerologctx", "zerologctx", true},
		{"with_spaces", "// nolint: zerologctx", "zerologctx", true},
		{"multiple", "//nolint:linter1,zerologctx,linter2", "zerologctx", true},
		{"not_found", "//nolint:otherlinter", "zerologctx", false},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				_ = isNoLintComment(tc.comment, tc.linter)
			}
		})
	}
}

// BenchmarkIsContextType benchmarks the context-interface satisfaction check
// against a synthetic *types.Named that fully implements context.Context.
//
// The named type's methods and the interface's methods are constructed from
// separate *types.Func objects with matching but distinct signatures so that
// types.Implements performs real signature comparison rather than the trivial
// same-pointer-identity fast path.
func BenchmarkIsContextType(b *testing.B) {
	pkg := types.NewPackage("ctxbench", "ctxbench")

	// time.Time placeholder (just needs to be a distinct named type).
	timePkg := types.NewPackage("time", "time")
	timeType := types.NewNamed(types.NewTypeName(token.NoPos, timePkg, "Time", nil), types.NewStruct(nil, nil), nil)

	// <-chan struct{} for Done() — must be receive-only, matching context.Context.
	chanStruct := types.NewChan(types.RecvOnly, types.NewStruct(nil, nil))

	// Deadline() (time.Time, bool)
	deadlineSig := func() *types.Signature {
		results := types.NewTuple(
			types.NewVar(token.NoPos, nil, "", timeType),
			types.NewVar(token.NoPos, nil, "", types.Typ[types.Bool]),
		)
		return types.NewSignatureType(nil, nil, nil, nil, results, false)
	}
	// Done() <-chan struct{}
	doneSig := func() *types.Signature {
		results := types.NewTuple(types.NewVar(token.NoPos, nil, "", chanStruct))
		return types.NewSignatureType(nil, nil, nil, nil, results, false)
	}
	// Err() error
	errSig := func() *types.Signature {
		results := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Universe.Lookup("error").Type()))
		return types.NewSignatureType(nil, nil, nil, nil, results, false)
	}
	// Value(key any) any
	valueSig := func() *types.Signature {
		params := types.NewTuple(types.NewVar(token.NoPos, nil, "key", types.Universe.Lookup("any").Type()))
		results := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Universe.Lookup("any").Type()))
		return types.NewSignatureType(nil, nil, nil, params, results, false)
	}

	// Build the interface with one set of Func objects.
	ifaceMethods := []*types.Func{
		types.NewFunc(token.NoPos, pkg, "Deadline", deadlineSig()),
		types.NewFunc(token.NoPos, pkg, "Done", doneSig()),
		types.NewFunc(token.NoPos, pkg, "Err", errSig()),
		types.NewFunc(token.NoPos, pkg, "Value", valueSig()),
	}
	iface := types.NewInterfaceType(ifaceMethods, nil)
	iface.Complete()

	// Build the named type with a separate set of Func objects (same signatures,
	// different pointers) so types.Implements must compare signatures rather
	// than short-circuit on pointer identity.
	tn := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "MyCtx", nil), types.NewStruct(nil, nil), nil)
	for _, name := range []string{"Deadline", "Done", "Err", "Value"} {
		var sig *types.Signature
		switch name {
		case "Deadline":
			sig = deadlineSig()
		case "Done":
			sig = doneSig()
		case "Err":
			sig = errSig()
		case "Value":
			sig = valueSig()
		}
		tn.AddMethod(types.NewFunc(token.NoPos, pkg, name, sig))
	}

	st := &state{contextIface: iface}

	b.ResetTimer()
	for b.Loop() {
		_ = st.isContextType(tn)
	}
}
