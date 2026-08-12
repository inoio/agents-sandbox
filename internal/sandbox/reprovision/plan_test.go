package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
)

func TestPlanReconfigServeOnly(t *testing.T) {
	desired := []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
	}
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
