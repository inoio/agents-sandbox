package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestInspectSandboxPopulatesDetail(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{
		Image:        "agents-sandbox/runner-proj:latest",
		MemoryMiB:    4096,
		CPUs:         4,
		MaxMemoryMiB: 8192,
		MaxCPUs:      8,
		Labels:       map[string]string{"project": "proj"},
	}
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:        "agents-sandbox-vm-abc",
			ID_:          "sbx-123",
			Status_:      msbSdk.SandboxStatusRunning,
			BackendKind_: msbSdk.BackendLocal,
			CreatedAt_:   time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
			UpdatedAt_:   time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
			Cfg:          cfg,
			Mounts: map[string]msbSdk.MountConfig{
				"/workspace": msbSdk.Mount.Bind("/host/workspace", msbSdk.MountOptions{}),
				"/home/dev":  msbSdk.Mount.Named("agents-sandbox-home-proj", msbSdk.MountOptions{}),
			},
		},
	}
	msb.WithMsbMock(t, mock)

	got, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err != nil {
		t.Fatalf("InspectSandbox() error = %v", err)
	}
	if got.Name != "agents-sandbox-vm-abc" || got.ID != "sbx-123" || got.Status != "running" ||
		got.Image != "agents-sandbox/runner-proj:latest" || got.Backend != "local" {
		t.Errorf("InspectSandbox() identity = %+v", got)
	}
	if got.CreatedAt != "2026-08-17 10:30:00" || got.UpdatedAt != "2026-08-17 11:00:00" {
		t.Errorf("InspectSandbox() times = %+v", got)
	}
	if got.MemoryMiB != 4096 || got.CPUs != 4 || got.MaxMemoryMiB != 8192 || got.MaxCPUs != 8 {
		t.Errorf("InspectSandbox() resources = %+v", got)
	}
	if got.Labels["project"] != "proj" {
		t.Errorf("InspectSandbox() labels = %v", got.Labels)
	}
	if len(got.Mounts) != 2 {
		t.Fatalf("InspectSandbox() mounts = %+v, want 2", got.Mounts)
	}
	if got.Mounts[0].GuestPath != "/home/dev" || got.Mounts[0].Kind != "named" ||
		got.Mounts[0].Source != "agents-sandbox-home-proj" {
		t.Errorf("InspectSandbox() mount[0] = %+v", got.Mounts[0])
	}
	if got.Mounts[1].GuestPath != "/workspace" || got.Mounts[1].Kind != "bind" ||
		got.Mounts[1].Source != "/host/workspace" {
		t.Errorf("InspectSandbox() mount[1] = %+v", got.Mounts[1])
	}
}

func TestInspectSandboxConfigError(t *testing.T) {
	configErr := errors.New("config boom")
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-abc", ConfigErr: configErr},
	}
	msb.WithMsbMock(t, mock)
	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("InspectSandbox() error = %v, want read config error", err)
	}
	if !errors.Is(err, configErr) {
		t.Errorf("error = %v, want it to wrap the config cause", err)
	}
}

func TestInspectSandboxMountsError(t *testing.T) {
	mountsErr := errors.New("mounts boom")
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:     "agents-sandbox-vm-abc",
			Cfg:       &msbSdk.SandboxConfig{Image: "img:tag"},
			MountsErr: mountsErr,
		},
	}
	msb.WithMsbMock(t, mock)
	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err == nil || !strings.Contains(err.Error(), "read mounts") {
		t.Fatalf("InspectSandbox() error = %v, want read mounts error", err)
	}
	if !errors.Is(err, mountsErr) {
		t.Errorf("error = %v, want it to wrap the mounts cause", err)
	}
}

func TestInspectSandboxMissing(t *testing.T) {
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-missing")
	if err == nil || !strings.Contains(err.Error(), "inspect sandbox") {
		t.Fatalf("InspectSandbox() error = %v, want inspect sandbox error", err)
	}
}

func TestMountSource(t *testing.T) {
	cases := []struct {
		name  string
		mount msbSdk.MountConfig
		want  string
	}{
		{"bind", msbSdk.Mount.Bind("/host/workspace", msbSdk.MountOptions{}), "/host/workspace"},
		{"named", msbSdk.Mount.Named("vol-name", msbSdk.MountOptions{}), "vol-name"},
		{"disk", msbSdk.Mount.Disk("/host/data.img", msbSdk.DiskOptions{}), "/host/data.img"},
		{"owned", msbSdk.Mount.Owned(msbSdk.OwnedVolumeOptions{}), string(msbSdk.VolumeKindDir)},
		{"tmpfs", msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{}), ""},
		{"unknown", msbSdk.MountConfig{}, ""},
	}
	for _, tc := range cases {
		if got := mountSource(tc.mount); got != tc.want {
			t.Errorf("mountSource(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMountKindString(t *testing.T) {
	cases := map[msbSdk.MountKind]string{
		msbSdk.MountKindBind:   "bind",
		msbSdk.MountKindNamed:  "named",
		msbSdk.MountKindTmpfs:  "tmpfs",
		msbSdk.MountKindDisk:   "disk",
		msbSdk.MountKindOwned:  "owned",
		msbSdk.MountKind(0):    "unknown",
		msbSdk.MountKind(0xff): "unknown",
	}
	for kind, want := range cases {
		if got := mountKindString(kind); got != want {
			t.Errorf("mountKindString(%d) = %q, want %q", kind, got, want)
		}
	}
}
