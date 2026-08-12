package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
)

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := options.ServeOnlyBindings()
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
			plan := PlanReconfig(cfg, "img", options.RunOptions{ServeOnly: tt.serveOnly}, false, false, false)
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

func TestDesiredPublishBindingsDelegatesToOptions(t *testing.T) {
	optsBindings := options.ServeOnlyBindings()
	planBindings := desiredPublishBindings(true)
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
	got := desiredPublishBindings(false)
	if got != nil {
		t.Errorf("expected nil when serveOnly=false, got %+v", got)
	}
}
