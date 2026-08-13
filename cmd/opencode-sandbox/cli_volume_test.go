package main

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
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

func TestVolumeResetHelp(t *testing.T) {
	initTestRepo(t)
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := &termio.Mock{}
	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)

	root := buildRootCmd(ui)
	root.SetArgs([]string{cmdVolume, cmdReset, "--help"})
	root.Execute()
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
