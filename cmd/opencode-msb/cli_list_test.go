package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

var errBoom = errors.New("boom")

func TestListSandboxes(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandbox.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "L1-empty (no sandboxes found)",
			mockSetup: func(_ *sandbox.MockMsbClient) {},
			wantInfo:  []string{"No sandboxes found."},
		},
		{
			name: "L2-one sandbox running",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Sandboxes = []sandbox.SandboxHandle{
					&sandbox.MockSandboxHandle{
						Name_:   "opencode-msb-vm-abc123",
						Status_: msb.SandboxStatusRunning,
					},
				}
			},
			wantOut: []string{"opencode-msb-vm-abc123                   running"},
		},
		{
			name: "L3-multiple sandboxes",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Sandboxes = []sandbox.SandboxHandle{
					&sandbox.MockSandboxHandle{Name_: "opencode-msb-vm-alpha", Status_: msb.SandboxStatusRunning},
					&sandbox.MockSandboxHandle{Name_: "opencode-msb-vm-beta", Status_: msb.SandboxStatusStopped},
					&sandbox.MockSandboxHandle{Name_: "opencode-msb-vm-gamma", Status_: msb.SandboxStatusDraining},
				}
			},
			wantOut: []string{
				"opencode-msb-vm-alpha                    running",
				"opencode-msb-vm-beta                     stopped",
				"opencode-msb-vm-gamma                    draining",
			},
		},
		{
			name: "L4-non-project VMs filtered",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Sandboxes = []sandbox.SandboxHandle{
					&sandbox.MockSandboxHandle{Name_: "myvm-other-abc", Status_: msb.SandboxStatusRunning},
					&sandbox.MockSandboxHandle{Name_: "legacy-vm-xyz", Status_: msb.SandboxStatusRunning},
					&sandbox.MockSandboxHandle{Name_: "opencode-msb-vm-abc", Status_: msb.SandboxStatusRunning},
				}
			},
			wantOut: []string{"opencode-msb-vm-abc                      running"},
		},
		{
			name:            "L5-list error",
			mockSetup:       func(m *sandbox.MockMsbClient) { m.ListSandboxesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list sandboxes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			tt.mockSetup(mock)
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmdList})

			if err := root.Execute(); err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if tt.wantErr {
				t.Error("expected error, got none")
				return
			}

			for _, want := range tt.wantOut {
				if !slices.Contains(ui.OutCalls, want) {
					t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
				}
			}
			for _, want := range tt.wantInfo {
				if !slices.Contains(ui.InfoCalls, want) {
					t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
				}
			}
		})
	}
}

func TestListImages(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandbox.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "L6-empty (no images found)",
			mockSetup: func(_ *sandbox.MockMsbClient) {},
			wantInfo:  []string{"No images found."},
		},
		{
			name: "L7-one image",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Images = []sandbox.ImageHandle{
					&sandbox.MockImageHandle{
						Reference_:      "opencode-msb/runner-abc123",
						ManifestDigest_: "sha256-abc123def456",
					},
				}
			},
			wantOut: []string{"opencode-msb/runner-abc123                         sha256-abc123def456"},
		},
		{
			name: "L8-multiple images",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Images = []sandbox.ImageHandle{
					&sandbox.MockImageHandle{Reference_: "opencode-msb/runner-latest", ManifestDigest_: "sha256-aaa"},
					&sandbox.MockImageHandle{Reference_: "opencode-msb/runner-v1.0.0", ManifestDigest_: "sha256-bbb"},
					&sandbox.MockImageHandle{
						Reference_:      "opencode-msb/runner-v2.0.0-beta",
						ManifestDigest_: "sha256-ccc",
					},
				}
			},
			wantOut: []string{
				"opencode-msb/runner-latest                         sha256-aaa",
				"opencode-msb/runner-v1.0.0                         sha256-bbb",
				"opencode-msb/runner-v2.0.0-beta                    sha256-ccc",
			},
		},
		{
			name: "L9-non-opencode images filtered",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Images = []sandbox.ImageHandle{
					&sandbox.MockImageHandle{Reference_: "opencode-msb/runner-xyz", ManifestDigest_: "sha256-abc123"},
					&sandbox.MockImageHandle{Reference_: "docker.io/some/img:latest", ManifestDigest_: "sha256-xxx"},
				}
			},
			wantOut: []string{"opencode-msb/runner-xyz                            sha256-abc123"},
		},
		{
			name:            "L10-list error",
			mockSetup:       func(m *sandbox.MockMsbClient) { m.ListImagesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			tt.mockSetup(mock)
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmdImage, cmdList})

			if err := root.Execute(); err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if tt.wantErr {
				t.Error("expected error, got none")
				return
			}

			for _, want := range tt.wantOut {
				if !slices.Contains(ui.OutCalls, want) {
					t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
				}
			}
			for _, want := range tt.wantInfo {
				if !slices.Contains(ui.InfoCalls, want) {
					t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
				}
			}
		})
	}
}

func TestListVolumes(t *testing.T) {
	type testCase struct {
		name            string
		mockSetup       func(m *sandbox.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}

	tests := []testCase{
		{
			name:      "L11-empty (no volumes found)",
			mockSetup: func(_ *sandbox.MockMsbClient) {},
			wantInfo:  []string{"No volumes found."},
		},
		{
			name: "L12-one volume",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Volumes = []sandbox.VolumeHandle{
					&sandbox.MockVolumeHandle{
						Name_: "opencode-msb-home-proj-abc",
						Path_: "/mnt/vol/home",
					},
				}
			},
			wantOut: []string{"opencode-msb-home-proj-abc                         /mnt/vol/home"},
		},
		{
			name: "L13-multiple volumes",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Volumes = []sandbox.VolumeHandle{
					&sandbox.MockVolumeHandle{Name_: "opencode-msb-home-alpha", Path_: "/mnt/vol/a"},
					&sandbox.MockVolumeHandle{Name_: "opencode-msb-home-beta", Path_: "/mnt/vol/b"},
				}
			},
			wantOut: []string{
				"opencode-msb-home-alpha                            /mnt/vol/a",
				"opencode-msb-home-beta                             /mnt/vol/b",
			},
		},
		{
			name: "L14-non-home volumes filtered",
			mockSetup: func(m *sandbox.MockMsbClient) {
				m.Volumes = []sandbox.VolumeHandle{
					&sandbox.MockVolumeHandle{Name_: "opencode-msb-clone-work", Path_: "/mnt/clone"},
					&sandbox.MockVolumeHandle{Name_: "opencode-msb-home-abc", Path_: "/mnt/vol"},
				}
			},
			wantOut: []string{"opencode-msb-home-abc                              /mnt/vol"},
		},
		{
			name:            "L15-list error",
			mockSetup:       func(m *sandbox.MockMsbClient) { m.ListVolumesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list volumes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Parallel() - disabled due to shared global test hook

			ui := &stdio.Mock{}
			mock := &sandbox.MockMsbClient{}
			tt.mockSetup(mock)
			overrideMsbClient(t, mock)

			root := buildRootCmd(ui)
			root.SetArgs([]string{cmdVolume, cmdList})

			if err := root.Execute(); err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.wantErrContains != "" && !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}
			if tt.wantErr {
				t.Error("expected error, got none")
				return
			}

			for _, want := range tt.wantOut {
				if !slices.Contains(ui.OutCalls, want) {
					t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
				}
			}
			for _, want := range tt.wantInfo {
				if !slices.Contains(ui.InfoCalls, want) {
					t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
				}
			}
		})
	}
}
