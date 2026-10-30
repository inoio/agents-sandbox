package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestVolumeInspect(t *testing.T) {
	quota := uint32(10240)
	capacity := uint64(20 * 1024 * 1024 * 1024)
	format := "raw"
	fstype := "ext4"
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{
			Name_:          "agents-sandbox-home-proj-opencode-20260817T104236",
			Path_:          "/home/dev/.local/share/agents-sandbox/msb/volumes/agents-sandbox-home-proj",
			Kind_:          msbSdk.VolumeKindDisk,
			CreatedAt_:     time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
			QuotaMiB_:      &quota,
			UsedBytes_:     1024 * 1024 * 1024,
			CapacityBytes_: &capacity,
			DiskFormat_:    &format,
			DiskFstype_:    &fstype,
			Labels_:        map[string]string{"org.agents-sandbox.project": "proj"},
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdInspect, "agents-sandbox-home-proj-opencode-20260817T104236")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume inspect: %v", err)
	}
	for _, want := range []string{
		"name: agents-sandbox-home-proj-opencode-20260817T104236",
		"kind: disk",
		"path: /home/dev/.local/share/agents-sandbox/msb/volumes/agents-sandbox-home-proj",
		"quota: 10 GiB",
		"used: 1 GiB",
		"capacity: 20 GiB",
		"disk format: raw",
		"disk fstype: ext4",
		"labels: org.agents-sandbox.project=proj",
		"created: 2026-08-17 10:42:36",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestVolumeInspectFormatJSON(t *testing.T) {
	quota := uint32(10240)
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{
			Name_:      "agents-sandbox-home-proj",
			Path_:      "/msb/volumes/agents-sandbox-home-proj",
			Kind_:      msbSdk.VolumeKindDir,
			QuotaMiB_:  &quota,
			UsedBytes_: 1024,
			CreatedAt_: time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdInspect, "--format", "json", "agents-sandbox-home-proj")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume inspect --format json: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		Quota    string `json:"quota"`
		Used     string `json:"used"`
		Capacity string `json:"capacity"`
		Created  string `json:"created"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Name != "agents-sandbox-home-proj" || got.Kind != "dir" || got.Quota != "10 GiB" ||
		got.Used != "1 KiB" || got.Capacity != "-" || got.Created != "2026-08-17 10:42:36" {
		t.Errorf("unexpected JSON: %+v", got)
	}
}

func TestVolumeInspectQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Volumes = []sandboxmsb.VolumeHandle{
		&sandboxmsb.MockVolumeHandle{Name_: "agents-sandbox-home-proj", Kind_: msbSdk.VolumeKindDir},
	}
	cmd, ui := setupCommandFixtures(t, cmdVolume, cmdInspect, "--quiet", "agents-sandbox-home-proj")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("volume inspect --quiet: %v", err)
	}
	if !containsNormalized(ui.PrintCalls, "agents-sandbox-home-proj") {
		t.Errorf("PrintCalls missing identifier; got: %v", ui.PrintCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet inspect should not write a block, got: %v", ui.OutCalls)
	}
}

func TestJoinLabels(t *testing.T) {
	if got := joinLabels(nil); got != "-" {
		t.Errorf("joinLabels(nil) = %q, want %q", got, "-")
	}
	if got := joinLabels(map[string]string{}); got != "-" {
		t.Errorf("joinLabels(empty) = %q, want %q", got, "-")
	}
	got := joinLabels(map[string]string{"b": "2", "a": "1"})
	if got != "a=1, b=2" {
		t.Errorf("joinLabels = %q, want sorted %q", got, "a=1, b=2")
	}
}

func TestVolumeInspectRequiredArgs(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdVolume, cmdInspect)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an argument error for volume inspect with no name")
	}
}

func TestVolumeInspectConflict(t *testing.T) {
	runListCmdTest(t, []string{cmdVolume, cmdInspect, "--quiet", "--format", "json", "vol"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "mutually exclusive")
}

func TestVolumeInspectFormatUnknown(t *testing.T) {
	runListCmdTest(t, []string{cmdVolume, cmdInspect, "--format", "yaml", "vol"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "unsupported format")
}

func TestVolumeInspectMissing(t *testing.T) {
	runListCmdTest(t, []string{cmdVolume, cmdInspect, "agents-sandbox-home-missing"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "inspect volume")
}

func TestImageInspect(t *testing.T) {
	size := int64(2 * 1024 * 1024 * 1024)
	layerSize := int64(1024)
	mock := &sandboxmsb.MockMsbClient{
		ImageInspectDetailFn: func(_ context.Context, _ string) (*sandboxmsb.ImageDetail, error) {
			return &sandboxmsb.ImageDetail{
				Reference:      "agents-sandbox/runner-proj:latest",
				ManifestDigest: "sha256:abc123",
				Architecture:   "arm64",
				OS:             "linux",
				SizeBytes:      &size,
				CreatedAt:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
				Config: msbSdk.ImageConfig{
					Env:        []string{"A=1", "B=2"},
					Entrypoint: []string{"/entry"},
					WorkingDir: "/workspace",
					User:       "dev",
					StopSignal: "SIGTERM",
					Labels:     map[string]string{"k": "v"},
				},
				Layers: []msbSdk.ImageLayer{
					{
						Position:            0,
						DiffID:              "sha256:layer0",
						BlobDigest:          "sha256:blob0",
						MediaType:           "application/vnd.oci.image.layer.v1.tar+gzip",
						CompressedSizeBytes: &layerSize,
					},
				},
			}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdInspect, "agents-sandbox/runner-proj:latest")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image inspect: %v", err)
	}
	for _, want := range []string{
		"reference: agents-sandbox/runner-proj:latest",
		"digest: sha256:abc123",
		"architecture: arm64",
		"os: linux",
		"size: 2 GiB",
		"created: 2026-08-17 10:42:36",
		"working dir: /workspace",
		"user: dev",
		"stop signal: SIGTERM",
		"env: A=1, B=2",
		"entrypoint: /entry",
		"layers (1):",
		"0 sha256:layer0 sha256:blob0 1 KiB erofs unknown application/vnd.oci.image.layer.v1.tar+gzip",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestImageInspectFormatJSON(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{
		ImageInspectDetailFn: func(_ context.Context, _ string) (*sandboxmsb.ImageDetail, error) {
			return &sandboxmsb.ImageDetail{
				Reference:      "agents-sandbox/runner-proj:latest",
				ManifestDigest: "sha256:abc123",
				Architecture:   "amd64",
				OS:             "linux",
				Config:         msbSdk.ImageConfig{Labels: map[string]string{"k": "v"}},
				Layers:         []msbSdk.ImageLayer{{Position: 2, DiffID: "sha256:l"}},
			}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdInspect, "--format", "json", "agents-sandbox/runner-proj:latest")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image inspect --format json: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got struct {
		Reference    string   `json:"reference"`
		Digest       string   `json:"digest"`
		Architecture string   `json:"architecture"`
		OS           string   `json:"os"`
		Env          []string `json:"env"`
		Layers       []struct {
			Position int    `json:"position"`
			DiffID   string `json:"diff_id"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Reference != "agents-sandbox/runner-proj:latest" || got.Digest != "sha256:abc123" ||
		got.Architecture != "amd64" || got.OS != "linux" {
		t.Errorf("unexpected identity JSON: %+v", got)
	}
	if got.Env == nil || len(got.Env) != 0 {
		t.Errorf("env should be an empty array, got %#v", got.Env)
	}
	if len(got.Layers) != 1 || got.Layers[0].Position != 2 || got.Layers[0].DiffID != "sha256:l" {
		t.Errorf("unexpected layers JSON: %+v", got.Layers)
	}
}

func TestImageInspectQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{
		ImageInspectDetailFn: func(_ context.Context, _ string) (*sandboxmsb.ImageDetail, error) {
			return &sandboxmsb.ImageDetail{Reference: "agents-sandbox/runner-proj:latest"}, nil
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdImage, cmdInspect, "--quiet", "agents-sandbox/runner-proj:latest")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image inspect --quiet: %v", err)
	}
	if !containsNormalized(ui.PrintCalls, "agents-sandbox/runner-proj:latest") {
		t.Errorf("PrintCalls missing identifier; got: %v", ui.PrintCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet inspect should not write a block, got: %v", ui.OutCalls)
	}
}

func TestImageInspectMissing(t *testing.T) {
	runListCmdTest(t, []string{cmdImage, cmdInspect, "agents-sandbox/runner-missing"},
		func(m *sandboxmsb.MockMsbClient) {
			m.ImageInspectDetailFn = func(_ context.Context, _ string) (*sandboxmsb.ImageDetail, error) {
				return nil, errBoom
			}
		}, nil, nil, true, "inspect image")
}

func TestSandboxInspect(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		&sandboxmsb.MockSandboxHandle{
			Name_:        "agents-sandbox-vm-abc",
			ID_:          "sbx-123",
			Status_:      msbSdk.SandboxStatusRunning,
			BackendKind_: msbSdk.BackendLocal,
			CreatedAt_:   time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
			UpdatedAt_:   time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
			Cfg: &msbSdk.SandboxConfig{
				Image:        "agents-sandbox/runner-proj:latest",
				MemoryMiB:    4096,
				CPUs:         4,
				MaxMemoryMiB: 8192,
				MaxCPUs:      8,
				Labels:       map[string]string{"project": "proj"},
			},
			Mounts: map[string]msbSdk.MountConfig{
				"/workspace": msbSdk.Mount.Bind("/host/workspace", msbSdk.MountOptions{Readonly: true}),
				"/home/dev":  msbSdk.Mount.Named("agents-sandbox-home-proj", msbSdk.MountOptions{}),
			},
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdSandbox, cmdInspect, "agents-sandbox-vm-abc")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox inspect: %v", err)
	}
	for _, want := range []string{
		"name: agents-sandbox-vm-abc",
		"id: sbx-123",
		"status: running",
		"image: agents-sandbox/runner-proj:latest",
		"backend: local",
		"created: 2026-08-17 10:30:00",
		"updated: 2026-08-17 11:00:00",
		"memory: 4 GiB",
		"cpus: 4",
		"max memory: 8 GiB",
		"max cpus: 8",
		"labels: project=proj",
		"mounts (2):",
		"/home/dev named agents-sandbox-home-proj",
		"/workspace bind /host/workspace  (read-only)",
	} {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
}

func TestSandboxInspectFormatJSON(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		&sandboxmsb.MockSandboxHandle{
			Name_:        "agents-sandbox-vm-abc",
			ID_:          "sbx-123",
			Status_:      msbSdk.SandboxStatusStopped,
			BackendKind_: msbSdk.BackendLocal,
			CreatedAt_:   time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
			UpdatedAt_:   time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
			Cfg: &msbSdk.SandboxConfig{
				Image:     "agents-sandbox/runner-proj:latest",
				MemoryMiB: 4096,
			},
			Mounts: map[string]msbSdk.MountConfig{
				"/workspace": msbSdk.Mount.Bind("/host/workspace", msbSdk.MountOptions{Readonly: true}),
			},
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdSandbox, cmdInspect, "--format", "json", "agents-sandbox-vm-abc")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox inspect --format json: %v", err)
	}
	joined := strings.Join(ui.OutCalls, "\n")
	var got struct {
		Name    string `json:"name"`
		ID      string `json:"id"`
		Status  string `json:"status"`
		Backend string `json:"backend"`
		Memory  string `json:"memory"`
		CPUs    int    `json:"cpus"`
		Mounts  []struct {
			GuestPath string `json:"guest_path"`
			Kind      string `json:"kind"`
			Source    string `json:"source"`
			Readonly  bool   `json:"readonly"`
		} `json:"mounts"`
	}
	if err := json.Unmarshal([]byte(joined), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v; output: %q", err, joined)
	}
	if got.Name != "agents-sandbox-vm-abc" || got.ID != "sbx-123" || got.Status != "stopped" ||
		got.Backend != "local" {
		t.Errorf("unexpected identity JSON: %+v", got)
	}
	if got.Memory != "4 GiB" || got.CPUs != 0 {
		t.Errorf(
			"sandbox resource JSON should be humanized memory and integer cpus, got memory=%q cpus=%d",
			got.Memory,
			got.CPUs,
		)
	}
	if len(got.Mounts) != 1 || got.Mounts[0].GuestPath != "/workspace" ||
		got.Mounts[0].Kind != "bind" || got.Mounts[0].Source != "/host/workspace" ||
		!got.Mounts[0].Readonly {
		t.Errorf("unexpected mounts JSON: %+v", got.Mounts)
	}
}

func TestSandboxInspectQuiet(t *testing.T) {
	mock := &sandboxmsb.MockMsbClient{}
	mock.Sandboxes = []sandboxmsb.SandboxHandle{
		&sandboxmsb.MockSandboxHandle{
			Name_:   "agents-sandbox-vm-abc",
			Status_: msbSdk.SandboxStatusRunning,
			Cfg:     &msbSdk.SandboxConfig{Image: "agents-sandbox/runner-proj:latest"},
		},
	}
	cmd, ui := setupCommandFixtures(t, cmdSandbox, cmdInspect, "--quiet", "agents-sandbox-vm-abc")
	sandboxmsb.WithMsbMock(t, mock)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox inspect --quiet: %v", err)
	}
	if !containsNormalized(ui.PrintCalls, "agents-sandbox-vm-abc") {
		t.Errorf("PrintCalls missing identifier; got: %v", ui.PrintCalls)
	}
	if len(ui.OutCalls) != 0 {
		t.Errorf("quiet inspect should not write a block, got: %v", ui.OutCalls)
	}
}

func TestSandboxInspectMissing(t *testing.T) {
	runListCmdTest(t, []string{cmdSandbox, cmdInspect, "agents-sandbox-vm-missing"},
		func(_ *sandboxmsb.MockMsbClient) {}, nil, nil, true, "inspect sandbox")
}
