package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/doctor"
	"github.com/inoio/opencode-sandbox/internal/sandbox/image"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// volumeOpScenario describes how to run a volume command and what to expect.
type volumeOpScenario struct {
	name        string
	command     string
	withStopped bool
	withActive  bool
	writeState  bool
	// emptyHomeVolume writes state with no home_volume set.
	emptyHomeVolume bool
	wantErrPart     string

	// error injection switches.
	preflightFail   bool
	ensureImageErr  bool
	createVolumeErr bool
	prefillErr      bool
	copyErr         bool
	editCreateErr   bool
}

func runVolumeOpScenario(t *testing.T, tc volumeOpScenario) {
	t.Helper()
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}

	doctor.MockedCheckAll(t, !tc.preflightFail)
	image.WithMockAgentVersion(t, "0.0.0-test")
	if tc.ensureImageErr {
		docker.WithDefaultErrorDockerMock(t)
	} else {
		docker.WithNoopDockerMock(t)
	}

	if tc.createVolumeErr {
		mock.CreateVolumeFn = func(context.Context, string, ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, errBoom
		}
	}
	if tc.prefillErr || tc.copyErr || tc.editCreateErr {
		// PrefillVolume is always the first CreateSandbox; migrate's
		// CopyVolume and edit's own sandbox are the second.
		calls := 0
		mock.CreateSandboxFn = func(_ context.Context, _ string, _ ...msbSdk.SandboxOption) (msb.Sandbox, error) {
			calls++
			switch {
			case tc.prefillErr:
				return nil, errBoom
			case tc.copyErr && calls == 2:
				return msb.NewMockSandbox(msb.SandboxOpts{ExecErr: errBoom}), nil
			case tc.editCreateErr && calls == 2:
				return nil, errBoom
			default:
				return &msb.MockSandbox{}, nil
			}
		}
	}

	slug := git.ProjectSlug()
	if tc.withStopped {
		mock.Sandboxes = append(mock.Sandboxes, mkStoppedVM(slug))
	}
	if tc.withActive {
		mock.Sandboxes = append(mock.Sandboxes, mkActiveVM(slug))
	}
	msb.WithMsbMock(t, mock)

	if tc.writeState {
		homeVolume := fmt.Sprintf("opencode-sandbox-home-%s-20260814T132349", slug)
		if tc.emptyHomeVolume {
			homeVolume = ""
		}
		state.WriteState(slug, state.HomeState{
			HomeVolume:  homeVolume,
			ImageDigest: "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12",
			EnvState:    state.EnvState{},
			SecretState: state.SecretState{},
		})
	}

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, tc.command})
	err := root.Execute()
	if tc.wantErrPart == "" {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Errorf("expected error containing %q, got none", tc.wantErrPart)
		return
	}
	if !strings.Contains(err.Error(), tc.wantErrPart) {
		t.Errorf("expected error containing %q, got: %v", tc.wantErrPart, err)
	}
}

func TestVolumeReset(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdReset, writeState: true},
		{name: "with stopped VM", command: cmdReset, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdReset, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdReset, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdReset, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdReset, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdReset, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdReset, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdReset, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
	}
}

func TestVolumeMigrate(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdMigrate, writeState: true},
		{name: "with stopped VM", command: cmdMigrate, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdMigrate, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdMigrate, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdMigrate, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdMigrate, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdMigrate, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdMigrate, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdMigrate, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
		{name: "copy volume error", command: cmdMigrate, writeState: true, copyErr: true, wantErrPart: "copy files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
	}
}

func TestVolumeEdit(t *testing.T) {
	for _, tc := range []volumeOpScenario{
		{name: "without VM", command: cmdEdit, writeState: true},
		{name: "with stopped VM", command: cmdEdit, withStopped: true, writeState: true},
		{name: "with active VM", command: cmdEdit, withActive: true, writeState: true, wantErrPart: "VM still running"},
		{name: "no state", command: cmdEdit, wantErrPart: "no state file found for project"},
		{name: "preflight failed", command: cmdEdit, writeState: true, preflightFail: true, wantErrPart: "preflight failed"},
		{name: "ensure image error", command: cmdEdit, writeState: true, ensureImageErr: true, wantErrPart: "ensure image"},
		{name: "no home volume in state", command: cmdEdit, writeState: true, emptyHomeVolume: true, wantErrPart: "no volume to operate on"},
		{name: "create volume error", command: cmdEdit, writeState: true, createVolumeErr: true, wantErrPart: "create volume"},
		{name: "prefill volume error", command: cmdEdit, writeState: true, prefillErr: true, wantErrPart: "prefill new volume"},
		{name: "edit sandbox error", command: cmdEdit, writeState: true, editCreateErr: true, wantErrPart: "create edit sandbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runVolumeOpScenario(t, tc)
		})
	}
}
