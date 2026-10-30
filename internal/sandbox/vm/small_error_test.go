package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPersistNetworkStateReadError covers the non-not-found ReadState error
// branch of persistFingerprints (a corrupt state file).
func TestPersistFingerprintsReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corruptnetproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	err := persistFingerprints(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{
			Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		},
	)
	if err == nil {
		t.Fatal("expected error for corrupted state YAML")
	}
}

// failingStateDirConfigPaths overrides only the state directory, delegating
// every other path to the (nil) embedded interface. It is used to force
// read/write errors on the per-project state file by pointing UserStateDir at a
// regular file, so the parent "directory" cannot be created or read.
type failingStateDirConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f failingStateDirConfigPaths) UserStateDir() string { return f.stateDir }

// withFailingStateDir installs a ConfigPaths whose UserStateDir is a regular
// file, so any attempt to read or write <stateDir>/... fails.
func withFailingStateDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })
}

// TestCurrentAgentVersionReadError covers the read-error branch of
// currentAgentVersion: an unreadable state file yields "".
func TestCurrentAgentVersionReadError(t *testing.T) {
	withFailingStateDir(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentVersion(k); got != "" {
		t.Errorf("currentAgentVersion() = %q, want empty when the state cannot be read", got)
	}
}

// TestValidateWorktreeBaseExecError covers the sb.Exec failure branch of
// validateWorktreeBase.
func TestValidateWorktreeBaseExecError(t *testing.T) {
	sb := &msb.MockSandbox{ExecErr: errors.New("exec failed")}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "main"); err == nil {
		t.Error("expected error when the base-check exec fails")
	}
}

// TestPersistConfigHashesWarnsOnError covers the warning branches of
// persistConfigHashes when the env/secret/network fingerprints cannot be
// persisted (a corrupt state file makes the write fail).
func TestPersistConfigHashesWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "corrupthashproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug, "opencode")
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	persistConfigHashes(
		state.Key{Slug: slug, Agent: "opencode"},
		state.Fingerprints{
			Network: state.BuildNetworkState(network.Policy{Profiles: []network.Profile{network.ProfileNone}}),
		},
		&ui,
	)

	if len(ui.WarnCalls) != 1 || !contains(ui.WarnCalls[0], "persisting config fingerprints") {
		t.Errorf("expected one warning about persisting config fingerprints, got %v", ui.WarnCalls)
	}
}
