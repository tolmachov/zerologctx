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
	// Test the isContextType function
	t.Run("isContextType", func(t *testing.T) {
		testCases := []struct {
			typeName string
			expected bool
		}{
			{"context.Context", true},
			{"*context.Context", true},
			{"foo.Context", true},
			{"somepackage.context.Context", true},
			{"string", false},
			{"Context", false},
			{"contextual", false},
		}

		for _, tc := range testCases {
			t.Run(tc.typeName, func(t *testing.T) {
				got := isContextType(tc.typeName)
				if got != tc.expected {
					t.Errorf("isContextType(%q) = %v, want %v", tc.typeName, got, tc.expected)
				}
			})
		}
	})

	// Test the hasNoLintDirective function
	t.Run("hasNoLintDirective", func(t *testing.T) {
		// This test requires a complex setup to create an analysis.Pass and AST nodes
		// Therefore, we rely on the integration test in TestAnalyzer which verifies
		// that nolint directives work properly using the test cases in testdata/src/testpkg/examples.go
		// The TestAnalyzer function will automatically verify that lines with nolint directives
		// do not produce diagnostics, while lines without them do
	})
}
