package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// TestProjectVMLockSeamResolution pins the seam contract: a nil
// options.Seams.ProjectVMLock uses the production default (acquireProjectVMLock),
// while a set field is used as-is.
func TestProjectVMLockSeamResolution(t *testing.T) {
	orig := acquireProjectVMLock
	t.Cleanup(func() { acquireProjectVMLock = orig })

	defaultErr := errors.New("production default lock")
	acquireProjectVMLock = func(state.Key) (func(), error) { return nil, defaultErr }
	if _, err := projectVMLock(options.Seams{})(testVMKey()); !errors.Is(err, defaultErr) {
		t.Errorf("nil seam lock error = %v, want the production default %v", err, defaultErr)
	}

	seamErr := errors.New("injected lock")
	seam := func(state.Key) (func(), error) { return nil, seamErr }
	if _, err := projectVMLock(options.Seams{ProjectVMLock: seam})(testVMKey()); !errors.Is(err, seamErr) {
		t.Errorf("set seam lock error = %v, want %v", err, seamErr)
	}
}

// TestDaemonHealthSeamResolution pins the seam contract: a nil
// options.Seams.DaemonHealth uses the production default (daemonHealthFunc),
// while a set field is used as-is.
func TestDaemonHealthSeamResolution(t *testing.T) {
	orig := daemonHealthFunc
	t.Cleanup(func() { daemonHealthFunc = orig })
	daemonHealthFunc = func(context.Context, msb.Sandbox, string) (string, int, error) {
		return "default", 1, nil
	}
	if out, code, _ := daemonHealth(
		options.Seams{},
	)(
		context.Background(),
		nil,
		"probe",
	); out != "default" ||
		code != 1 {
		t.Errorf("nil seam probe = (%q, %d), want the production default (default, 1)", out, code)
	}

	seam := func(context.Context, msb.Sandbox, string) (string, int, error) { return "seam", 2, nil }
	if out, code, _ := daemonHealth(
		options.Seams{DaemonHealth: seam},
	)(
		context.Background(),
		nil,
		"probe",
	); out != "seam" ||
		code != 2 {
		t.Errorf("set seam probe = (%q, %d), want (seam, 2)", out, code)
	}
}
