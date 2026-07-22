// Tests for the zerologctx analyzer
package zerologctx

import (
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the analyzer against test cases in the testdata directory.
// It verifies that the analyzer correctly identifies missing Ctx() calls
// in zerolog event chains.
func TestAnalyzer(t *testing.T) {
	// Get the test data directory
	testdata := analysistest.TestData()

	// Run the analyzer on the test packages.
	// logonlypkg imports only a zerolog sub-package, wrapperconsumer reaches
	// *zerolog.Event via a local wrapper without directly importing zerolog,
	// noctxpkg has neither zerolog nor "context" in its import graph and
	// must be skipped without diagnostics or errors, and scopepkg pins the
	// context-availability gate (no reachable context — no diagnostic).
	analysistest.Run(t, testdata, Analyzer, "testpkg", "logonlypkg", "wrapperconsumer", "noctxpkg", "scopepkg")
}

// TestSuggestedFixes verifies the suggested-fix output end-to-end: candidate
// selection in findCtxInScope (ctx-name preference, nearest-preceding choice,
// skipping uninitialized vars) and the TextEdit insertion point.
func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Analyzer, "fixpkg")
}

// TestIsContextType directly tests the isContextType method against synthetic
// go/types constructs. This exercises cases that cannot be expressed in the
// testdata fixtures because the stub's Ctx(context.Context) parameter rejects
// non-context values at compile time.
func TestIsContextType(t *testing.T) {
	// Build a minimal context.Context interface: Deadline, Done, Err, Value.
	pkg := types.NewPackage("ctxtest", "ctxtest")
	emptySig := types.NewSignatureType(nil, nil, nil, nil, nil, false)

	// Correct method signatures matching context.Context.
	timePkg := types.NewPackage("time", "time")
	timeType := types.NewNamed(types.NewTypeName(token.NoPos, timePkg, "Time", nil), types.NewStruct(nil, nil), nil)
	deadlineSig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(
			types.NewVar(token.NoPos, nil, "", timeType),
			types.NewVar(token.NoPos, nil, "", types.Typ[types.Bool]),
		), false)
	doneSig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, nil, "", types.NewChan(types.RecvOnly, types.NewStruct(nil, nil)))),
		false)
	errSig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Universe.Lookup("error").Type())),
		false)
	valueSig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, nil, "key", types.Universe.Lookup("any").Type())),
		types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Universe.Lookup("any").Type())),
		false)

	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, pkg, "Deadline", deadlineSig),
		types.NewFunc(token.NoPos, pkg, "Done", doneSig),
		types.NewFunc(token.NoPos, pkg, "Err", errSig),
		types.NewFunc(token.NoPos, pkg, "Value", valueSig),
	}, nil)
	iface.Complete()

	st := &state{contextIface: iface}

	// Helper to build a named struct type with the given methods.
	namedWith := func(name string, methods ...*types.Func) *types.Named {
		tn := types.NewNamed(types.NewTypeName(token.NoPos, pkg, name, nil), types.NewStruct(nil, nil), nil)
		for _, m := range methods {
			tn.AddMethod(m)
		}
		return tn
	}

	t.Run("nil contextIface returns false", func(t *testing.T) {
		nilSt := &state{contextIface: nil}
		if nilSt.isContextType(types.Typ[types.String]) {
			t.Error("isContextType with nil contextIface should return false")
		}
	})

	t.Run("nil type returns false", func(t *testing.T) {
		if st.isContextType(nil) {
			t.Error("isContextType(nil) should return false")
		}
	})

	t.Run("map type returns false", func(t *testing.T) {
		mapType := types.NewMap(types.Typ[types.String], types.Universe.Lookup("any").Type())
		if st.isContextType(mapType) {
			t.Error("map type should not satisfy context.Context")
		}
	})

	t.Run("wrong method signatures returns false", func(t *testing.T) {
		// Struct has correct method names but all return nothing (wrong signatures).
		wrongType := namedWith("WrongCtx",
			types.NewFunc(token.NoPos, pkg, "Deadline", emptySig),
			types.NewFunc(token.NoPos, pkg, "Done", emptySig),
			types.NewFunc(token.NoPos, pkg, "Err", emptySig),
			types.NewFunc(token.NoPos, pkg, "Value", emptySig),
		)
		if st.isContextType(wrongType) {
			t.Error("type with wrong method signatures should not satisfy context.Context")
		}
	})

	t.Run("correct implementation returns true", func(t *testing.T) {
		goodType := namedWith("GoodCtx",
			types.NewFunc(token.NoPos, pkg, "Deadline", deadlineSig),
			types.NewFunc(token.NoPos, pkg, "Done", doneSig),
			types.NewFunc(token.NoPos, pkg, "Err", errSig),
			types.NewFunc(token.NoPos, pkg, "Value", valueSig),
		)
		if !st.isContextType(goodType) {
			t.Error("type with correct method signatures should satisfy context.Context")
		}
	})

	t.Run("pointer to correct implementation returns true", func(t *testing.T) {
		goodType := namedWith("GoodCtxPtr",
			types.NewFunc(token.NoPos, pkg, "Deadline", deadlineSig),
			types.NewFunc(token.NoPos, pkg, "Done", doneSig),
			types.NewFunc(token.NoPos, pkg, "Err", errSig),
			types.NewFunc(token.NoPos, pkg, "Value", valueSig),
		)
		if !st.isContextType(types.NewPointer(goodType)) {
			t.Error("pointer to type with correct method signatures should satisfy context.Context")
		}
	})
}

// TestAnalyzerHelpers tests the helper functions used by the analyzer.
func TestAnalyzerHelpers(t *testing.T) {
	// Note: isContextType is directly tested in TestIsContextType above with
	// synthetic go/types constructs that cover negative cases not expressible
	// in testdata fixtures.

	// Test the isNoLintComment function
	t.Run("isNoLintComment", func(t *testing.T) {
		testCases := []struct {
			comment  string
			linter   string
			expected bool
		}{
			{"//nolint:zerologctx", "zerologctx", true},
			{"// nolint:zerologctx", "zerologctx", true},
			{"//nolint: zerologctx", "zerologctx", true},
			{"// nolint: zerologctx", "zerologctx", true},
			{"//   nolint: zerologctx", "zerologctx", true},
			{"//nolint:linter1,zerologctx,linter2", "zerologctx", true},
			{"//nolint:linter1, zerologctx, linter2", "zerologctx", true},
			{"//   nolint: another1,zerologctx,another2", "zerologctx", true},
			{"//   nolint: another1, zerologctx, another2", "zerologctx", true},
			{"//nolint:otherlinter", "zerologctx", false},
			{"//nolint:linter1,linter2", "zerologctx", false},
			{"// just a comment", "zerologctx", false},
			{"//nolint", "zerologctx", true},                       // bare nolint suppresses all linters
			{"// nolint", "zerologctx", true},                      // bare nolint with leading space
			{"//nolint:all", "zerologctx", true},                   // explicit all
			{"//nolint:zerologctx // because", "zerologctx", true}, // trailing reason
			{"//nolint:l1,zerologctx // because", "zerologctx", true},
			{"//nolint:", "zerologctx", false}, // empty linter list
			// Edge cases: malformed directives
			{"//nolint:ZerolOGCTX", "zerologctx", false},             // case sensitive linter names
			{"//nolint // reason without colon", "zerologctx", true}, // bare nolint is valid
			{"/* nolint:zerologctx */", "zerologctx", false},         // block comments are not nolint directives
		}

		for _, tc := range testCases {
			t.Run(tc.comment, func(t *testing.T) {
				got := isNoLintComment(tc.comment, tc.linter)
				if got != tc.expected {
					t.Errorf("isNoLintComment(%q, %q) = %v, want %v", tc.comment, tc.linter, got, tc.expected)
				}
			})
		}
	})
}
