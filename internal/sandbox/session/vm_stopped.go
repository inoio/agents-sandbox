package session

import (
	"context"
	"errors"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
)

// errVMStoppedDuringAttach replaces the runtime's "connection closed" when the
// attach ended because the project VM itself went away, so the user learns
// what happened and what to do instead of reading it as a launcher bug.
var errVMStoppedDuringAttach = errors.New(
	"the project VM stopped while the session was attached (killed, stopped, or crashed); " +
		"run agents-sandbox again to restart it",
)

// projectVMStatus looks the project VM up; a seam so a test decides the VM's
// state.
var projectVMStatus = defaultProjectVMStatus

// defaultProjectVMStatus looks the project VM up and reports its status, or
// found false when it no longer exists.
func defaultProjectVMStatus(ctx context.Context, k state.Key) (msb.SandboxStatus, bool, error) {
	handle, found, err := sandbox.FindProjectVM(ctx, msb.Get(), k)
	if err != nil || !found {
		return "", found, err
	}
	return handle.Status(), true, nil
}

// vmStoppedDuringAttach reports whether a failed attach ended because the
// project VM is no longer running: it is gone, stopped, crashed, or shutting
// down. A VM still running, or a lookup that fails, keeps the attach's own
// error.
func vmStoppedDuringAttach(ctx context.Context, k state.Key) bool {
	status, found, err := projectVMStatus(ctx, k)
	if err != nil {
		return false
	}
	return !found || status != msb.SandboxStatusRunning
}
