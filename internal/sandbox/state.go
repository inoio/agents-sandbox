package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// stateDirSuffix is the base directory for state files.
// Derived from XdgStateSuffix to allow override in tests.
//
//nolint:gochecknoglobals // Required for testable state directory path override
var stateDirSuffix = XdgStateSuffix

// HomeState represents the per-project state file contents.
type HomeState struct {
	HomeVolume  string `yaml:"home_volume"`
	ImageDigest string `yaml:"image_digest"`
}

// StateFile returns the path to the state file for a project slug.
func StateFile(slug string) string {
	return filepath.Join(stateDirSuffix, slug, "state.yaml")
}

// ReadState loads and parses the state file.
// Returns nil, nil if no file exists.
// Returns error for parse failures or non-"not found" I/O errors.
func ReadState(slug string) (*HomeState, error) {
	path := StateFile(slug)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	var state HomeState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	return &state, nil
}

// WriteState atomically writes the state to disk.
func WriteState(slug string, state HomeState) error {
	dir := filepath.Dir(StateFile(slug))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	tmpFile := filepath.Join(dir, ".state.yaml.tmp")
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("write state temp: %w", err)
	}
	if err := os.Rename(tmpFile, StateFile(slug)); err != nil {
		// best-effort cleanup of temp file if rename failed
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

// RemoveState removes the state file and its parent directory.
func RemoveState(slug string) error {
	stateDir := filepath.Join(stateDirSuffix, slug)
	return os.RemoveAll(stateDir)
}
