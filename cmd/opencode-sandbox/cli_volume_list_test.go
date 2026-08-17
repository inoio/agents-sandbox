package main

import (
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

func TestVolumeList(t *testing.T) {
	quota := uint32(2048)
	var capacity uint64 = 1024 * 1024 * 1024

	tests := []struct {
		name            string
		mockSetup       func(m *sandboxmsb.MockMsbClient)
		wantOut         []string
		wantInfo        []string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:      "empty",
			mockSetup: func(_ *sandboxmsb.MockMsbClient) {},
			wantInfo:  []string{"No volumes found."},
		},
		{
			name: "dir volume renders dash size",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:      "opencode-sandbox-home-proj",
						Kind_:      msb.VolumeKindDir,
						CreatedAt_: time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj dir - 2026-08-17 10:42:36",
			},
		},
		{
			name: "disk volume uses quota for size",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:      "opencode-sandbox-home-proj",
						Kind_:      msb.VolumeKindDisk,
						QuotaMiB_:  &quota,
						CreatedAt_: time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj disk 2 GiB 2026-08-17 10:42:36",
			},
		},
		{
			name: "disk volume uses capacity when no quota",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{
						Name_:          "opencode-sandbox-home-proj",
						Kind_:          msb.VolumeKindDisk,
						CapacityBytes_: &capacity,
						CreatedAt_:     time.Date(2026, 8, 17, 10, 42, 36, 0, time.UTC),
					},
				}
			},
			wantOut: []string{
				"opencode-sandbox-home-proj disk 1 GiB 2026-08-17 10:42:36",
			},
		},
		{
			name: "non-project volume filtered",
			mockSetup: func(m *sandboxmsb.MockMsbClient) {
				m.Volumes = []sandboxmsb.VolumeHandle{
					&sandboxmsb.MockVolumeHandle{Name_: "other-volume", Kind_: msb.VolumeKindDir},
				}
			},
			wantOut: []string{},
		},
		{
			name:            "list error",
			mockSetup:       func(m *sandboxmsb.MockMsbClient) { m.ListVolumesErr = errBoom },
			wantErr:         true,
			wantErrContains: "list volumes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &sandboxmsb.MockMsbClient{}
			tc.mockSetup(mock)
			cmd, ui := setupCommandFixtures(t, cmdVolume, cmdList)
			sandboxmsb.WithMsbMock(t, mock)
			if err := cmd.Execute(); err != nil {
				if !tc.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if tc.wantErr {
				t.Fatal("expected error, got none")
			}
			for _, want := range tc.wantOut {
				if !containsNormalized(ui.OutCalls, want) {
					t.Errorf("OutCalls missing %q; got: %v", want, ui.OutCalls)
				}
			}
			for _, want := range tc.wantInfo {
				if !containsNormalized(ui.InfoCalls, want) {
					t.Errorf("InfoCalls missing %q; got: %v", want, ui.InfoCalls)
				}
			}
		})
	}
}

func TestVolumeListFormatShared(t *testing.T) {
	if volumeListFormat == "" {
		t.Fatal("volumeListFormat must be non-empty so command and tests share it")
	}
}
