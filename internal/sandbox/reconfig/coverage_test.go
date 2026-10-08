package reconfig

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestConfigChangeListLabelOnly verifies the default formatting branch when Old
// or New is empty.
func TestConfigChangeListLabelOnly(t *testing.T) {
	got := configChangeList([]Change{
		{Label: "environment variables"},
		{Label: "size", Old: "1", New: "2"},
	})
	if got == "" {
		t.Fatal("expected a non-empty change list")
	}
}

// TestResolveReconfigPromptAError verifies that a prompt selection error is
// swallowed without applying.
func TestResolveReconfigPromptAError(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "", errors.New("select boom")
	}
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Errorf("expected select error to be swallowed, got %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate when the select errored")
	}
}

// TestResolveReconfigPromptADefer verifies that selecting keep defers the change.
func TestResolveReconfigPromptADefer(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return keepKey, nil }
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate when keep is selected")
	}
}

// TestResolveReconfigPromptBError verifies that a PromptB select error is
// swallowed without applying a restart.
func TestResolveReconfigPromptBError(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "", errors.New("select boom")
	}
	_, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Errorf("expected select error to be swallowed, got %v", err)
	}
	if applyRestart {
		t.Error("expected no restart when the select errored")
	}
}

// TestResolveReconfigRestartAlone verifies the silent restart path with no
// other clients attached.
func TestResolveReconfigRestartAlone(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 0, plan.Changes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate {
		t.Error("expected no recreate")
	}
	if !applyRestart {
		t.Error("expected restart when alone")
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("expected an informational line when restarting silently")
	}
}

// TestResolveReconfigNoAction verifies the no-op branch when the plan has no
// recreate or restart requirement.
func TestResolveReconfigNoAction(t *testing.T) {
	ui := &termio.Mock{}
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, &Plan{}, 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyRecreate || applyRestart {
		t.Errorf("expected no actions, got recreate=%v restart=%v", applyRecreate, applyRestart)
	}
}

// TestDiskMiBOr0NilRootDisk verifies diskMiBOr0 returns 0 when RootDisk is nil.
func TestDiskMiBOr0NilRootDisk(t *testing.T) {
	if got := diskMiBOr0(&msbSdk.SandboxConfig{}); got != 0 {
		t.Errorf("expected 0 for nil RootDisk, got %d", got)
	}
}

// TestPlanReconfigDiskChangeWithNilRootDisk verifies the disk recreate trigger
// when RootDisk is nil (diskMiBOr0 nil branch) and exercises the disk change
// formatting.
func TestPlanReconfigDiskChangeWithNilRootDisk(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "img"}
	plan := PlanReconfig(cfg, "img", options.RunOptions{DiskSize: "8G"}, ChangeFlags{}, "")
	if !plan.Recreate {
		t.Fatal("expected recreate on disk size change with nil RootDisk")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "root disk size" && c.Old == "0M" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected root disk size change with Old=0M, got %+v", plan.Changes)
	}
}

// TestPortBindingsEqualLengthMismatch verifies the length-mismatch branch.
func TestPortBindingsEqualLengthMismatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost"}}
	if portBindingsEqual(a, nil) {
		t.Error("expected false for differing lengths")
	}
}

// TestPortBindingsEqualElementMismatch verifies the element-mismatch branch for
// equal-length bindings.
func TestPortBindingsEqualElementMismatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	b := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 2}}
	if portBindingsEqual(a, b) {
		t.Error("expected false for differing elements")
	}
}

// TestPortBindingsEqualMatch verifies the equal branch.
func TestPortBindingsEqualMatch(t *testing.T) {
	a := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	b := []msbSdk.PortBinding{{Bind: "localhost", HostPort: 1}}
	if !portBindingsEqual(a, b) {
		t.Error("expected true for matching bindings")
	}
}
