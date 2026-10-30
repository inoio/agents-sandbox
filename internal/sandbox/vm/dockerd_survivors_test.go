package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// existsErrorFS is a SandboxFS whose Exists fails while every other operation is
// delegated to the embedded test FS, so a test can exercise guestStorageDriver's
// inspect-failure branch independently of Read.
type existsErrorFS struct {
	*msb.TestFS

	err error
}

func (f existsErrorFS) Exists(context.Context, string) (bool, error) {
	return false, f.err
}

// TestGuestStorageDriverExistsErrorWrapsCause pins the inspect-failure branch:
// an FS.Exists error aborts with the daemon.json path named and its cause
// preserved, rather than being treated as "absent".
func TestGuestStorageDriverExistsErrorWrapsCause(t *testing.T) {
	sentinel := errors.New("exists boom")
	sb := &msb.MockSandbox{FSValue_: existsErrorFS{TestFS: msb.NewTestFS(nil, nil), err: sentinel}}

	driver, err := guestStorageDriver(context.Background(), sb)
	if err == nil {
		t.Fatalf("expected an Exists failure to abort, got driver %q", driver)
	}
	if driver != "" {
		t.Errorf("driver = %q, want empty on error", driver)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Exists cause", err)
	}
}

// TestStartDockerdIfPresentBinaryCheckErrorWrapsCause pins the binary-probe
// failure: a Shell error checking for dockerd aborts with its cause preserved,
// rather than being read as "not present".
func TestStartDockerdIfPresentBinaryCheckErrorWrapsCause(t *testing.T) {
	ui := termio.NewTestMock(t)
	sentinel := errors.New("shell boom")
	sb := msb.NewMockSandbox(msb.SandboxOpts{ShellErr: sentinel})

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected a binary-check Shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Shell cause", err)
	}
}
