package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
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
				tmpMountPath: {SizeMiB: tmpMiB},
			},
		}
	}

	cases := []struct {
		name     string
		cfg      *msbSdk.SandboxConfig
		imageRef string
		opts     options.RunOptions
		want     bool
	}{
		{
			name:     "image mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-b",
			opts:     options.RunOptions{},
			want:     true,
		},
		{
			name:     "tmpfs mismatch",
			cfg:      mkConfig(4, 4096, 0, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "1G"},
			want:     true,
		},
		{
			name:     "disk mismatch (explicit)",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{DiskSize: "16G"},
			want:     true,
		},
		{
			name:     "disk unset ignores disk",
			cfg:      mkConfig(4, 4096, 8192, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{},
			want:     false,
		},
		{
			name:     "no change",
			cfg:      mkConfig(4, 4096, 16384, 2048),
			imageRef: "image-a",
			opts:     options.RunOptions{TmpSize: "2G", DiskSize: "16G"},
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanReconfig(tc.cfg, tc.imageRef, tc.opts, false, false, false).Recreate
			if got != tc.want {
				t.Errorf("PlanReconfig().Recreate = %v, want %v", got, tc.want)
			}
		})
	}
}
