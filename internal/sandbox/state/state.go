package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/configpaths"

	"gopkg.in/yaml.v3"
)

// Key identifies a project artifact by project slug and agent.
type Key struct {
	Slug  string
	Agent string
}

func stateRoot() string {
	return configpaths.Get().UserStateDir()
}

// KeyDir returns the state directory for a project/agent key.
func KeyDir(k Key) string {
	return filepath.Join(stateRoot(), k.Slug, k.Agent)
}

// slugDir returns the per-project state directory (not agent-scoped), used for
// per-project claims.
func slugDir(slug string) string {
	return filepath.Join(stateRoot(), slug)
}

// slugPath returns a path under the key's state directory.
func slugPath(k Key, parts ...string) string {
	return filepath.Join(append([]string{KeyDir(k)}, parts...)...)
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

// NetworkState tracks network-policy fingerprint data for a project.
type NetworkState = FingerprintState

// MountState tracks host bind-mount fingerprint data for a project.
type MountState = FingerprintState

// HomeState represents the per-project state file contents.
type HomeState struct {
	HomeVolume   string       `yaml:"home_volume"`
	ImageDigest  string       `yaml:"image_digest"`
	EnvState     EnvState     `yaml:"env_state,omitempty"`
	SecretState  SecretState  `yaml:"secret_state,omitempty"`
	NetworkState NetworkState `yaml:"network_state,omitempty"`
	MountState   MountState   `yaml:"mount_state,omitempty"`
}

// NewHomeState returns a HomeState with a zeroed EnvState/SecretState, ready
// for write-after-creation or write-after-action flows.
func NewHomeState(homeVolume, digest string) HomeState {
	return HomeState{ //nolint:exhaustruct // EnvState/SecretState zeroed intentionally; serialized with omitempty
		HomeVolume:  homeVolume,
		ImageDigest: digest,
	}
}

func stateFile(k Key) string {
	return slugPath(k, "state.yaml")
}

// ErrStateNotFound is returned by readState when no state file exists yet.
var ErrStateNotFound = errors.New("state file not found")

// ReadState loads and parses the state file.
// Returns ErrStateNotFound with a nil state if no file exists.
// Returns an error for parse failures or non-"not found" I/O errors.
func ReadState(k Key) (*HomeState, error) {
	path := stateFile(k)
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
func WriteState(k Key, state HomeState) error {
	dir := filepath.Dir(stateFile(k))
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
	if err := os.Rename(tmpFile, stateFile(k)); err != nil {
		// best-effort cleanup of temp file if rename failed
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

// RemoveState removes the state file and its per-agent directory. Legacy
// agent-less keys remove only the legacy state file so per-project claims and
// other agents' state survive.
func RemoveState(k Key) error {
	if k.Agent != "" {
		return os.RemoveAll(KeyDir(k))
	}
	// Legacy agent-less state lived directly under the slug dir; remove only
	// the legacy state file, leaving per-project claims and sibling per-agent
	// state intact.
	if err := os.Remove(filepath.Join(slugDir(k.Slug), "state.yaml")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
