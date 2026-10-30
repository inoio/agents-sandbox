package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// isProjectVMName reports whether name is a project VM under either scheme: a
// prefixed legacy name, or a name that parses to a key and is not a task
// sandbox.
func isProjectVMName(name string) bool {
	if naming.IsLegacyProjectSandbox(name) {
		return true
	}
	info := naming.ParseVMName(name)
	return info.Slug != "" && !info.Task
}

// RemoveSandbox removes a project VM by name. It removes only agents-sandbox
// project VMs, refuses a VM that is not stopped unless force is set, confirms
// before mutating, and leaves the persisted project state intact so the next run
// reuses the recorded home volume. A forced removal stops the VM after the
// confirmation succeeds, immediately before removing.
func RemoveSandbox(ctx context.Context, name string, force bool, ui termio.UI) error {
	if !isProjectVMName(name) {
		return fmt.Errorf("%q is not an agents-sandbox project VM", name)
	}
	handle, err := msb.Get().GetSandbox(ctx, name)
	if err != nil {
		return fmt.Errorf("remove sandbox %q: %w", name, err)
	}
	stopped := msb.IsSandboxInactive(handle.Status())
	if !stopped && !force {
		return fmt.Errorf("sandbox %q is not stopped; stop it first or pass --force", name)
	}
	if err := termio.ConfirmAction(ui, fmt.Sprintf("Remove sandbox %q?", name)); err != nil {
		return err
	}
	if !stopped {
		if err := handle.Stop(ctx); err != nil {
			return fmt.Errorf("stop sandbox %q: %w", name, err)
		}
	}
	if err := handle.Remove(ctx); err != nil {
		return fmt.Errorf("remove sandbox %q: %w", name, err)
	}
	return nil
}
