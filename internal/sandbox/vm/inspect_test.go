package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// TestInspectSandboxWrapsLookupError pins the sandbox-lookup wrap: a GetSandbox
// failure preserves its cause and names the requested VM.
func TestInspectSandboxWrapsLookupError(t *testing.T) {
	sentinel := errors.New("lookup boom")
	mock := &msbtest.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		return nil, sentinel
	}}
	msbtest.WithMsbMock(t, mock)

	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err == nil {
		t.Fatal("expected a lookup failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the lookup cause", err)
	}
	if !strings.Contains(err.Error(), "agents-sandbox-vm-abc") {
		t.Errorf("error = %v, want it to name the requested VM", err)
	}
}

func TestInspectSandboxPopulatesDetail(t *testing.T) {
	cfg := &msb.SandboxConfig{
		Image:        "agents-sandbox/runner-proj:latest",
		MemoryMiB:    4096,
		CPUs:         4,
		MaxMemoryMiB: 8192,
		MaxCPUs:      8,
		Labels:       map[string]string{"project": "proj"},
	}
	mock := &msbtest.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msbtest.MockSandboxHandle{
			Name_:        "agents-sandbox-vm-abc",
			ID_:          "sbx-123",
			Status_:      msb.SandboxStatusRunning,
			BackendKind_: msb.BackendLocal,
			CreatedAt_:   time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
			UpdatedAt_:   time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
			Cfg:          cfg,
			Mounts: map[string]msb.MountConfig{
				"/workspace": msb.Mount.Bind("/host/workspace", msb.MountOptions{}),
				"/home/dev":  msb.Mount.Named("agents-sandbox-home-proj", msb.MountOptions{}),
			},
		},
	}
	msbtest.WithMsbMock(t, mock)

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
	mock := &msbtest.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msbtest.MockSandboxHandle{Name_: "agents-sandbox-vm-abc", ConfigErr: configErr},
	}
	msbtest.WithMsbMock(t, mock)
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
	mock := &msbtest.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msbtest.MockSandboxHandle{
			Name_:     "agents-sandbox-vm-abc",
			Cfg:       &msb.SandboxConfig{Image: "img:tag"},
			MountsErr: mountsErr,
		},
	}
	msbtest.WithMsbMock(t, mock)
	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-abc")
	if err == nil || !strings.Contains(err.Error(), "read mounts") {
		t.Fatalf("InspectSandbox() error = %v, want read mounts error", err)
	}
	if !errors.Is(err, mountsErr) {
		t.Errorf("error = %v, want it to wrap the mounts cause", err)
	}
}

func TestInspectSandboxMissing(t *testing.T) {
	msbtest.WithMsbMock(t, &msbtest.MockMsbClient{})
	_, err := InspectSandbox(context.Background(), "agents-sandbox-vm-missing")
	if err == nil || !strings.Contains(err.Error(), "inspect sandbox") {
		t.Fatalf("InspectSandbox() error = %v, want inspect sandbox error", err)
	}
}

// TestMountDetailsSortsGuestPaths pins the end-to-end ordering across a map
// whose iteration order is randomized.
func TestMountDetailsSortsGuestPaths(t *testing.T) {
	volumes := map[string]msb.MountConfig{
		"/z": {},
		"/a": {},
		"/m": {},
		"/b": {},
		"/y": {},
	}
	got := mountDetails(volumes)
	want := []string{"/a", "/b", "/m", "/y", "/z"}
	if len(got) != len(want) {
		t.Fatalf("mountDetails returned %d mounts, want %d", len(got), len(want))
	}
	for i, path := range want {
		if got[i].GuestPath != path {
			t.Fatalf("mountDetails[%d].GuestPath = %q, want %q (got %+v)", i, got[i].GuestPath, path, got)
		}
	}
}

// TestLessMountByGuestPathIsStrict pins the comparator as a strict weak ordering:
// it orders distinct paths by value and never orders a path before itself, so a
// "<=" boundary or a constant-true body is observable.
func TestLessMountByGuestPathIsStrict(t *testing.T) {
	a := MountDetail{GuestPath: "/a"}
	b := MountDetail{GuestPath: "/b"}

	if !lessMountByGuestPath(a, b) {
		t.Error("lessMountByGuestPath(/a, /b) = false, want true")
	}
	if lessMountByGuestPath(b, a) {
		t.Error("lessMountByGuestPath(/b, /a) = true, want false")
	}
	if lessMountByGuestPath(a, a) {
		t.Error("lessMountByGuestPath(/a, /a) = true, want false (irreflexive)")
	}
}

func TestMountKindString(t *testing.T) {
	cases := map[msb.MountKind]string{
		msb.MountKindBind:   "bind",
		msb.MountKindNamed:  "named",
		msb.MountKindTmpfs:  "tmpfs",
		msb.MountKindDisk:   "disk",
		msb.MountKindOwned:  "owned",
		msb.MountKind(0):    "unknown",
		msb.MountKind(0xff): "unknown",
	}
	for kind, want := range cases {
		if got := mountKindString(kind); got != want {
			t.Errorf("mountKindString(%d) = %q, want %q", kind, got, want)
		}
	}
}

func TestMountSource(t *testing.T) {
	cases := []struct {
		name  string
		mount msb.MountConfig
		want  string
	}{
		{"bind", msb.Mount.Bind("/host/workspace", msb.MountOptions{}), "/host/workspace"},
		{"named", msb.Mount.Named("vol-name", msb.MountOptions{}), "vol-name"},
		{"disk", msb.MountConfig{Kind: msb.MountKindDisk, Disk: "/host/data.img"}, "/host/data.img"},
		{
			"owned",
			msb.MountConfig{Kind: msb.MountKindOwned, Owned: string(msb.VolumeKindDir)},
			string(msb.VolumeKindDir),
		},
		{"tmpfs", msb.Mount.Tmpfs(msb.TmpfsOptions{}), ""},
		{"unknown", msb.MountConfig{}, ""},
	}
	for _, tc := range cases {
		if got := mountSource(tc.mount); got != tc.want {
			t.Errorf("mountSource(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
