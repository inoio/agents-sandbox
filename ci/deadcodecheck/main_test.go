package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

// TestMain installs the fail-fast seam defaults. This package reaches no seam
// itself, but the test-isolation contract requires every test package to
// install the families its import graph allows.
func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}

// TestCheckIgnoresFindingsUnderTest asserts the gate never reports an
// unreachable function under test/: non-test .go files there are test code, so
// nothing in a main package is meant to reach them.
func TestCheckIgnoresFindingsUnderTest(t *testing.T) {
	t.Parallel()
	findings := []Finding{
		{File: "test/msb/driver/spec.go", Function: "HarnessSpec", Line: 10},
		{File: "internal/sandbox/dead.go", Function: "Dead", Line: 20},
	}

	diagnosis := Check(findings, nil, false)

	if len(diagnosis.Unmarked) != 1 {
		t.Fatalf("unmarked findings = %d, want 1: %+v", len(diagnosis.Unmarked), diagnosis.Unmarked)
	}
	if got := diagnosis.Unmarked[0].File; got != "internal/sandbox/dead.go" {
		t.Errorf("reported file = %q, want the finding under internal/", got)
	}
}

// TestRunSkipsMarkersUnderTest asserts a //deadcode:ignore marker recorded from
// a test/ path is neither required nor stale: run() drops those paths before it
// scans them, so a stale-looking marker there produces no diagnostic.
func TestRunSkipsMarkersUnderTest(t *testing.T) {
	dir := t.TempDir()
	writeReport(t, filepath.Join(dir, "no-tags.json"))

	work := t.TempDir()
	t.Chdir(work)
	if err := os.MkdirAll(filepath.Join("test", "msb", "driver"), 0o750); err != nil {
		t.Fatalf("create test source dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join("test", "msb", "driver", "spec.go"),
		[]byte("package driver\n\n//deadcode:ignore reason=\"test seam\"\nfunc Dead() {}\n"),
		0o600,
	); err != nil {
		t.Fatalf("write test source: %v", err)
	}

	stdin := strings.NewReader("test/msb/driver/spec.go\ntest/msb/driver/absent.go\n")
	var stdout, stderr strings.Builder

	status := run([]string{"-reports", dir, "-scope", "all"}, stdin, &stdout, &stderr)

	if status != exitClean {
		t.Fatalf(
			"exit = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			status,
			exitClean,
			stdout.String(),
			stderr.String(),
		)
	}
}

// writeReport writes one empty deadcode -json payload at path so run() sees a
// report directory it can trust.
func writeReport(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
}
