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
}
