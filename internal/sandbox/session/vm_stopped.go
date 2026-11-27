package session

import (
	"context"
	"errors"
	"time"

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

// defaultProjectVMStatus reports the project VM's status, or the empty status
// when it no longer exists.
func defaultProjectVMStatus(ctx context.Context, k state.Key) (msb.SandboxStatus, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, vmStatusLookupTimeout)
	defer cancel()
	// A failed lookup reports found false too, so one branch carries both.
	handle, found, err := sandbox.FindProjectVM(lookupCtx, msb.Get(), k)
	if !found {
		return "", err
	}
	return handle.Status(), nil
}

// vmStatusLookupTimeout bounds the lookup, so a hung runtime cannot hang the
// exit of a session that already ended.
//
// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT,ARITHMETIC_BASE reason="the bound only has to outlast a responsive runtime's lookup; its exact length is no contract"
const vmStatusLookupTimeout = 5 * time.Second

// vmStoppedDuringAttach reports whether a failed attach ended because the
// project VM is no longer running: it is gone (no status), inactive (stopped,
// crashed, or never started), or shutting down. A running, paused, or starting
// VM, or a lookup that fails, keeps the attach's own error: "run again" would
// not help there.
func vmStoppedDuringAttach(ctx context.Context, k state.Key) bool {
	status, err := projectVMStatus(ctx, k)
	if err != nil {
		return false
	}
	return status == "" || msb.IsSandboxInactive(status) || status == msb.SandboxStatusDraining
}
