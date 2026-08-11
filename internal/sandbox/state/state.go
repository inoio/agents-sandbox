package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/configpaths"

	"gopkg.in/yaml.v3"
)

// StateDir overrides the state directory root for tests when set to a
// non-empty value. An empty value resolves to the XDG state directory at call
// time (see stateRoot).
//
//nolint:gochecknoglobals // Required for testable state directory path override
var StateDir = ""

// stateRoot returns the base directory for state files. Tests override it via
// StateDir/SetStateDirForTest; otherwise it is the XDG state directory,
// resolved fresh so environment changes are honored.
func stateRoot() string {
	if StateDir != "" {
		return StateDir
	}
	return configpaths.GetConfigPaths().UserStateDir()
}

// EnvState tracks environment-variable fingerprint data for a project.
type EnvState struct {
	Hash  string   `yaml:"hash,omitempty"`
	Names []string `yaml:"names,omitempty"`
}

// SecretState tracks secret fingerprint data for a project.
type SecretState struct {
	Hash  string   `yaml:"hash,omitempty"`
	Names []string `yaml:"names,omitempty"`
}

// HomeState represents the per-project state file contents.
type HomeState struct {
	HomeVolume  string      `yaml:"home_volume"`
	ImageDigest string      `yaml:"image_digest"`
	EnvState    EnvState    `yaml:"env_state,omitempty"`
	SecretState SecretState `yaml:"secret_state,omitempty"`
}

//nolint:revive // StateFile matches the original unexported name stateFile
func StateFile(slug string) string {
	return filepath.Join(stateRoot(), slug, "state.yaml")
}

// SetStateDirForTest overrides the state directory root for the given test.
// The original value is restored via t.Cleanup.
func SetStateDirForTest(t *testing.T, dir string) {
	old := StateDir
	StateDir = dir
	t.Cleanup(func() { StateDir = old })
}

// ErrStateNotFound is returned by readState when no state file exists yet.
var ErrStateNotFound = errors.New("state file not found")

// ReadState loads and parses the state file.
// Returns ErrStateNotFound with a nil state if no file exists.
// Returns an error for parse failures or non-"not found" I/O errors.
func ReadState(slug string) (*HomeState, error) {
	path := StateFile(slug)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStateNotFound
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
	slugStateDir := filepath.Join(stateRoot(), slug)
	return os.RemoveAll(slugStateDir)
}
