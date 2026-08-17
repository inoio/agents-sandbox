package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

var errBoom = errors.New("boom")

func runListCmdTest(t *testing.T, cmdArgs []string, mockSetup func(m *sandboxmsb.MockMsbClient), wantOut,
	wantInfo []string, wantErr bool, wantErrContains string) {
	t.Helper()
	mock := &sandboxmsb.MockMsbClient{}
	mockSetup(mock)
	cmd, ui := setupCommandFixtures(t, cmdArgs...)
	sandboxmsb.WithMsbMock(t, mock)

	if err := cmd.Execute(); err != nil {
		if !wantErr {
			t.Fatalf("unexpected error: %v", err)
		}
		if wantErrContains != "" && !strings.Contains(err.Error(), wantErrContains) {
			t.Errorf("error %q should contain %q", err.Error(), wantErrContains)
		}
		return
	}
	if wantErr {
		t.Error("expected error, got none")
		return
	}
	for _, want := range wantOut {
		if !containsNormalized(ui.OutCalls, want) {
			t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
		}
	}
	for _, want := range wantInfo {
		if !containsNormalized(ui.InfoCalls, want) {
			t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
		}
	}
}

// containsNormalized reports whether any element of got matches want when both
// are whitespace-normalized. Column padding produced by the list renderers
// (e.g. %-50s) depends on identifier widths that change independently of
// test data, so alignment is not part of the assertion.
func containsNormalized(got []string, want string) bool {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	wantN := norm(want)
	for _, g := range got {
		if norm(g) == wantN {
			return true
		}
	}
	return false
}

func TestListSandboxes(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "empty (no sandboxes found)",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No sandboxes found."},
		},
		{
			name: "one sandbox running",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-abc123",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "opencode-sandbox/runner:latest"},
					},
				}
			},
			wantOut: []string{
				"NAME IMAGE STATUS CREATED",
				"opencode-sandbox-vm-abc123 opencode-sandbox/runner:latest running 2026-08-17 10:30:00",
			},
		},
		{
			name: "multiple sandboxes",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-alpha",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "opencode-sandbox/runner:latest"},
					},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-beta",
						Status_:    msb.SandboxStatusStopped,
						CreatedAt_: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "opencode-sandbox/runner:v1.0.0"},
					},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-gamma",
						Status_:    msb.SandboxStatusDraining,
						CreatedAt_: time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 15, 9, 15, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "opencode-sandbox/runner:v2.0.0"},
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-vm-alpha opencode-sandbox/runner:latest running 2026-08-17 09:00:00",
				"opencode-sandbox-vm-beta opencode-sandbox/runner:v1.0.0 stopped 2026-08-16 12:00:00",
				"opencode-sandbox-vm-gamma opencode-sandbox/runner:v2.0.0 draining 2026-08-15 08:30:00",
			},
		},
		{
			name: "non-project VMs filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{Name_: "myvm-other-abc", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{Name_: "legacy-vm-xyz", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{
						Name_:      "opencode-sandbox-vm-abc",
						Status_:    msb.SandboxStatusRunning,
						CreatedAt_: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
						UpdatedAt_: time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC),
						Cfg:        &msb.SandboxConfig{Image: "opencode-sandbox/runner:latest"},
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-vm-abc opencode-sandbox/runner:latest running 2026-08-17 10:00:00",
			},
		},
		{
			name:            "list sandboxes error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListSandboxesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list sandboxes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook
			runListCmdTest(
				t,
				[]string{cmdList},
				tt.mockSetup,
				tt.wantOut,
				tt.wantInfo,
				tt.wantErr,
				tt.wantErrContains,
			)
		})
	}
}

func TestListImages(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "empty (no images found)",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No images found."},
		},
		{
			name: "one image",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-abc123",
						ManifestDigest_: "sha256-abc123def456",
						SizeBytes_:      int64PtrCLI(2 * 1024 * 1024 * 1024),
						CreatedAt_:      time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"REFERENCE DIGEST SIZE CREATED",
				"opencode-sandbox/runner-abc123 sha256-abc123def456 2 GiB 2026-08-17 10:42:36",
			},
		},
		{
			name: "multiple images",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-latest",
						ManifestDigest_: "sha256-aaa",
						SizeBytes_:      int64PtrCLI(864256000),
						CreatedAt_:      time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-v1.0.0",
						ManifestDigest_: "sha256-bbb",
						SizeBytes_:      int64PtrCLI(1024),
						CreatedAt_:      time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-v2.0.0-beta",
						ManifestDigest_: "sha256-ccc",
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox/runner-latest sha256-aaa 824.2 MiB 2026-08-17 09:00:00",
				"opencode-sandbox/runner-v1.0.0 sha256-bbb 1 KiB 2026-08-16 12:00:00",
				"opencode-sandbox/runner-v2.0.0-beta sha256-ccc unknown",
			},
		},
		{
			name: "non-opencode images filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-xyz",
						ManifestDigest_: "sha256-abc123",
						SizeBytes_:      int64PtrCLI(512),
						CreatedAt_:      time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
					},
					&sandboxmsb.MockImageHandle{Reference_: "docker.io/some/img:latest", ManifestDigest_: "sha256-xxx"},
				}
			},
			wantOut: []string{
				"opencode-sandbox/runner-xyz sha256-abc123 512 B 2026-08-17 10:00:00",
			},
		},
		{
			name:            "list images error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListImagesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook
			runListCmdTest(
				t,
				[]string{cmdImage, cmdList},
				tt.mockSetup,
				tt.wantOut,
				tt.wantInfo,
				tt.wantErr,
				tt.wantErrContains,
			)
		})
	}
}

func TestTruncateDigest(t *testing.T) {
	full := "sha256:c9b7a85bcbd83f0eab313d091efe933c1608e952a082def11eb038841cb66375"
	got := truncateDigest(full)
	want := "sha256:c9b7a85bcbd8"
	if got != want {
		t.Errorf("truncateDigest() = %q, want %q", got, want)
	}
	if got := truncateDigest("sha256:abc"); got != "sha256:abc" {
		t.Errorf("truncateDigest(short) = %q, want unchanged", got)
	}
}

func TestSandboxListHeadersOrder(t *testing.T) {
	want := []string{"NAME", "IMAGE", "STATUS", "CREATED"}
	got := sandboxListHeaders()
	if len(got) != len(want) {
		t.Fatalf("sandboxListHeaders() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sandboxListHeaders()[%d] = %q, want %q (order must match msb)", i, got[i], want[i])
		}
	}
}

func int64PtrCLI(n int64) *int64 { return &n } //nolint:modernize // address-of-value is the intended pattern

func TestImageListHeadersOrder(t *testing.T) {
	want := []string{"REFERENCE", "DIGEST", "SIZE", "CREATED"}
	got := imageListHeaders()
	if len(got) != len(want) {
		t.Fatalf("imageListHeaders() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("imageListHeaders()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
