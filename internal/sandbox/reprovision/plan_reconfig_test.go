package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
)

func TestPlanReconfigDecidesRecreate(t *testing.T) {
	mkConfig := func(cpus uint8, mem uint32, diskMiB uint32, tmpMiB uint32) *msbSdk.SandboxConfig {
		var rootDisk *msbSdk.RootDiskConfig
		if diskMiB > 0 {
			d := msbSdk.RootDisk.Managed(diskMiB)
			rootDisk = &d
		}
		return &msbSdk.SandboxConfig{
			CPUs:      cpus,
			MemoryMiB: mem,
			RootDisk:  rootDisk,
			Image:     "image-a",
			Volumes: map[string]msbSdk.MountConfig{
				tmpMountPath:       {SizeMiB: tmpMiB},
				workspaceMountPath: {QuotaMiB: options.DefaultWorkspaceQuotaMiB},
				VMHomeDir:          {Named: "opencode-sandbox-home-proj-vol"},
			},
		}
	}

	cases := []struct {
		name     string
		cfg      *msbSdk.SandboxConfig
		imageRef string
		opts     options.RunOptions
		homeVol  string
		want     bool
	}{
		{
			name:     "image mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-b",
			opts:     options.RunOptions{},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "tmpfs mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "1G"},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "workspace quota mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{WorkspaceQuota: "32G"},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "workspace quota unset ignores quota",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     false,
		},
		{
			name:     "disk mismatch (explicit)",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{DiskSize: "16G"},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "disk unset ignores disk",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     false,
		},
		{
			name:     "home volume mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "opencode-sandbox-home-proj-new",
			want:     true,
		},
		{
			name:     "no change",
			cfg:      mkConfig(4, 4096, 16384, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "2G", DiskSize: "16G"},
			homeVol:  "opencode-sandbox-home-proj-vol",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanReconfig(tc.cfg, tc.imageRef, tc.opts, ChangeFlags{}, tc.homeVol).Recreate
			if got != tc.want {
				t.Errorf("PlanReconfig().Recreate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanReconfigServeHostPortReuse(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image: "img",
		PortBindings: []msbSdk.PortBinding{
			{Bind: "127.0.0.1", HostPort: 4096, GuestPort: 4096, Protocol: msbSdk.PortProtocolTCP},
		},
	}
	opts := options.RunOptions{ServeOnly: true}
	plan := PlanReconfig(cfg, "img", opts, ChangeFlags{}, "")
	if plan.Recreate {
		t.Error("expected no recreate when reusing the existing binding")
	}
	if plan.ServeHostPort != 4096 {
		t.Errorf("ServeHostPort = %d, want 4096", plan.ServeHostPort)
	}
}
