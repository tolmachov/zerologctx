// Tests for the zerologctx analyzer
package zerologctx

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer runs the analyzer against test cases in the testdata directory.
// It verifies that the analyzer correctly identifies missing Ctx() calls
// in zerolog event chains.
func TestAnalyzer(t *testing.T) {
	// Get the test data directory
	testdata := analysistest.TestData()

	// Run the analyzer on the test package
	analysistest.Run(t, testdata, Analyzer, "testpkg")
}

// TestAnalyzerHelpers tests the helper functions used by the analyzer.
func TestAnalyzerHelpers(t *testing.T) {
	// Note: isContextType now requires *analysis.Pass and types.Type,
	// so it's tested through the integration tests in TestAnalyzer instead.

	// Test the isNoLintComment function
	t.Run("isNoLintComment", func(t *testing.T) {
		testCases := []struct {
			comment  string
			linter   string
			expected bool
		}{
			{"//nolint:zerologctx", "zerologctx", true},
			{"// nolint:zerologctx", "zerologctx", true},
			{"//nolint: zerologctx", "zerologctx", true},
			{"// nolint: zerologctx", "zerologctx", true},
			{"//   nolint: zerologctx", "zerologctx", true},
			{"//nolint:linter1,zerologctx,linter2", "zerologctx", true},
			{"//nolint:linter1, zerologctx, linter2", "zerologctx", true},
			{"//   nolint: another1,zerologctx,another2", "zerologctx", true},
			{"//   nolint: another1, zerologctx, another2", "zerologctx", true},
			{"//nolint:otherlinter", "zerologctx", false},
			{"//nolint:linter1,linter2", "zerologctx", false},
			{"// just a comment", "zerologctx", false},
			{"//nolint", "zerologctx", false},  // missing colon
			{"//nolint:", "zerologctx", false}, // empty linter list
		}

		for _, tc := range testCases {
			t.Run(tc.comment, func(t *testing.T) {
				got := isNoLintComment(tc.comment, tc.linter)
				if got != tc.expected {
					t.Errorf("isNoLintComment(%q, %q) = %v, want %v", tc.comment, tc.linter, got, tc.expected)
				}
			})
		}
	})
}
