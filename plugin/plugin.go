// Package plugin registers zerologctx as a golangci-lint module plugin.
//
// golangci-lint v2 builds a custom binary that imports this package for its
// registration side effect. See the README for the host configuration, and
// testdata/pluginfixture/custom-gcl.yml for the example CI actually builds.
package plugin

import (
	"fmt"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"

	"github.com/tolmachov/zerologctx"
)

func init() {
	register.Plugin("zerologctx", New)
}

// New builds the plugin. The analyzer has no configuration surface, so any
// settings supplied for it are rejected rather than silently ignored: a typo'd
// or hopeful block in .golangci.yml doing nothing is worse than an error.
func New(settings any) (register.LinterPlugin, error) {
	if _, err := register.DecodeSettings[struct{}](settings); err != nil {
		return nil, fmt.Errorf("zerologctx takes no settings: %w", err)
	}
	return &zerologctxPlugin{}, nil
}

type zerologctxPlugin struct{}

// BuildAnalyzers returns the analyzers golangci-lint should run.
func (*zerologctxPlugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{zerologctx.Analyzer}, nil
}

// GetLoadMode reports the load mode the analyzer needs. Every predicate is
// type-driven — zerolog type identity, context.Context satisfaction — and the
// analyzer exchanges facts across packages, so syntax alone is not enough.
func (*zerologctxPlugin) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
