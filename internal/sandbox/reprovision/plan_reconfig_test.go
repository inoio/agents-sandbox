package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
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
				VMHomeDir:          {Named: "agents-sandbox-home-proj-vol"},
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
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "tmpfs mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "1G"},
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "workspace quota mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{WorkspaceQuota: "32G"},
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "workspace quota unset ignores quota",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     false,
		},
		{
			name:     "disk mismatch (explicit)",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{DiskSize: "16G"},
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     true,
		},
		{
			name:     "disk unset ignores disk",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "agents-sandbox-home-proj-vol",
			want:     false,
		},
		{
			name:     "home volume mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			homeVol:  "agents-sandbox-home-proj-new",
			want:     true,
		},
		{
			name:     "no change",
			cfg:      mkConfig(4, 4096, 16384, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "2G", DiskSize: "16G"},
			homeVol:  "agents-sandbox-home-proj-vol",
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

func TestWorkspaceQuotaChange(t *testing.T) {
	vol := func(quotaMiB uint32) map[string]msbSdk.MountConfig {
		return map[string]msbSdk.MountConfig{workspaceMountPath: {QuotaMiB: quotaMiB}}
	}
	tests := []struct {
		name    string
		volumes map[string]msbSdk.MountConfig
		spec    string
		want    bool
	}{
		{name: "missing volume", volumes: nil, spec: "32G", want: false},
		{name: "invalid spec", volumes: vol(1024), spec: "not-a-size", want: false},
		{
			name:    "equal quota",
			volumes: vol(options.DefaultWorkspaceQuotaMiB),
			spec:    FormatSizeSpec(options.DefaultWorkspaceQuotaMiB, ""),
			want:    false,
		},
		{name: "different quota", volumes: vol(1024), spec: "32G", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &msbSdk.SandboxConfig{Volumes: tc.volumes}
			if _, changed := workspaceQuotaChange(
				cfg,
				options.RunOptions{WorkspaceQuota: tc.spec},
			); changed != tc.want {
				t.Errorf("workspaceQuotaChange() changed = %v, want %v", changed, tc.want)
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
