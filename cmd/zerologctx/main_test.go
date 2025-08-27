package main

import (
	"os"
	"testing"
)

func Test_Main(t *testing.T) {
	// Save original os.Args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Test that main function can be called without panicking
	// We set Args to simulate running with -h flag to avoid actual analysis
	os.Args = []string{"zerologctx", "-h"}

	// Capture the fact that main() will call os.Exit
	// by recovering from a panic if one occurs
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("main() panicked: %v", r)
		}
	}()

	// Note: main() calls singlechecker.Main which will call os.Exit
	// In a real test environment, we can't easily test this without
	// mocking or using a subprocess. This test at least ensures
	// the code compiles and the imports are correct.

	// For a minimal test, we just verify that the main function exists
	// and can be referenced. The actual testing of the analyzer
	// is done in the parent package tests.
	_ = main
}
