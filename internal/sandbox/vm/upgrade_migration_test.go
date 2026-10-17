package vm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestMigrateLegacyUpdaterStateFoldsLastCheckedAndDeletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must be deleted")
	}
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Fatal("last_checked not folded")
	}
}

func TestMigrateLegacyUpdaterStatePresentValueWins(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: time.Unix(5000, 0).UTC()},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.Unix() != 5000 {
		t.Fatal("present last_checked must win over the legacy value")
	}
}

func TestMigrateLegacyUpdaterStateCorruptIsDeleted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(legacyUpdaterStatePath(), []byte("not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); !os.IsNotExist(err) {
		t.Fatal("corrupt updater.yaml must be deleted")
	}
}

func TestMigrateLegacyUpdaterStateSkippedWhenLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ok, err := state.WithRootState(true, func(*state.RootTxn) error {
		migrateLegacyUpdaterState(&termio.Mock{})
		return nil
	})
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("updater.yaml must survive when the lock is held")
	}
}

func TestMigrateLegacyUpdaterStateFoldsFlatFieldIntoOpencode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("last_checked: 2021-06-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	root, _ := state.ReadRootState()
	if got := root.AgentCheck("opencode").LastChecked.UTC().Year(); got != 2021 {
		t.Fatalf("flat last_checked not folded into opencode: year=%d", got)
	}
	if _, err := os.Stat(legacyUpdaterStatePath()); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must be deleted")
	}
}

func TestMigrateLegacyUpdaterStateSkipsZeroLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    current_version: 1.2.3\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	root, _ := state.ReadRootState()
	if !root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Fatal("a zero last_checked must not seed a check state")
	}
}

func TestMigrateLegacyUpdaterStateReadErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(legacyUpdaterStatePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("an unreadable updater.yaml must survive a read failure")
	}
}

func TestMigrateLegacyUpdaterStateRootStateReadErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("updater.yaml must survive a root-state read failure")
	}
}

func TestMigrateLegacyUpdaterStateLockErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserStateDir(), "locks", "root.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("updater.yaml must survive a lock failure")
	}
}

func TestMigrateLegacyUpdaterStateWriteErrorIsNonFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	stateDir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "locks", "root.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
	migrateLegacyUpdaterState(&termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath()); err != nil {
		t.Fatal("updater.yaml must survive a root-state write failure")
	}
}
