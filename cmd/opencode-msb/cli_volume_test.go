package main

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func TestVolumeMigrateHelp(t *testing.T) {
	initTestRepo(t)
	sandbox.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sandbox.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdMigrate, "--help"})
	root.Execute()
}

func TestVolumeResetHelp(t *testing.T) {
	initTestRepo(t)
	sandbox.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sandbox.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdReset, "--help"})
	root.Execute()
}

func TestVolumeEditHelp(t *testing.T) {
	initTestRepo(t)
	sandbox.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sandbox.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdEdit, "--help"})
	root.Execute()
}
