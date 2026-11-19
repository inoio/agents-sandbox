package fakecmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDispatchServesTheRegisteredHandler runs the fake the way a script would:
// it execs the PATH symlink and checks the handler's streams and exit status are
// served. This is the cross-process contract the TestMain hook depends on.
func TestDispatchServesTheRegisteredHandler(t *testing.T) {
	binDir := BinDir(t, "faketool")
	command := exec.Command(filepath.Join(binDir, "faketool"), "alpha", "beta")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("Run() error = %v, want exit status 3", err)
	}
	if stdout.String() != "out:alpha,beta" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "out:alpha,beta")
	}
	if stderr.String() != "err\n" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "err\n")
	}
}

// TestDispatchReturnsMinusOneWhenNotAFake keeps the normal test run untouched:
// a process whose name is not registered is not a fake.
func TestDispatchReturnsMinusOneWhenNotAFake(t *testing.T) {
	original := os.Args
	os.Args = []string{"/usr/bin/some-other-tool"}
	t.Cleanup(func() { os.Args = original })

	if got := Dispatch(); got != -1 {
		t.Errorf("Dispatch() = %d, want -1", got)
	}
}
