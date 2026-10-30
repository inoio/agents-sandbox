package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/inoio/agents-sandbox/internal/atomicwrite"
	"github.com/inoio/agents-sandbox/internal/configpaths"

	"go.yaml.in/yaml/v3"
)

// Key identifies a project artifact by project slug and agent.
type Key struct {
	Slug  string
	Agent string
}

func stateRoot() string {
	return configpaths.Get().UserStateDir()
}

// locksDir is the never-deleted tree holding every lock file. Lock files live
// outside the removable per-key state directory so removing a key's state can
// never delete the lock a concurrent writer holds (or resurrect removed state).
func locksDir() string {
	return filepath.Join(stateRoot(), "locks")
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
//
//exhaustruct:ignore
type HomeState struct {
	HomeVolume      string       `yaml:"home_volume"`
	ImageDigest     string       `yaml:"image_digest"`
	AgentVersion    string       `yaml:"agent_version,omitempty"`
	AgentSource     string       `yaml:"agent_source,omitempty"`
	DockerSource    string       `yaml:"docker_source,omitempty"`
	OfferedVersions []string     `yaml:"offered_versions,omitempty"`
	EnvState        EnvState     `yaml:"env_state,omitempty"`
	SecretState     SecretState  `yaml:"secret_state,omitempty"`
	NetworkState    NetworkState `yaml:"network_state,omitempty"`
	MountState      MountState   `yaml:"mount_state,omitempty"`

	// RecoveredHomeVolume is the home volume a VM-only identity adoption (#144)
	// preserved by destroying a legacy VM whose state file was lost. Adoption
	// records it before the destroy, because the legacy VM is then gone and its
	// mount can no longer be read; home resolution recovers it into HomeVolume
	// (with the current digest and fingerprints) and clears the field. It is set
	// only while a recovery is pending, so an empty value means the state is
	// settled.
	RecoveredHomeVolume string `yaml:"recovered_home_volume,omitempty"`
}

// Offered reports whether the version has already had its rebuild prompt shown
// for this project's image.
func (s *HomeState) Offered(v string) bool {
	return slices.Contains(s.OfferedVersions, v)
}

// MarkOffered records that v's rebuild prompt has been shown for this project's
// image, so it is not offered again.
func (s *HomeState) MarkOffered(v string) {
	if !s.Offered(v) {
		s.OfferedVersions = append(s.OfferedVersions, v)
	}
}

// NewHomeState returns a HomeState with a zeroed EnvState/SecretState, ready
// for write-after-creation or write-after-action flows.
func NewHomeState(homeVolume, digest string) HomeState {
	return HomeState{
		HomeVolume:  homeVolume,
		ImageDigest: digest,
	}
}

// Fingerprints is the set of VM-config fingerprints persisted in HomeState.
// Seeding a fresh HomeState with the desired fingerprints keeps an empty
// (fingerprint-less) state from coexisting with a VM, where it would be
// mistaken for a change on the next decision (see NetworkChanged and
// specs/reconfig-decision.md).
type Fingerprints struct {
	Env     EnvState
	Secret  SecretState
	Network NetworkState
	Mount   MountState
}

// Apply copies the fingerprints onto hs and returns the updated state.
func (f Fingerprints) Apply(hs HomeState) HomeState {
	hs.EnvState = f.Env
	hs.SecretState = f.Secret
	hs.NetworkState = f.Network
	hs.MountState = f.Mount
	return hs
}

func stateFile(k Key) string {
	return slugPath(k, "state.yaml")
}

// ErrStateNotFound is returned by ReadState when no state file exists yet.
var ErrStateNotFound = errors.New("state file not found")

// ErrStateUnparsable wraps a YAML parse failure in ReadState and ReadRootState,
// classifying a corrupt document distinctly from a missing one. WithHomeState
// returns it without writing so an unknown document is never clobbered; only
// recovery (RecoverHomeState) may discard an unparsable document.
var ErrStateUnparsable = errors.New("state file is unparsable")

// WriteInstalled reports whether a state write's error was raised after the
// rename already installed the new document: the on-disk state is the new one,
// and only a durability step failed (see atomicwrite.ErrInstalled). A caller
// that rolls back on a write error must not roll back when this is true, or it
// would discard the resource the installed state names.
func WriteInstalled(err error) bool {
	return errors.Is(err, atomicwrite.ErrInstalled)
}

// ReadState loads and parses the state file.
// Returns ErrStateNotFound with a nil state if no file exists.
// Returns ErrStateUnparsable-wrapped error for parse failures and a plain error
// for non-"not found" I/O errors.
func ReadState(k Key) (*HomeState, error) {
	path := stateFile(k)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStateNotFound
		}
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}
	state, err := parseHomeState(data)
	if err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	return &state, nil
}

// parseHomeState is the pure unmarshal step of ReadState: arbitrary bytes in,
// HomeState or ErrStateUnparsable-wrapped error out, no file I/O. Kept separate
// so FuzzParseState can drive it without a filesystem. The schema is checked
// before the fields are decoded, so a newer document whose fields changed shape
// is refused with ErrStateSchemaUnsupported, never misread as unparsable.
func parseHomeState(data []byte) (HomeState, error) {
	if err := checkDocumentSchema(data, HomeStateSchema); err != nil {
		return HomeState{}, err
	}
	var state HomeState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return HomeState{}, fmt.Errorf("%w: %w", ErrStateUnparsable, err)
	}
	return state, nil
}

// writeState atomically writes the state to disk. Unexported so every mutation
// flows through WithHomeState or RecoverHomeState.
func writeState(k Key, state HomeState) error {
	data, err := yaml.Marshal(versionedDocument[HomeState]{Schema: HomeStateSchema, Document: state})
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := atomicwrite.WriteFile(stateFile(k), data); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	return nil
}

// RemoveHomeStateIf removes the state file and its per-agent directory only
// when shouldRemove reports true for the current document. The read, the
// decision, and the removal all run under one blocking home lock, so the
// decision cannot interleave with a concurrent writer that recreates or
// re-references the state between them.
//
// shouldRemove is called with the document read under the lock. A missing
// document removes nothing and is not passed to shouldRemove. An unreadable or
// unparsable document is returned as an error and nothing is removed, so an
// unknown document is never discarded.
//
// Lock files under locksDir are never removed. Legacy agent-less keys remove
// only the legacy state file so per-project claims and other agents' state
// survive.
func RemoveHomeStateIf(k Key, shouldRemove func(cur *HomeState) bool) error {
	return withHomeLock(k, func() error {
		cur, err := ReadState(k)
		if err != nil {
			if errors.Is(err, ErrStateNotFound) {
				return nil
			}
			return err
		}
		if !shouldRemove(cur) {
			return nil
		}
		return removeHomeStateLocked(k)
	})
}

// removeHomeStateLocked removes the state file and its per-agent directory, run
// by its caller while the home lock is held.
func removeHomeStateLocked(k Key) error {
	if k.Agent != "" {
		return os.RemoveAll(KeyDir(k))
	}
	// Legacy agent-less state lived directly under the slug dir; remove only the
	// legacy state file, leaving per-project claims and sibling per-agent state
	// intact. The file was read under the lock, so it exists: there is no
	// not-exist case to tolerate.
	return os.Remove(filepath.Join(slugDir(k.Slug), "state.yaml"))
}
