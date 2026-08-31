package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// TestPersistNetworkStateReadError covers the non-not-found ReadState error
// branch of persistNetworkState (a corrupt state file).
func TestPersistNetworkStateReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "corruptnetproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	err := persistNetworkState(slug, network.Policy{Profile: network.ProfileNone})
	if err == nil {
		t.Fatal("expected error for corrupted state YAML")
	}
}

// TestCurrentUpgradeVersionReadError covers the load-error branch of
// currentUpgradeVersion: an unreadable state file yields "".
func TestCurrentUpgradeVersionReadError(t *testing.T) {
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

	if got := currentUpgradeVersion(); got != "" {
		t.Errorf("currentUpgradeVersion() = %q, want empty when the state cannot be read", got)
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
// persisted (a corrupt state file makes both writes fail).
func TestPersistConfigHashesWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "corrupthashproj"
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "{ corrupted: yaml: [")

	persistConfigHashes(slug, network.Policy{Profile: network.ProfileNone}, nil, &ui)

	if !contains(joinStrings(ui.WarnCalls), "persisting env/secret fingerprints") {
		t.Errorf("expected a warning about persisting env/secret fingerprints, got %v", ui.WarnCalls)
	}
	if !contains(joinStrings(ui.WarnCalls), "persisting network fingerprint") {
		t.Errorf("expected a warning about persisting the network fingerprint, got %v", ui.WarnCalls)
	}
	if !contains(joinStrings(ui.WarnCalls), "persisting mount fingerprint") {
		t.Errorf("expected a warning about persisting the mount fingerprint, got %v", ui.WarnCalls)
	}
}
