package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tolmachov/zerologctx"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestGetAnalyzers verifies the plugin entry point's contract: returns
// exactly one analyzer and it is the zerologctx analyzer.
func TestGetAnalyzers(t *testing.T) {
	analyzers := GetAnalyzers()

	if len(analyzers) != 1 {
		t.Fatalf("GetAnalyzers() returned %d analyzers, want 1", len(analyzers))
	}
	if analyzers[0] != zerologctx.Analyzer {
		t.Errorf("GetAnalyzers() returned wrong analyzer instance, got %v",
			analyzers[0].Name)
	}
	if analyzers[0].Name != "zerologctx" {
		t.Errorf("analyzer name = %q, want %q", analyzers[0].Name, "zerologctx")
	}
}

// TestPluginAnalyzerEndToEnd runs the analyzer returned by GetAnalyzers
// through analysistest against the project's testdata fixtures, ensuring
// the plugin entry point exposes a functionally equivalent analyzer (not
// just a same-named placeholder).
func TestPluginAnalyzerEndToEnd(t *testing.T) {
	analyzers := GetAnalyzers()
	if len(analyzers) == 0 {
		t.Fatal("GetAnalyzers returned no analyzers")
	}

	// Locate the parent module's testdata directory regardless of cwd.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine plugin_test.go path")
	}
	if !filepath.IsAbs(thisFile) {
		t.Skipf("source path %q is not absolute (built with -trimpath?); skipping path-dependent test", thisFile)
	}
	testdata := filepath.Join(filepath.Dir(thisFile), "..", "testdata")

	analysistest.Run(t, testdata, analyzers[0], "testpkg", "logonlypkg")
}
