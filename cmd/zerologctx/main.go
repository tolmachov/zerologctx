// Command zerologctx is a static analysis tool that checks
// that zerolog logging events include context via the Ctx() method.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/tolmachov/zerologctx"
)

func main() {
	// singlechecker runs a single analyzer as a command line tool
	singlechecker.Main(zerologctx.Analyzer)
}
