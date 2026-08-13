package reprovision

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestPlanReconfigRecreateOnTmpMismatch(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{tmpMountPath: {SizeMiB: 2048}},
	}
	d := PlanReconfig(cfg, "img:tag", options.RunOptions{TmpSize: "4G"},
		false, false, false)
	if !d.Recreate {
		t.Error("expected recreate on /tmp size mismatch")
	}
	if len(d.Changes) != 1 || d.Changes[0].Label != "/tmp tmpfs size" {
		t.Errorf("expected one /tmp change, got %+v", d.Changes)
	}
}

func TestPlanReconfigRecreateOnImageMismatch(t *testing.T) {
	d := PlanReconfig(nil, "new:tag", options.RunOptions{}, false, false, false)
	if d.Recreate {
		t.Error("image comparison requires cfg.Image; see resolver, planner.cpp nil-cfg safe")
	}
	cfg := &msbSdk.SandboxConfig{Image: "old:tag"}
	d = PlanReconfig(cfg, "new:tag", options.RunOptions{}, false, false, false)
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
}

func TestPlanReconfigStagesClampedCpus(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8}
	d := PlanReconfig(cfg, "img", options.RunOptions{CPUs: 16}, false, false, false)
	if d.Resources == nil || d.Resources.CPUs != 8 {
		t.Fatalf("expected resources clamped CPUs=8, got %+v", d.Resources)
	}
}

func TestPlanReconfigNoActionsWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, "img", options.RunOptions{CPUs: 4, Memory: "4G"},
		false, false, false)
	if d.Recreate || d.RestartDaemons || d.Resources != nil {
		t.Errorf("expected no actions, got %+v", d)
	}
}

func TestResolveReconfigSilentWhenAlone(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 0, nil)
	if err != nil || !applyRecreate {
		t.Errorf("alone recreate should apply silently, got %v %v", applyRecreate, err)
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("expected at least an informational line when recreating silently")
	}
}

func TestResolveReconfigPromptAKeep(t *testing.T) {
	plan := &Plan{Recreate: true, Changes: []Change{{Label: "root disk size"}}}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "q", nil // quit
	}
	applyRecreate, _, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err == nil {
		t.Error("expected quit to abort (return an error)")
	}
	if applyRecreate {
		t.Error("quit must not apply recreate")
	}
}

func TestResolveReconfigPromptBKeepReturnsNoRestart(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "k", nil }
	_, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Fatalf("keep should not error: %v", err)
	}
	if applyRestart {
		t.Error("keep must not restart daemons")
	}
}

func TestPlanReconfigEnvChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, "a", options.RunOptions{}, true, false, false) // envChanged=true
	if !d.Recreate {
		t.Error("expected recreate on env change (env cannot be applied live)")
	}
	if d.RestartDaemons {
		t.Error("unexpected daemon restart for env change (folded into recreate)")
	}
}

func TestPlanReconfigOpenCodeConfigChangeRestartsDaemon(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, "a", options.RunOptions{}, false, false, true) // opencodeConfigChanged=true
	if !d.RestartDaemons {
		t.Error("expected restartDaemons on opencode config change")
	}
	if d.Recreate {
		t.Error("expected NO VM recreate for opencode-config-only change")
	}
}

func TestPlanReconfigSecretsChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, "a", options.RunOptions{}, false, true, false) // secretsChanged=true
	if !d.Recreate {
		t.Error("expected recreate on secrets change (secrets cannot be applied live)")
	}
	if d.RestartDaemons {
		t.Error("unexpected daemon restart for secrets change (folded into recreate)")
	}
}

func TestPlanReconfigEnvWithRecreateNoRestartFlag(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "old", CPUs: 4, MemoryMiB: 4096, Volumes: map[string]msbSdk.MountConfig{
		tmpMountPath: {SizeMiB: 2048},
	}}
	// image mismatch triggers recreate, env change would add restartDaemons
	d := PlanReconfig(cfg, "new:tag", options.RunOptions{TmpSize: "4G"}, true, false, false)
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
	if d.RestartDaemons {
		t.Error("expected restartDaemons=false when recreate is set (folded into recreate)")
	}
}

func TestPlanReconfigMemoryClampToMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "a", MemoryMiB: 4096, MaxMemoryMiB: 8192}
	d := PlanReconfig(cfg, "a", options.RunOptions{Memory: "16G"}, false, false, false)
	if d.Resources == nil {
		t.Fatal("expected resources set for memory change")
	}
	if d.Resources.MemoryMiB != 8192 {
		t.Errorf("expected memory clamped to 8192, got %d", d.Resources.MemoryMiB)
	}
}

func TestResolveReconfigPromptBQuit(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "q", nil }
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err == nil {
		t.Error("expected quit to abort (return an error)")
	}
	if applyRecreate || applyRestart {
		t.Error("quit must not apply recreate or restart daemons")
	}
}

func TestResolveReconfigPromptBRestart(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "r", nil }
	applyRecreate, applyRestart, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
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
