package main

import (
	"testing"

	"github.com/tolmachov/zerologctx"
	"golang.org/x/tools/go/analysis"
)

func TestGetAnalyzers(t *testing.T) {
	analyzers := GetAnalyzers()

	// Check that the function returns exactly one analyzer
	if len(analyzers) != 1 {
		t.Errorf("GetAnalyzers() returned %d analyzers, want 1", len(analyzers))
	}

	// Check that the analyzer is the zerologctx.Analyzer
	if len(analyzers) > 0 {
		if analyzers[0] != zerologctx.Analyzer {
			t.Errorf("GetAnalyzers() returned wrong analyzer, got %v, want %v",
				analyzers[0].Name, zerologctx.Analyzer.Name)
		}

		// Verify it's a valid analyzer
		if analyzers[0].Name != "zerologctx" {
			t.Errorf("Analyzer name = %q, want %q", analyzers[0].Name, "zerologctx")
		}

		// Check that it's properly typed
		var _ []*analysis.Analyzer = analyzers
	}
}

func TestPluginExports(t *testing.T) {
	// Ensure the plugin properly exports the required function
	// This is mainly to ensure the function signature matches
	// what golangci-lint expects
	var fn func() []*analysis.Analyzer = GetAnalyzers

	// Call the function to ensure it doesn't panic
	result := fn()
	if result == nil {
		t.Error("GetAnalyzers() returned nil")
	}
}
