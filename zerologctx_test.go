// Tests for the zerologctx analyzer
package zerologctx

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"
)

// TestAnalyzer runs the analyzer against test cases in the testdata directory.
// It verifies that the analyzer correctly identifies missing Ctx() calls
// in zerolog event chains.
func TestAnalyzer(t *testing.T) {
	// Get the test data directory
	testdata := analysistest.TestData()

	// Run the analyzer on the test packages. Every package whose fixtures are
	// meant to assert something must be listed: analysistest checks `want`
	// comments only in the packages named here, and runs the rest purely as
	// dependencies.
	//
	// logonlypkg imports only a zerolog sub-package; wrappkg is both a
	// dependency of wrapperconsumer and the source of the exported ctxCarrier
	// facts, whose `want` annotations pin the exported/unexported split;
	// wrapperconsumer reaches *zerolog.Event via a local wrapper without
	// directly importing zerolog and consumes those facts; noctxpkg has
	// neither zerolog nor "context" in its import graph and must be skipped
	// without diagnostics or errors; scopepkg pins the context-availability
	// gate (no reachable context — no diagnostic); deepchainpkg pins fixpoint
	// convergence for a dependency chain deeper than any fixed pass budget.
	analysistest.Run(t, testdata, Analyzer, "testpkg", "logonlypkg", "wrappkg", "wrapperconsumer", "noctxpkg", "scopepkg", "deepchainpkg")
}

// TestSuggestedFixes verifies the suggested-fix output end-to-end: candidate
// selection in reachableCtx (ctx-name preference, nearest-preceding choice,
// skipping variables that only ever hold nil, address-taking for
// pointer-receiver context types) and the TextEdit insertion point.
func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), Analyzer, "fixpkg", "pkgctxpkg")
}

// TestFactTableJoin pins the property collectFacts' termination rests on:
// writes at one position join rather than overwrite, so the table only ever
// ascends. token.Pos does not uniquely identify a write site — Go allows the
// same assignment target twice in one statement, and an ExprStmt shares its
// position with a composite literal it starts with — so same-position
// collisions are reachable from ordinary code, and an overwrite would let a
// later write undo an earlier one and the fixpoint loop spin.
func TestFactTableJoin(t *testing.T) {
	zl := types.NewPackage("github.com/rs/zerolog", "zerolog")
	logger := types.NewNamed(types.NewTypeName(token.NoPos, zl, "Logger", nil), types.NewStruct(nil, nil), nil)
	obj := types.NewVar(token.NoPos, zl, "l", logger)
	const pos = token.Pos(10)

	tbl := newFactTable()
	tbl.set(obj, pos, true)
	writes := tbl.writes

	tbl.set(obj, pos, false)
	if !tbl.entries[obj][pos] {
		t.Error("a false write at an occupied position cleared the context fact; writes must join")
	}
	if tbl.writes != writes {
		t.Errorf("a write that changed nothing counted as progress: writes %d -> %d", writes, tbl.writes)
	}

	// The reverse order must reach the same state, and must count as progress
	// so the fixpoint loop runs another pass.
	tbl2 := newFactTable()
	tbl2.set(obj, pos, false)
	writes = tbl2.writes
	tbl2.set(obj, pos, true)
	if !tbl2.entries[obj][pos] {
		t.Error("a context fact did not supersede an earlier contextless write at the same position")
	}
	if tbl2.writes == writes {
		t.Error("an ascending write was not counted as progress; the fixpoint loop would stop early")
	}
}

// TestSuggestedFixesCompile type-checks fixpkg.go.golden — the source
// analysistest produces by applying every suggested fix — against the same
// GOPATH-style testdata tree the analyzer runs on.
//
// TestSuggestedFixes only proves the fixed text is the text we expected; it
// says nothing about whether that text compiles. Three separate classes of
// broken fix (a candidate name shadowed at the call site, a value whose
// context methods use pointer receivers, a blank receiver field) shipped
// under a green golden comparison. Any fix that does not type-check now fails
// here.
func TestSuggestedFixesCompile(t *testing.T) {
	testdata := analysistest.TestData()
	// Every package TestSuggestedFixes drives must appear here, or its fixes
	// are compared as text and never compiled.
	for _, pkg := range []string{"fixpkg", "pkgctxpkg"} {
		t.Run(pkg, func(t *testing.T) {
			dir := filepath.Join(testdata, "src", pkg)
			fixed, err := os.ReadFile(filepath.Join(dir, pkg+".go.golden"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			cfg := &packages.Config{
				Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
					packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedDeps,
				Dir: testdata,
				// GOPATH mode, matching analysistest's own loader.
				Env: append(os.Environ(), "GOPATH="+testdata, "GO111MODULE=off", "GOWORK=off"),
				Overlay: map[string][]byte{
					filepath.Join(dir, pkg+".go"): fixed,
				},
			}
			pkgs, err := packages.Load(cfg, pkg)
			if err != nil {
				t.Fatalf("load %s with suggested fixes applied: %v", pkg, err)
			}
			if len(pkgs) != 1 {
				t.Fatalf("loaded %d packages, want 1", len(pkgs))
			}
			if pkgs[0].Name == "" {
				t.Fatalf("%s failed to load at all: %v", pkg, pkgs[0].Errors)
			}
			for _, e := range pkgs[0].Errors {
				t.Errorf("source with suggested fixes applied does not compile: %v", e)
			}
		})
	}
}

// TestIsContextType directly tests context-interface satisfaction against
// synthetic go/types constructs, covering shapes the testdata fixtures cannot
// express: a type whose methods have the right names but the wrong signatures,
// and non-named types. The addressableContext half of isContextType — a value
// whose type satisfies context.Context only through its pointer — is covered
// end-to-end by fixpkg, which pins the resulting &v fix text.
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
