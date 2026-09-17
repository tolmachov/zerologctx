// Command zerologctx reports zerolog output operations that are not proven to
// carry a context.Context. Run it over packages the way go vet is run:
//
//	zerologctx ./...
//
// See package github.com/tolmachov/zerologctx for the full contract.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/tolmachov/zerologctx"
)

func main() {
	singlechecker.Main(zerologctx.Analyzer)
}
