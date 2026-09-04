package main

import (
	"testing"

	"github.com/tolmachov/zerologctx"
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
