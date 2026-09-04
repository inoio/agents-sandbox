package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := options.ServeOnlyBindings(options.ServeOnlyBasePort)
	tests := []struct {
		name         string
		serveOnly    bool
		cfgPorts     []msbSdk.PortBinding
		wantRecreate bool
		wantLabel    string
	}{
		{"serve on, vm not published", true, nil, true, "published port(s)"},
		{"serve on, vm already published", true, desired, false, ""},
		{"serve off, vm published", false, desired, true, "published port(s)"},
		{"serve off, vm not published", false, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg *msbSdk.SandboxConfig
			if tt.cfgPorts == nil {
				// A running VM always has a non-nil SandboxConfig; PortBindings
				// is simply nil/empty when nothing is published.
				cfg = &msbSdk.SandboxConfig{PortBindings: nil}
			} else {
				cfg = &msbSdk.SandboxConfig{PortBindings: tt.cfgPorts}
			}
			plan := PlanReconfig(
				cfg,
				"img",
				options.RunOptions{ServeOnly: tt.serveOnly},
				ChangeFlags{},
				"",
			)
			if plan.Recreate != tt.wantRecreate {
				t.Errorf("Recreate = %v, want %v", plan.Recreate, tt.wantRecreate)
			}
			if tt.wantLabel != "" {
				found := false
				for _, c := range plan.Changes {
					if c.Label == tt.wantLabel {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected change label %q, got %+v", tt.wantLabel, plan.Changes)
				}
			}
		})
	}
}

func TestPlanReconfigTriggersRecreateOnImageChange(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{Image: "agents-sandbox/runner-proj:oldhash"}

	planSame := PlanReconfig(
		cfg,
		"agents-sandbox/runner-proj:oldhash",
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if planSame.Recreate {
		t.Error("expected no recreate when image reference is unchanged")
	}

	planNew := PlanReconfig(
		cfg,
		"agents-sandbox/runner-proj:newhash",
		options.RunOptions{},
		ChangeFlags{},
		"",
	)
	if !planNew.Recreate {
		t.Error("expected recreate when image reference changes after a rebuild")
	}
	if len(planNew.Changes) == 0 || planNew.Changes[0].Label != "image" {
		t.Errorf("expected an 'image' change label, got %+v", planNew.Changes)
	}
}

func TestDesiredPublishBindingsDelegatesToOptions(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4097, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}}
	optsBindings := options.ServeOnlyBindings(4097)
	planBindings := desiredPublishBindings(true, cfg)
	if len(planBindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(planBindings))
	}
	if planBindings[0].Bind != optsBindings[0].Bind {
		t.Errorf("Bind = %q, want %q", planBindings[0].Bind, optsBindings[0].Bind)
	}
	if planBindings[0].HostPort != optsBindings[0].HostPort {
		t.Errorf("HostPort = %d, want %d", planBindings[0].HostPort, optsBindings[0].HostPort)
	}
	if planBindings[0].GuestPort != optsBindings[0].GuestPort {
		t.Errorf("GuestPort = %d, want %d", planBindings[0].GuestPort, optsBindings[0].GuestPort)
	}
	if planBindings[0].Protocol != optsBindings[0].Protocol {
		t.Errorf("Protocol = %v, want %v", planBindings[0].Protocol, optsBindings[0].Protocol)
	}
}

func TestDesiredPublishBindingsNilWhenNotServeOnly(t *testing.T) {
	got := desiredPublishBindings(false, nil)
	if got != nil {
		t.Errorf("expected nil when serveOnly=false, got %+v", got)
	}
}

func TestPlanReconfigNetworkChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{Network: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when network policy changes")
	}
}

func TestPlanReconfigNetworkSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatal("expected no Recreate when network policy is unchanged")
	}
}

func TestPlanReconfigHomeVolumeChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: "img",
		Volumes: map[string]msbSdk.MountConfig{
			VMHomeDir: {Named: "agents-sandbox-home-proj-old"},
		},
	}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-new")
	if !plan.Recreate {
		t.Fatal("expected Recreate when the desired home volume differs from the mounted one")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == "home volume" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'home volume' change label, got %+v", plan.Changes)
	}
}

func TestPlanReconfigHomeVolumeSameNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: "img",
		Volumes: map[string]msbSdk.MountConfig{
			VMHomeDir: {Named: "agents-sandbox-home-proj-vol"},
		},
	}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate when the mounted home volume matches the desired one")
	}
}

func TestPlanReconfigNilCfgHomeVolumeSafe(t *testing.T) {
	plan := PlanReconfig(nil, "img", options.RunOptions{}, ChangeFlags{},
		"agents-sandbox-home-proj-vol")
	if plan.Recreate {
		t.Fatal("expected no Recreate for a nil config (fresh VM creation)")
	}
}

func TestPlanReconfigMountsChangeTriggersRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	plan := PlanReconfig(cfg, "img", options.RunOptions{}, ChangeFlags{Mounts: true}, "")
	if !plan.Recreate {
		t.Fatal("expected Recreate when host bind mounts change")
	}
	found := false
	for _, c := range plan.Changes {
		if c.Label == changeLabelBindMounts {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a %q change label, got %+v", changeLabelBindMounts, plan.Changes)
	}
}

// TestPlanReconfigMountsUnchangedNoRecreate guards the regression where the
// plan was derived from cfg.Volumes, which the SDK never populates when
// reading an existing VM back: every run would then rebuild the VM.
func TestPlanReconfigMountsUnchangedNoRecreate(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	opts := options.RunOptions{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Fatalf("expected no Recreate for unchanged mounts, got %+v", plan.Changes)
	}
}
