// Package main is the golangci-lint custom-linter plugin entry point for
// zerologctx. It is built as a Go plugin (`-buildmode=plugin`) and loaded by
// golangci-lint v1's linters-settings.custom mechanism.
package main

import (
	"golang.org/x/tools/go/analysis"

	"github.com/tolmachov/zerologctx"
)

// GetAnalyzers returns the analyzers exposed by this plugin. It is the
// entry point consumed by golangci-lint's plugin loader and must keep this
// exact name and signature.
func GetAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		zerologctx.Analyzer,
	}
}
