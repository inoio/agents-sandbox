package main

import (
	"fmt"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/git"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestVolumeMigrateHelp(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdMigrate, "--help"})
	root.Execute()
}

func TestVolumeResetWithoutVM(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	state.WriteState(git.ProjectSlug(ui), state.HomeState{
		HomeVolume:  "test",
		ImageDigest: "test ",
		EnvState:    state.EnvState{},
		SecretState: state.SecretState{},
	})
	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdReset})
	err := root.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVolumeResetWithStoppedVM(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	slug := git.ProjectSlug(ui)
	mock.Sandboxes = append(mock.Sandboxes,
		mkStoppedVM(slug))

	msb.WithMsbMock(t, mock)

	state.WriteState(slug, state.HomeState{
		HomeVolume:  fmt.Sprintf("opencode-sandbox-home-%s-20260814T132349", slug),
		ImageDigest: "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12",
		EnvState:    state.EnvState{},
		SecretState: state.SecretState{},
	})
	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdReset})
	err := root.Execute()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVolumeResetWithActiveVM(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	slug := git.ProjectSlug(ui)
	mock.Sandboxes = append(mock.Sandboxes,
		mkActiveVM(slug))

	msb.WithMsbMock(t, mock)

	state.WriteState(slug, state.HomeState{
		HomeVolume:  fmt.Sprintf("opencode-sandbox-home-%s-20260814T132349", slug),
		ImageDigest: "sha256:2e454dd5b8ba117988d3beebd09f457ca46e758724e673d2272f77ddc9b3fb12",
		EnvState:    state.EnvState{},
		SecretState: state.SecretState{},
	})
	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdReset})
	err := root.Execute()
	if !strings.Contains(err.Error(), "VM still running") {
		t.Errorf("expected VM still running error, got: %v", err)
	}
}

func TestVolumeEditHelp(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdEdit, "--help"})
	root.Execute()
}
