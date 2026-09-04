package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
	"github.com/tolmachov/zerologctx"
)

// TestPluginRegistration verifies the contract golangci-lint relies on: the
// package registers itself under the linter's name at init, and the plugin it
// builds exposes the zerologctx analyzer with a load mode that provides type
// information.
func TestPluginRegistration(t *testing.T) {
	newPlugin, err := register.GetPlugin("zerologctx")
	if err != nil {
		t.Fatalf("plugin not registered: %v", err)
	}

	p, err := newPlugin(nil)
	if err != nil {
		t.Fatalf("New(nil) failed: %v", err)
	}

	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatalf("BuildAnalyzers failed: %v", err)
	}
	if len(analyzers) != 1 {
		t.Fatalf("BuildAnalyzers returned %d analyzers, want 1", len(analyzers))
	}
	if analyzers[0] != zerologctx.Analyzer {
		t.Errorf("BuildAnalyzers returned %q, want the zerologctx analyzer", analyzers[0].Name)
	}

	if got := p.GetLoadMode(); got != register.LoadModeTypesInfo {
		t.Errorf("GetLoadMode() = %q, want %q", got, register.LoadModeTypesInfo)
	}
}

// TestPluginRejectsSettings pins that a settings block aimed at zerologctx is
// an error rather than a no-op, since the analyzer has nothing to configure.
func TestPluginRejectsSettings(t *testing.T) {
	if _, err := New(map[string]any{"level": "warn"}); err == nil {
		t.Error("New with unknown settings returned no error, want one")
	}
}
