package vm

import (
	"context"
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

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

	handle, found, err := findProjectVM(ctx, client, k)
	if err != nil {
		return err
	}
	if !found {
		ui.Infof("no project VM found: %s", name)
		return nil
	}
	name = handle.Name()

	pastTense := action + "ed"
	if action == "stop" {
		pastTense = "stopped"
	}

	status := handle.Status()
	if msb.IsSandboxInactive(status) {
		ui.Infof("project VM already %s: %s", pastTense, name)
		if !remove {
			return nil
		}
		if dryRun {
			ui.Infof("dry-run: Would remove persisted state of %s project VM: %s", pastTense, name)
			return nil
		}
		removePersistedState(ctx, handle, ui, name)
		return nil
	}
	if !msb.IsSandboxActive(status) {
		return fmt.Errorf("cannot %s sandbox %q with unexpected status %q", action, name, status)
	}
	if status == msbSdk.SandboxStatusPaused {
		return fmt.Errorf("cannot %s paused sandbox %q: resume is not supported", action, name)
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
		removePersistedState(ctx, handle, ui, name)
	}
	return nil
}

// removePersistedState deletes a sandbox's persisted state, warning rather than
// failing the command when the removal does not succeed.
func removePersistedState(ctx context.Context, handle msb.SandboxHandle, ui termio.UI, name string) {
	if err := handle.Remove(ctx); err != nil {
		ui.Warnf("failed to remove sandbox state: %v", err)
		return
	}
	ui.Verbosef("persisted state removed: %s", name)
}

// StopProjectVM gracefully stops the project VM for the current directory and
// agent. If remove is true, it also removes the VM's persisted state; an
// inactive VM is not stopped, only removed.
func StopProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI, agentName string) error {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return err
	}
	adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), !dryRun, ui)
	if err != nil {
		return err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, k, "stop", "Stopping", msb.Get(),
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
}

// KillProjectVM force-kills the project VM for the current directory and agent.
// If remove is true, it also removes the VM's persisted state; an inactive VM
// is not killed, only removed.
func KillProjectVM(ctx context.Context, remove, dryRun bool, ui termio.UI, agentName string) error {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return err
	}
	adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), !dryRun, ui)
	if err != nil {
		return err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}
	return stopOrKillProjectVM(ctx, remove, dryRun, ui, k, "kill", "Force-killing", msb.Get(),
		func(h msb.SandboxHandle, c context.Context) error { return h.Kill(c) })
}
