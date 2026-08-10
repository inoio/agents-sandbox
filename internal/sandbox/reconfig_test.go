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
	plan := &reconfigDecision{restartDaemons: true}
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

func TestPlanReconfigEnvChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := planReconfig(cfg, "a", RunOptions{}, true, false, false) // envChanged=true
	if !d.recreate {
		t.Error("expected recreate on env change (env cannot be applied live)")
	}
	if d.restartDaemons {
		t.Error("unexpected daemon restart for env change (folded into recreate)")
	}
}

func TestPlanReconfigOpenCodeConfigChangeRestartsDaemon(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := planReconfig(cfg, "a", RunOptions{}, false, false, true) // opencodeConfigChanged=true
	if !d.restartDaemons {
		t.Error("expected restartDaemons on opencode config change")
	}
	if d.recreate {
		t.Error("expected NO VM recreate for opencode-config-only change")
	}
}

func TestPlanReconfigSecretsChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := planReconfig(cfg, "a", RunOptions{}, false, true, false) // secretsChanged=true
	if !d.recreate {
		t.Error("expected recreate on secrets change (secrets cannot be applied live)")
	}
	if d.restartDaemons {
		t.Error("unexpected daemon restart for secrets change (folded into recreate)")
	}
}

func TestPlanReconfigEnvWithRecreateNoRestartFlag(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old", CPUs: 4, MemoryMiB: 4096, Volumes: map[string]msbSdk.MountConfig{
		"/tmp": {SizeMiB: 2048},
	}}
	// image mismatch triggers recreate, env change would add restartDaemons
	d := planReconfig(cfg, "new:tag", RunOptions{TmpSize: "4G"}, true, false, false)
	if !d.recreate {
		t.Error("expected recreate on image mismatch")
	}
	if d.restartDaemons {
		t.Error("expected restartDaemons=false when recreate is set (folded into recreate)")
	}
}

func TestPlanReconfigMemoryClampToMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", MemoryMiB: 4096, MaxMemoryMiB: 8192}
	d := planReconfig(cfg, "a", RunOptions{Memory: "16G"}, false, false, false)
	if d.resources == nil {
		t.Fatal("expected resources set for memory change")
	}
	if d.resources.MemoryMiB != 8192 {
		t.Errorf("expected memory clamped to 8192, got %d", d.resources.MemoryMiB)
	}
}

func TestResolveReconfigPromptBRestart(t *testing.T) {
	plan := &reconfigDecision{restartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "r", nil }
	applyRecreate, applyRestart, err := resolveReconfig(context.Background(), ui, plan, 1, plan.changes)
	if err != nil {
		t.Fatalf("restart should not error: %v", err)
	}
	if applyRecreate {
		t.Error("restart must not apply recreate")
	}
	if !applyRestart {
		t.Error("expected applyRestart=true for restart selection")
	}
}
