// Benchmarks for the zerologctx analyzer
package zerologctx

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// BenchmarkAnalyzer benchmarks the analyzer on the test cases.
// This helps identify performance bottlenecks and track performance over time.
func BenchmarkAnalyzer(b *testing.B) {
	testdata := analysistest.TestData()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		analysistest.Run(b, testdata, Analyzer, "testpkg")
	}
}

// BenchmarkIsNoLintComment benchmarks the nolint comment parsing function.
func BenchmarkIsNoLintComment(b *testing.B) {
	testCases := []struct {
		name     string
		comment  string
		linter   string
		expected bool
	}{
		{"simple", "//nolint:zerologctx", "zerologctx", true},
		{"with_spaces", "// nolint: zerologctx", "zerologctx", true},
		{"multiple", "//nolint:linter1,zerologctx,linter2", "zerologctx", true},
		{"not_found", "//nolint:otherlinter", "zerologctx", false},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = isNoLintComment(tc.comment, tc.linter)
			}
		})
	}
}

// BenchmarkImplementsContextInterface benchmarks the context interface checking.
func BenchmarkImplementsContextInterface(b *testing.B) {
	// This would require creating actual types.Type instances
	// Skipped for now as it requires more complex setup
	b.Skip("Requires type system setup")
}
