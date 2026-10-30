package reconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestPlanReconfigRecreateOnTmpMismatch(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{mounts.TmpMountPath: {SizeMiB: 2048}},
	}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{TmpSize: "4G"},
		ChangeFlags{}, "")
	if !d.Recreate {
		t.Error("expected recreate on /tmp size mismatch")
	}
	if len(d.Changes) != 1 || d.Changes[0].Label != "/tmp tmpfs size" {
		t.Errorf("expected one /tmp change, got %+v", d.Changes)
	}
}

func TestPlanReconfigRecreateOnImageMismatch(t *testing.T) {
	d := PlanReconfig(nil, planImage(), options.RunOptions{}, ChangeFlags{}, "")
	if d.Recreate {
		t.Error("image comparison requires cfg.Image; see resolver, planner.cpp nil-cfg safe")
	}
	cfg := &msbSdk.SandboxConfig{Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57")}
	d = PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{}, "")
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
}

func TestPlanReconfigRecreateOnImageDigestChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.DockerImageRef(planSlug, "opencode")}

	d := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Image: true}, "")
	if !d.Recreate {
		t.Fatal("expected recreate when the image digest changes")
	}
	if d.RestartDaemons {
		t.Error("expected image changes to recreate the VM instead of restarting daemons")
	}
	if len(d.Changes) != 1 || d.Changes[0].Label != changeLabelImage {
		t.Errorf("expected one image change, got %+v", d.Changes)
	}
}

func TestPlanReconfigStagesClampedCpus(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{CPUs: 16}, ChangeFlags{}, "")
	if d.Resources == nil || d.Resources.CPUs != 8 {
		t.Fatalf("expected resources clamped CPUs=8, got %+v", d.Resources)
	}
}

func TestPlanReconfigNoActionsWhenUnchanged(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{CPUs: 4, Memory: "4G"},
		ChangeFlags{}, "")
	if d.Recreate || d.RestartDaemons || d.Resources != nil {
		t.Errorf("expected no actions, got %+v", d)
	}
}

func TestResolveReconfigSilentWhenAlone(t *testing.T) {
	plan := &Plan{Recreate: true}
	ui := &termio.Mock{}
	actions, err := ResolveReconfig(context.Background(), ui, plan, 0, nil)
	if err != nil || !actions.Recreate {
		t.Errorf("alone recreate should apply silently, got %v %v", actions.Recreate, err)
	}
	if len(ui.InfoCalls) == 0 {
		t.Error("expected at least an informational line when recreating silently")
	}
}

func TestResolveReconfigVerboseReportsRebuildReasons(t *testing.T) {
	plan := &Plan{
		Recreate: true,
		Changes:  []Change{{Label: "image"}, {Label: "secrets"}},
	}
	ui := &termio.Mock{}
	if _, err := ResolveReconfig(context.Background(), ui, plan, 0, plan.Changes); err != nil {
		t.Fatalf("ResolveReconfig: %v", err)
	}
	joined := strings.Join(ui.VerboseCalls, "\n")
	if !strings.Contains(joined, "rebuild reasons:") ||
		!strings.Contains(joined, "image") || !strings.Contains(joined, "secrets") {
		t.Errorf("verbose calls = %v, want the rebuild reasons", ui.VerboseCalls)
	}
}

func TestResolveReconfigVerboseReportsRestartReasons(t *testing.T) {
	plan := &Plan{
		RestartDaemons: true,
		Changes:        []Change{{Label: "agent config"}},
	}
	ui := &termio.Mock{}
	if _, err := ResolveReconfig(context.Background(), ui, plan, 0, plan.Changes); err != nil {
		t.Fatalf("ResolveReconfig: %v", err)
	}
	joined := strings.Join(ui.VerboseCalls, "\n")
	if !strings.Contains(joined, "restart reasons: agent config") {
		t.Errorf("verbose calls = %v, want the restart reasons", ui.VerboseCalls)
	}
}

func TestResolveReconfigPromptAKeep(t *testing.T) {
	plan := &Plan{Recreate: true, Changes: []Change{{Label: "root disk size"}}}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return "q", nil // quit
	}
	actions, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err == nil {
		t.Error("expected quit to abort (return an error)")
	}
	if actions.Recreate {
		t.Error("quit must not apply recreate")
	}
	if actions.Restart {
		t.Error("quit must not apply restart")
	}
}

func TestResolveReconfigPromptBKeepReturnsNoRestart(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "k", nil }
	actions, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Fatalf("keep should not error: %v", err)
	}
	if actions.Restart {
		t.Error("keep must not restart daemons")
	}
}

func TestPlanReconfigEnvChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(planSlug, "opencode"), CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Env: true}, "") // envChanged=true
	if !d.Recreate {
		t.Error("expected recreate on env change (env cannot be applied live)")
	}
	if d.RestartDaemons {
		t.Error("unexpected daemon restart for env change (folded into recreate)")
	}
}

func TestPlanReconfigAgentConfigChangeRestartsDaemon(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(planSlug, "opencode"), CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(
		cfg,
		planImage(),
		options.RunOptions{},
		ChangeFlags{AgentConfig: true},
		"",
	) // agentConfigChanged=true
	if !d.RestartDaemons {
		t.Error("expected restartDaemons on agent config change")
	}
	if d.Recreate {
		t.Error("expected NO VM recreate for agent-config-only change")
	}
}

func TestPlanReconfigSecretsChangeRebuildsVM(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: naming.RunnerImageRef(planSlug, "opencode"), CPUs: 4, MemoryMiB: 4096}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{}, ChangeFlags{Secrets: true}, "") // secretsChanged=true
	if !d.Recreate {
		t.Error("expected recreate on secrets change (secrets cannot be applied live)")
	}
	if d.RestartDaemons {
		t.Error("unexpected daemon restart for secrets change (folded into recreate)")
	}
}

func TestPlanReconfigEnvWithRecreateNoRestartFlag(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: naming.ContentImageRef(planSlug, "opencode", "4b8d0e6f2a1c3b57"), CPUs: 4, MemoryMiB: 4096,
		Volumes: map[string]msbSdk.MountConfig{
			mounts.TmpMountPath: {SizeMiB: 2048},
		},
	}
	// image mismatch triggers recreate, env change would add restartDaemons
	d := PlanReconfig(cfg, planImage(), options.RunOptions{TmpSize: "4G"}, ChangeFlags{Env: true}, "")
	if !d.Recreate {
		t.Error("expected recreate on image mismatch")
	}
	if d.RestartDaemons {
		t.Error("expected restartDaemons=false when recreate is set (folded into recreate)")
	}
}

func TestPlanReconfigMemoryClampToMax(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image:        naming.RunnerImageRef(planSlug, "opencode"),
		MemoryMiB:    4096,
		MaxMemoryMiB: 8192,
	}
	d := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "16G"}, ChangeFlags{}, "")
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
	actions, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err == nil {
		t.Error("expected quit to abort (return an error)")
	}
	if actions.Recreate || actions.Restart {
		t.Error("quit must not apply recreate or restart daemons")
	}
}

func TestResolveReconfigPromptBRestart(t *testing.T) {
	plan := &Plan{RestartDaemons: true}
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return "r", nil }
	actions, err := ResolveReconfig(context.Background(), ui, plan, 1, plan.Changes)
	if err != nil {
		t.Fatalf("restart should not error: %v", err)
	}
	if actions.Recreate {
		t.Error("restart must not apply recreate")
	}
	if !actions.Restart {
		t.Error("expected Restart=true for restart selection")
	}
}

func TestResolveReconfigAloneRecreateNoRestart(t *testing.T) {
	actions, err := ResolveReconfig(
		context.Background(), &termio.Mock{}, &Plan{Recreate: true}, 0, nil)
	if err != nil || !actions.Recreate || actions.Restart {
		t.Errorf("alone recreate: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}

func TestResolveReconfigAloneRestartNoRecreate(t *testing.T) {
	actions, err := ResolveReconfig(
		context.Background(), &termio.Mock{}, &Plan{RestartDaemons: true}, 0, nil)
	if err != nil || actions.Recreate || !actions.Restart {
		t.Errorf("alone restart: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}

func TestResolveReconfigNoActionReturnsNothing(t *testing.T) {
	actions, err := ResolveReconfig(
		context.Background(), &termio.Mock{}, &Plan{}, 0, nil)
	if err != nil || actions.Recreate || actions.Restart {
		t.Errorf("no-action: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}

// TestResolveReconfigPromptAKeepDefers pins the keep path: a non-quit answer to
// the recreate prompt returns without applying anything.
func TestResolveReconfigPromptAKeepDefers(t *testing.T) {
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) { return keepKey, nil }
	actions, err := ResolveReconfig(
		context.Background(), ui, &Plan{Recreate: true}, 1, nil)
	if err != nil || actions.Recreate || actions.Restart {
		t.Errorf("keep: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}

// TestResolveReconfigPromptAErrorDefers pins that a prompt error is swallowed
// even when the prompt also reports the quit key.
func TestResolveReconfigPromptAErrorDefers(t *testing.T) {
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return quitKey, errors.New("select failed")
	}
	actions, err := ResolveReconfig(
		context.Background(), ui, &Plan{Recreate: true}, 1, nil)
	if err != nil || actions.Recreate || actions.Restart {
		t.Errorf("prompt A error: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}

func TestResolveReconfigPromptBErrorDefers(t *testing.T) {
	ui := &termio.Mock{}
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		return quitKey, errors.New("select failed")
	}
	actions, err := ResolveReconfig(
		context.Background(), ui, &Plan{RestartDaemons: true}, 1, nil)
	if err != nil || actions.Recreate || actions.Restart {
		t.Errorf("prompt B error: recreate=%v restart=%v err=%v", actions.Recreate, actions.Restart, err)
	}
}
