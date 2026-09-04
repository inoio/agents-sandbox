package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// stopOrKillProjectVM is the shared implementation for StopProjectVM and KillProjectVM.
// action is "stop" or "kill", actionVerb is used in user-facing messages.
func stopOrKillProjectVM(
	ctx context.Context,
	remove bool,
	dryRun bool,
	ui termio.UI,
	k state.Key,
	action, actionVerb string,
	client msb.Client,
	stopFn func(msb.SandboxHandle, context.Context) error,
) error {
	name := projectVMName(k)

	handle, err := client.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsNotFound(err) {
			ui.Infof("no project VM found: %s", name)
			return nil
		}
		return fmt.Errorf("get sandbox %q: %w", name, err)
	}

	pastTense := action + "ed"
	if action == "stop" {
		pastTense = "stopped"
	}

	if kind, err := msb.GetVMStatus(handle.Status()); err == nil && kind == msb.VMStatusStopped {
		ui.Infof("project VM already %s: %s", pastTense, name)
		return nil
	}

	if dryRun {
		actionWord := "Would stop"
		if action == "kill" {
			actionWord = "Would kill"
		}
		if remove {
			ui.Infof("dry-run: %s project VM: %s (also would remove persisted state)", actionWord, name)
		} else {
			ui.Infof("dry-run: %s project VM: %s", actionWord, name)
		}
		return nil
	}

	spin := ui.Spinnerf("%s project VM", actionVerb)
	if err := stopFn(handle, ctx); err != nil {
		spin.StopError(err)
		return fmt.Errorf("%s sandbox %q: %w", action, name, err)
	}
	spin.Stop()
	ui.Infof("%s project VM: %s", pastTense, name)

	if remove {
		if err := handle.Remove(ctx); err != nil {
			ui.Warnf("failed to remove sandbox state: %v", err)
		} else {
			ui.Verbosef("persisted state removed: %s", name)
		}
	}
	return nil
}

// StopProjectVM gracefully stops the project VM for the current directory and
// agent. If remove is true, it also removes the VM's persisted state after
// stopping.
func StopProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI, agentName string) error {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	k := state.Key{Slug: git.ProjectSlug(), Agent: a.Name()}
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, k, "stop", "Stopping", msb.Get(),
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
}

// KillProjectVM force-kills the project VM for the current directory and agent.
// If remove is true, it also removes the VM's persisted state after killing.
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI, agentName string) error {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	k := state.Key{Slug: git.ProjectSlug(), Agent: a.Name()}
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, k, "kill", "Force-killing", msb.Get(),
		func(h msb.SandboxHandle, c context.Context) error { return h.Kill(c) })
}
