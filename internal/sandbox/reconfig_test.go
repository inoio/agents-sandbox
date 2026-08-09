package sandbox

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestPlanReconfigRecreateOnTmpMismatch(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{tmpMountPath: {SizeMiB: 2048}},
	}
	d := planReconfig(cfg, "img:tag", RunOptions{TmpSize: "4G"},
		false, false, false)
	if !d.recreate {
		t.Error("expected recreate on /tmp size mismatch")
	}
	if len(d.changes) != 1 || d.changes[0].label != "/tmp tmpfs size" {
		t.Errorf("expected one /tmp change, got %+v", d.changes)
	}
}

func TestPlanReconfigRecreateOnImageMismatch(t *testing.T) {
	d := planReconfig(nil, "new:tag", RunOptions{}, false, false, false)
	if d.recreate {
		t.Error("image comparison requires cfg.Image; see resolver, planner.cpp nil-cfg safe")
	}
	cfg := &msbSdk.SandboxConfig{Image: "old:tag"}
	d = planReconfig(cfg, "new:tag", RunOptions{}, false, false, false)
	if !d.recreate {
		t.Error("expected recreate on image mismatch")
	}
}

func TestPlanReconfigStagesClampedCpus(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8}
	d := planReconfig(cfg, "img", RunOptions{CPUs: 16}, false, false, false)
	if d.resources == nil || d.resources.CPUs != 8 {
		t.Fatalf("expected resources clamped CPUs=8, got %+v", d.resources)
	}
}

func TestPlanReconfigNoActionsWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096}
	d := planReconfig(cfg, "img", RunOptions{CPUs: 4, Memory: "4G"},
		false, false, false)
	if d.recreate || d.restartDaemons || d.resources != nil {
		t.Errorf("expected no actions, got %+v", d)
	}
}

func TestResolveReconfigSilentWhenAlone(t *testing.T) {
	plan := &reconfigDecision{recreate: true}
	ui := &termio.Mock{}
	applyRecreate, _, err := resolveReconfig(context.Background(), ui, plan, 0, nil)
	if err != nil || !applyRecreate {
		t.Errorf("alone recreate should apply silently, got %v %v", applyRecreate, err)
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("expected at least an informational line when recreating silently")
	}
}

func TestResolveReconfigPromptAKeep(t *testing.T) {
	plan := &reconfigDecision{recreate: true, changes: []reconfigChange{{label: "root disk size"}}}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "q", nil // quit
	}
	applyRecreate, _, err := resolveReconfig(context.Background(), ui, plan, 1, plan.changes)
	if err == nil {
		t.Error("expected quit to abort (return an error)")
	}
	if applyRecreate {
		t.Error("quit must not apply recreate")
	}
}

func TestResolveReconfigPromptBKeepReturnsNoRestart(t *testing.T) {
	plan := &reconfigDecision{restartDaemons: true, restartDockerd: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "k", nil }
	_, applyRestart, err := resolveReconfig(context.Background(), ui, plan, 1, plan.changes)
	if err != nil {
		t.Fatalf("keep should not error: %v", err)
	}
	if applyRestart {
		t.Error("keep must not restart daemons")
	}
}
