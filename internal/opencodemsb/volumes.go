package opencodemsb

import (
	"path/filepath"
)

func HomeVolumeName(projectSlug, imageDigest string) string {
	return projectSlug + "-opencode-home-" + imageDigest
}

type VolumeManager struct {
	fallback bool
	stateDir string
}

func NewVolumeManager(fallback bool, stateDir string) *VolumeManager {
	return &VolumeManager{fallback: fallback, stateDir: stateDir}
}

func (vm *VolumeManager) fallbackHomePath(projectSlug, imageDigest string) string {
	return filepath.Join(vm.stateDir, "state", projectSlug, "home", imageDigest)
}
