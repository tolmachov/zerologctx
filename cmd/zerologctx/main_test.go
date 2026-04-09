package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMainHelp builds the binary and runs it with `-h` in a subprocess to
// verify the CLI entry point links and starts. singlechecker.Main always
// calls os.Exit, so it cannot be invoked in-process from a test.
func TestMainHelp(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available in PATH")
	}

	tmp, err := os.MkdirTemp("", "zerologctx-cli-")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(tmp)

	bin := tmp + "/zerologctx"
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "-h")
	out, err := cmd.CombinedOutput()
	// `-h` makes singlechecker print usage and exit non-zero (status 2).
	// A crash (segfault, nil-deref, missing dependency) would produce a
	// different exit code and must be surfaced explicitly.
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("binary -h failed to run (not an exit error): %v\noutput: %s", err, out)
		}
		if exitErr.ExitCode() != 2 {
			t.Fatalf("binary -h exited with unexpected status %d\noutput: %s", exitErr.ExitCode(), out)
		}
	}
	if !strings.Contains(string(out), "zerologctx") {
		t.Errorf("binary -h output did not mention zerologctx: %s", out)
	}
}
