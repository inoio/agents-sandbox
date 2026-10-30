package state

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func withFixedNow(t *testing.T, fixed time.Time) {
	t.Helper()
	original := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = original })
}

func TestRepairRootStateQuarantinesCorruptDocument(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withFixedNow(t, time.Date(2026, 10, 29, 12, 0, 0, 5, time.UTC))
	corrupt := []byte("migrations: [unterminated")
	seedFile(t, rootStateFile(), corrupt)

	quarantinedPath, err := RepairRootState()
	if err != nil {
		t.Fatalf("RepairRootState: %v", err)
	}
	if want := rootStateFile() + ".corrupt-20261029T120000.000000005Z"; quarantinedPath != want {
		t.Errorf("quarantined path = %q, want %q", quarantinedPath, want)
	}
	if got, _ := os.ReadFile(quarantinedPath); string(got) != string(corrupt) {
		t.Errorf("quarantined bytes = %q, want %q", got, corrupt)
	}
	if _, err := os.Stat(rootStateFile()); !os.IsNotExist(err) {
		t.Errorf("root state file still present: %v", err)
	}
	ok, err := WithRootState(true, func(txn *RootTxn) error {
		txn.State().SetAgentCheck("opencode", AgentCheckState{LatestVersion: "1.0.0"})
		return txn.Commit()
	})
	if err != nil || !ok {
		t.Fatalf("transaction after repair: ok=%v err=%v", ok, err)
	}
}

func TestRepairRootStateLeavesHealthyAndMissingDocumentsAlone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if path, err := RepairRootState(); path != "" || err != nil {
		t.Fatalf("missing: path=%q err=%v", path, err)
	}
	if err := writeRootState(RootState{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if path, err := RepairRootState(); path != "" || err != nil {
		t.Fatalf("healthy: path=%q err=%v", path, err)
	}
	if _, err := os.Stat(rootStateFile()); err != nil {
		t.Errorf("healthy document removed: %v", err)
	}
}

func TestRepairRootStateRefusesNewerSchema(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	newer := []byte("schema: 99\nmigrations: [changed, shape]\n")
	seedFile(t, rootStateFile(), newer)
	if _, err := RepairRootState(); !errors.Is(err, ErrStateSchemaUnsupported) {
		t.Fatalf("error = %v, want ErrStateSchemaUnsupported", err)
	}
	if got, _ := os.ReadFile(rootStateFile()); string(got) != string(newer) {
		t.Errorf("newer-schema document changed: %q", got)
	}
}

func TestRepairRootStateReportsLockFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir failed")
	original := mkdirRootLock
	mkdirRootLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirRootLock = original })
	if _, err := RepairRootState(); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestRepairRootStateReportsRenameFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedFile(t, rootStateFile(), []byte("migrations: [unterminated"))
	withFixedNow(t, time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	wantErr := errors.New("rename blocked")
	original := renameRootStateFile
	renameRootStateFile = func(string, string) error { return wantErr }
	t.Cleanup(func() { renameRootStateFile = original })
	_, err := RepairRootState()
	if err == nil || !strings.Contains(err.Error(), "quarantine root state file") || !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want quarantine failure wrapping %v", err, wantErr)
	}
}

func TestCorruptRootStateErrorNamesRepairCommand(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedFile(t, rootStateFile(), []byte("migrations: [unterminated"))
	_, err := WithRootState(true, func(*RootTxn) error { return nil })
	if !errors.Is(err, ErrStateUnparsable) || !strings.Contains(err.Error(), RepairRootStateCommand) {
		t.Fatalf("error = %v, want ErrStateUnparsable naming %q", err, RepairRootStateCommand)
	}
}
