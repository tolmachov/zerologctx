package main

import (
	"golang.org/x/tools/go/analysis"

	"github.com/tolmachov/zerologctx"
)

// golangci-lint: linter
func GetAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		zerologctx.Analyzer,
	}
}
