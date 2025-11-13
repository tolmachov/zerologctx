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
			{"foo.Context", false},                  // BUG #3 FIX: Now correctly rejects non-context types
			{"db.Context", false},                   // Should reject database context
			{"custom.Context", false},               // Should reject custom context types
			{"somepackage.context.Context", true},   // Vendored or full path context
			{"github.com/pkg/context.Context", true}, // Module path context
			{"string", false},
			{"Context", false},
			{"contextual", false},
			{"interface{context.Context}", true}, // Interface containing context
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
