package main

import (
	"errors"
	"strings"
	"testing"

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
						Name_:   "opencode-sandbox-vm-abc123",
						Status_: msb.SandboxStatusRunning,
					},
				}
			},
			wantOut: []string{"opencode-sandbox-vm-abc123                   running"},
		},
		{
			name: "multiple sandboxes",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{
						Name_:   "opencode-sandbox-vm-alpha",
						Status_: msb.SandboxStatusRunning,
					},
					&sandboxmsb.MockSandboxHandle{Name_: "opencode-sandbox-vm-beta", Status_: msb.SandboxStatusStopped},
					&sandboxmsb.MockSandboxHandle{
						Name_:   "opencode-sandbox-vm-gamma",
						Status_: msb.SandboxStatusDraining,
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-vm-alpha                    running",
				"opencode-sandbox-vm-beta                     stopped",
				"opencode-sandbox-vm-gamma                    draining",
			},
		},
		{
			name: "non-project VMs filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Sandboxes = []sandboxmsb.SandboxHandle{
					&sandboxmsb.MockSandboxHandle{Name_: "myvm-other-abc", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{Name_: "legacy-vm-xyz", Status_: msb.SandboxStatusRunning},
					&sandboxmsb.MockSandboxHandle{Name_: "opencode-sandbox-vm-abc", Status_: msb.SandboxStatusRunning},
				}
			},
			wantOut: []string{"opencode-sandbox-vm-abc                      running"},
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
					},
				}
			},
			wantOut: []string{"opencode-sandbox/runner-abc123                         sha256-abc123def456"},
		},
		{
			name: "multiple images",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-latest",
						ManifestDigest_: "sha256-aaa",
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-v1.0.0",
						ManifestDigest_: "sha256-bbb",
					},
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-v2.0.0-beta",
						ManifestDigest_: "sha256-ccc",
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox/runner-latest                         sha256-aaa",
				"opencode-sandbox/runner-v1.0.0                         sha256-bbb",
				"opencode-sandbox/runner-v2.0.0-beta                    sha256-ccc",
			},
		},
		{
			name: "non-opencode images filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Images = []sandboxmsb.ImageHandle{
					&sandboxmsb.MockImageHandle{
						Reference_:      "opencode-sandbox/runner-xyz",
						ManifestDigest_: "sha256-abc123",
					},
					&sandboxmsb.MockImageHandle{Reference_: "docker.io/some/img:latest", ManifestDigest_: "sha256-xxx"},
				}
			},
			wantOut: []string{"opencode-sandbox/runner-xyz                            sha256-abc123"},
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

func TestListVolumes(t *testing.T) {
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
			name:      "empty (no volumes found)",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No volumes found."},
		},
		{
			name: "one volume",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_: "opencode-sandbox-home-proj-abc",
						Path_: "/mnt/vol/home",
					},
				}
			},
			wantOut: []string{"opencode-sandbox-home-proj-abc                         /mnt/vol/home"},
		},
		{
			name: "multiple volumes",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{Name_: "opencode-sandbox-home-alpha", Path_: "/mnt/vol/a"},
					&sandboxmsb.MockVolumeHandle{Name_: "opencode-sandbox-home-beta", Path_: "/mnt/vol/b"},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-alpha                            /mnt/vol/a",
				"opencode-sandbox-home-beta                             /mnt/vol/b",
			},
		},
		{
			name: "non-home volumes filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{Name_: "opencode-sandbox-clone-work", Path_: "/mnt/clone"},
					&sandboxmsb.MockVolumeHandle{Name_: "opencode-sandbox-home-abc", Path_: "/mnt/vol"},
				}
			},
			wantOut: []string{"opencode-sandbox-home-abc                              /mnt/vol"},
		},
		{
			name:            "list volumes error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListVolumesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list volumes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook
			runListCmdTest(
				t,
				[]string{cmdVolume, cmdList},
				tt.mockSetup,
				tt.wantOut,
				tt.wantInfo,
				tt.wantErr,
				tt.wantErrContains,
			)
		})
	}
}
