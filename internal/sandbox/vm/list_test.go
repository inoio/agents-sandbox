package vm

import (
	"context"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

func intPtr(n uint32) *uint32 { return &n } //nolint:modernize // keep explicit pointer helper for test clarity

func TestFormatTime(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{name: "zero time", in: time.Time{}, want: ""},
		{
			name: "timestamp",
			in:   time.Date(2026, 8, 17, 10, 30, 45, 0, time.UTC),
			want: "2026-08-17 10:30:45",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatTime(tt.in); got != tt.want {
				t.Errorf("FormatTime(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestListSandboxesPopulatesInfo(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-abc",
			Status_:    msbSdk.SandboxStatusRunning,
			CreatedAt_: now,
			UpdatedAt_: now,
			Cfg:        &msbSdk.SandboxConfig{Image: "agents-sandbox/runner:latest"},
		},
		&msb.MockSandboxHandle{Name_: "myvm-other", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	got, err := ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListSandboxes() got %d infos, want 1 (non-project VM filtered)", len(got))
	}
	info := got[0]
	if info.Name != "agents-sandbox-vm-abc" || info.Status != "running" {
		t.Errorf("unexpected name/status: %+v", info)
	}
	if info.CreatedAt != "2026-08-17 10:30:00" {
		t.Errorf("CreatedAt = %q, want 2026-08-17 10:30:00", info.CreatedAt)
	}
	if info.Image != "agents-sandbox/runner:latest" {
		t.Errorf("Image = %q, want agents-sandbox/runner:latest", info.Image)
	}
}

func TestListSandboxesRunningOnly(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-a", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-b", Status_: msbSdk.SandboxStatusStopped},
		&msb.MockSandboxHandle{Name_: "other-vm-c", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{RunningOnly: true})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "agents-sandbox-vm-a" {
		t.Errorf("expected only running project VM, got %+v", infos)
	}
}

func TestListSandboxesStoppedOnly(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-a", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-b", Status_: msbSdk.SandboxStatusStopped},
		&msb.MockSandboxHandle{Name_: "other-vm-c", Status_: msbSdk.SandboxStatusStopped},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{StoppedOnly: true})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "agents-sandbox-vm-b" {
		t.Errorf("expected only stopped project VM, got %+v", infos)
	}
}

func TestListSandboxesRunningWinsWhenBoth(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-a", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-b", Status_: msbSdk.SandboxStatusStopped},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{RunningOnly: true, StoppedOnly: true})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "agents-sandbox-vm-a" {
		t.Errorf("expected running-only when both set, got %+v", infos)
	}
}

func TestListSandboxesLimit(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-a", Status_: msbSdk.SandboxStatusRunning},
		&msb.MockSandboxHandle{Name_: "agents-sandbox-vm-b", Status_: msbSdk.SandboxStatusRunning},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background(), ListOption{Limit: intPtr(1)})
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("expected 1 result due to limit, got %d: %+v", len(infos), infos)
	}
}

func TestListSandboxesLabelsForwarded(t *testing.T) {
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = nil
	var captured map[string]string
	mock.ListSandboxesFn = func(_ context.Context, labels map[string]string) ([]msb.SandboxHandle, error) {
		captured = labels
		return mock.Sandboxes, nil
	}
	msb.WithMsbMock(t, mock)

	if _, err := ListSandboxes(context.Background(), ListOption{Labels: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if captured == nil || captured["k"] != "v" {
		t.Errorf("expected labels forwarded to msb, got %+v", captured)
	}
}

func TestListSandboxesPopulatesInfoFields(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	mock := &msb.MockMsbClient{}
	mock.Sandboxes = []msb.SandboxHandle{
		&msb.MockSandboxHandle{
			Name_:      "agents-sandbox-vm-a",
			Status_:    msbSdk.SandboxStatusRunning,
			CreatedAt_: now,
			UpdatedAt_: now.Add(time.Hour),
			Cfg: &msbSdk.SandboxConfig{
				Image:  "agents-sandbox/runner:latest",
				Labels: map[string]string{naming.LabelProject: "proj"},
			},
		},
	}
	msb.WithMsbMock(t, mock)

	infos, err := ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(infos), infos)
	}
	info := infos[0]
	if info.Labels == nil || info.Labels[naming.LabelProject] != "proj" {
		t.Errorf("Labels not populated: %+v", info.Labels)
	}
	if !info.CreatedAtRaw.Equal(now) {
		t.Errorf("CreatedAtRaw = %v, want %v", info.CreatedAtRaw, now)
	}
	if !info.UpdatedAtRaw.Equal(now.Add(time.Hour)) {
		t.Errorf("UpdatedAtRaw = %v, want %v", info.UpdatedAtRaw, now.Add(time.Hour))
	}
}
