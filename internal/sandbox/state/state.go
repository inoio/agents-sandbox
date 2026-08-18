package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/opencode-sandbox/internal/configpaths"

	"gopkg.in/yaml.v3"
)

func stateRoot() string {
	return configpaths.Get().UserStateDir()
}

// slugDir returns the slug-specific state directory under stateRoot.
func slugDir(slug string) string {
	return filepath.Join(stateRoot(), slug)
}

// slugPath returns a path under the slug's state directory.
func slugPath(slug string, parts ...string) string {
	return filepath.Join(append([]string{slugDir(slug)}, parts...)...)
}

// FingerprintState is the shared shape of env/secret fingerprint data.
type FingerprintState struct {
	Hash  string   `yaml:"hash,omitempty"`
	Names []string `yaml:"names,omitempty"`
}

// EnvState tracks environment-variable fingerprint data for a project.
type EnvState = FingerprintState

// SecretState tracks secret fingerprint data for a project.
type SecretState = FingerprintState

// HomeState represents the per-project state file contents.
type HomeState struct {
	HomeVolume  string      `yaml:"home_volume"`
	ImageDigest string      `yaml:"image_digest"`
	EnvState    EnvState    `yaml:"env_state,omitempty"`
	SecretState SecretState `yaml:"secret_state,omitempty"`
}

// NewHomeState returns a HomeState with a zeroed EnvState/SecretState, ready
// for write-after-creation or write-after-action flows.
func NewHomeState(homeVolume, digest string) HomeState {
	return HomeState{ //nolint:exhaustruct // EnvState/SecretState zeroed intentionally; serialized with omitempty
		HomeVolume:  homeVolume,
		ImageDigest: digest,
	}
}

func stateFile(slug string) string {
	return slugPath(slug, "state.yaml")
}

// ErrStateNotFound is returned by readState when no state file exists yet.
var ErrStateNotFound = errors.New("state file not found")

// ReadState loads and parses the state file.
// Returns ErrStateNotFound with a nil state if no file exists.
// Returns an error for parse failures or non-"not found" I/O errors.
func ReadState(slug string) (*HomeState, error) {
	path := stateFile(slug)
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
	dir := filepath.Dir(stateFile(slug))
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
	if err := os.Rename(tmpFile, stateFile(slug)); err != nil {
		// best-effort cleanup of temp file if rename failed
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

// RemoveState removes the state file and its parent directory.
func RemoveState(slug string) error {
	slugStateDir := slugDir(slug)
	return os.RemoveAll(slugStateDir)
}
