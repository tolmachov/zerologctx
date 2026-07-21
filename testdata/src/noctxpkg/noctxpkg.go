// Package noctxpkg imports neither zerolog nor (transitively) "context".
// Regression fixture: the analyzer must silently skip such packages instead
// of returning a "could not locate context.Context" error, which would fail
// every zerolog-free package in a monorepo.
package noctxpkg

import "strings"

// Upper exists so the package imports something real but context-free.
func Upper(s string) string {
	return strings.ToUpper(s)
}
