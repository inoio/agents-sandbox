package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// failingStateDirConfigPaths overrides only the state directory, delegating
// every other path to the (nil) embedded interface. It is used to force
// read/write errors on the updater state file by pointing UserStateDir at a
// regular file, so the parent "directory" cannot be created or read.
type failingStateDirConfigPaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (f failingStateDirConfigPaths) UserStateDir() string { return f.stateDir }

// withFailingStateDir installs a ConfigPaths whose UserStateDir is a regular
// file, so any attempt to read or write <stateDir>/updater.yaml fails.
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

func TestPersistUpgradeStateWarnsOnSaveError(t *testing.T) {
	withFailingStateDir(t)
	ui := &termio.Mock{}

	persistUpgradeState(ui, upgradeState{CurrentVersion: "1.0.0"})

	joined := strings.Join(ui.WarnCalls, " ")
	if !strings.Contains(joined, "could not persist updater state") {
		t.Errorf("expected a persist warning, got %q", joined)
	}
}

func TestLoadOrFreshUpgradeStateWarnsOnReadError(t *testing.T) {
	withFailingStateDir(t)
	ui := &termio.Mock{}

	got := loadOrFreshUpgradeState(ui)

	joined := strings.Join(ui.WarnCalls, " ")
	if !strings.Contains(joined, "could not read updater state") {
		t.Errorf("expected a read warning, got %q", joined)
	}
	if !got.LastChecked.IsZero() {
		t.Errorf("expected a fresh empty state on read error, got %+v", got)
	}
}
