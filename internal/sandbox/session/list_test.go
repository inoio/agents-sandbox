package session

import (
	"context"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

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
			want: "2026-08-17 10:30",
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
			Name_:      "opencode-sandbox-vm-abc",
			Status_:    msbSdk.SandboxStatusRunning,
			CreatedAt_: now,
			UpdatedAt_: now,
			Cfg:        &msbSdk.SandboxConfig{Image: "opencode-sandbox/runner:latest"},
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
	if info.Name != "opencode-sandbox-vm-abc" || info.Status != "running" {
		t.Errorf("unexpected name/status: %+v", info)
	}
	if info.CreatedAt != "2026-08-17 10:30" {
		t.Errorf("CreatedAt = %q, want 2026-08-17 10:30", info.CreatedAt)
	}
	if info.UpdatedAt != "2026-08-17 10:30" {
		t.Errorf("UpdatedAt = %q, want 2026-08-17 10:30", info.UpdatedAt)
	}
	if info.Image != "opencode-sandbox/runner:latest" {
		t.Errorf("Image = %q, want opencode-sandbox/runner:latest", info.Image)
	}
}
