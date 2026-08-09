package sandbox

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestPlanReconfigRecreateOnTmpMismatch(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Volumes: map[string]msbSdk.MountConfig{"/tmp": {SizeMiB: 2048}},
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
