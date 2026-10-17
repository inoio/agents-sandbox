package state

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestRootStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	f := rootStateFile()
	if filepath.Base(f) != "state.yaml" {
		t.Errorf("expected state.yaml basename, got %q", filepath.Base(f))
	}
	if filepath.Dir(f) != stateRoot() {
		t.Errorf("expected root state under %q, got %q", stateRoot(), f)
	}
}

func TestReadRootState_MissingFileReturnsZero(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	result, err := ReadRootState()
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil zero RootState for missing file")
	}
	if _, ok := result.Migration("runtime_home"); ok {
		t.Error("expected pending (absent) runtime_home migration for missing file")
	}
}

func TestMigrationRegistryRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Migrations: map[string]Migration{
		"runtime_home": {Status: MigrationCopying, Copied: []string{"a"}, Excluded: []string{"b"}},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	m, ok := got.Migration("runtime_home")
	if !ok || m.Status != MigrationCopying || len(m.Copied) != 1 || m.Copied[0] != "a" || len(m.Excluded) != 1 {
		t.Fatalf("migration = %+v, ok = %v", m, ok)
	}
}

func TestMigrationWireKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Migrations: map[string]Migration{
		"runtime_home": {Status: MigrationCompleted},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}
	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatalf("read root state: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	reg, ok := raw["migrations"].(map[string]any)
	if !ok {
		t.Fatalf("migrations = %v, want a mapping", raw["migrations"])
	}
	entry, ok := reg["runtime_home"].(map[string]any)
	if !ok || entry["status"] != "completed" {
		t.Fatalf("runtime_home entry = %v, want status completed", reg["runtime_home"])
	}
}

func TestMigrationAbsentIsPending(t *testing.T) {
	var s RootState
	if m, ok := s.Migration("runtime_home"); ok || m.Status != "" {
		t.Fatalf("Migration() = %+v, %v; want zero and false", m, ok)
	}
}

func TestSetMigrationInitializesRegistry(t *testing.T) {
	var s RootState
	s.SetMigration("runtime_home", Migration{Status: MigrationDismissed})
	if s.Migrations["runtime_home"].Status != MigrationDismissed {
		t.Fatalf("Migrations = %+v", s.Migrations)
	}
}

func TestUpdateRootStatePreservesOtherMigrations(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Migrations: map[string]Migration{
		"other": {Status: MigrationCompleted},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpdateRootState(func(s *RootState) {
		s.SetMigration("runtime_home", Migration{Status: MigrationDismissed})
	}); err != nil {
		t.Fatalf("UpdateRootState: %v", err)
	}
	got, _ := ReadRootState()
	if got.Migrations["other"].Status != MigrationCompleted {
		t.Errorf("other migration lost: %+v", got.Migrations)
	}
	if got.Migrations["runtime_home"].Status != MigrationDismissed {
		t.Errorf("runtime_home not set: %+v", got.Migrations)
	}
}

func TestReadRootState_ParseError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatalf("create state root: %v", err)
	}
	if err := os.WriteFile(rootStateFile(), []byte("runtime_home_migration: [unterminated"), 0o600); err != nil {
		t.Fatalf("write corrupt root state: %v", err)
	}

	if _, err := ReadRootState(); err == nil {
		t.Fatal("ReadRootState() error = nil, want a parse error")
	}
}

// TestReadRootState_NonNotFoundReadError covers a read failure that is not
// "not exist": a directory at the state file path.
func TestReadRootState_NonNotFoundReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := os.MkdirAll(rootStateFile(), 0o700); err != nil {
		t.Fatalf("mkdir at state file path: %v", err)
	}

	if _, err := ReadRootState(); err == nil {
		t.Fatal("ReadRootState() error = nil, want a non-not-exist read error")
	}
}

// TestWriteRootState_MarshalError covers the marshal failure via the seam: the
// concrete RootState shape cannot make yaml.Marshal fail.
func TestWriteRootState_MarshalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	wantErr := errors.New("marshal boom")
	orig := marshalRootState
	marshalRootState = func(any) ([]byte, error) { return nil, wantErr }
	t.Cleanup(func() { marshalRootState = orig })

	if err := WriteRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); !errors.Is(
		err,
		wantErr,
	) {
		t.Fatalf("WriteRootState() error = %v, want %v", err, wantErr)
	}
}

// TestWriteRootState_MkdirAllError covers the state-root creation failure: a
// regular file occupies the directory's path.
func TestWriteRootState_MkdirAllError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	root := stateRoot()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, root, "not a directory")

	if err := WriteRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); err == nil {
		t.Fatal("WriteRootState() error = nil, want MkdirAll failure")
	}
}

// TestWriteRootState_RenameErrorCleansTemp covers the rename failure: a
// directory occupies the final state file path. The unique temp file must be
// cleaned up.
func TestWriteRootState_RenameErrorCleansTemp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := os.MkdirAll(rootStateFile(), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WriteRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); err == nil {
		t.Fatal("WriteRootState() error = nil, want rename failure")
	}
	assertNoRootStateTempFiles(t)
}

// TestWriteRootState_ConcurrentWritesDoNotCollide is the regression guard for
// issue #3: the launcher-global state root is shared by every project, so two
// concurrent writers must not collide on the temporary file name. With a fixed
// temp name, the first rename removed it and the second failed, aborting a
// dismissing session.
func TestWriteRootState_ConcurrentWritesDoNotCollide(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			errs <- WriteRootState(RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}})
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent WriteRootState() error = %v", err)
		}
	}
	if _, err := ReadRootState(); err != nil {
		t.Fatalf("ReadRootState() after concurrent writes: %v", err)
	}
}

// TestUpdateRootState_ReadError covers the read failure propagating out of
// UpdateRootState: a corrupt state file makes the read fail before mutate runs.
func TestUpdateRootState_ReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatalf("create state root: %v", err)
	}
	if err := os.WriteFile(rootStateFile(), []byte("runtime_home_migration: [unterminated"), 0o600); err != nil {
		t.Fatalf("write corrupt root state: %v", err)
	}

	mutated := false
	if err := UpdateRootState(func(*RootState) { mutated = true }); err == nil {
		t.Fatal("UpdateRootState() error = nil, want a read error")
	}
	if mutated {
		t.Error("UpdateRootState ran mutate despite the read failure")
	}
}

func TestRootStateAgentCheckRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Agents: map[string]AgentCheckState{
		"opencode": {LastChecked: time.Unix(100, 0).UTC(), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	c := got.AgentCheck("opencode")
	if c.LatestVersion != "2.0.0" || c.LastChecked.Unix() != 100 {
		t.Fatalf("AgentCheck = %+v", c)
	}
}

func TestRootStateAgentCheckMissingIsZero(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if c := got.AgentCheck("opencode"); !c.LastChecked.IsZero() || c.LatestVersion != "" {
		t.Fatalf("missing AgentCheck = %+v, want zero", c)
	}
}

func TestAgentCheckDueForCheck(t *testing.T) {
	now := time.Unix(1000, 0)
	if !(AgentCheckState{}).DueForCheck(now) {
		t.Error("zero LastChecked must be due")
	}
	if (AgentCheckState{LastChecked: now.Add(-AgentCheckInterval + time.Minute)}).DueForCheck(now) {
		t.Error("recent check must not be due")
	}
	if !(AgentCheckState{LastChecked: now.Add(-AgentCheckInterval)}).DueForCheck(now) {
		t.Error("check at the interval boundary must be due")
	}
}

func TestRootStateAgentCheckWireKeys(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Agents: map[string]AgentCheckState{
		"opencode": {LastChecked: time.Unix(100, 0).UTC(), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}
	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatalf("read root state: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agents, ok := raw["agents"].(map[string]any)
	if !ok {
		t.Fatalf("agents = %v, want a mapping", raw["agents"])
	}
	entry, ok := agents["opencode"].(map[string]any)
	if !ok {
		t.Fatalf("opencode entry = %v, want a mapping", agents["opencode"])
	}
	if entry["latest_version"] != "2.0.0" {
		t.Errorf("latest_version = %v, want 2.0.0", entry["latest_version"])
	}
	if _, ok := entry["last_checked"]; !ok {
		t.Error("last_checked key missing")
	}
}

func TestSetAgentCheckInitializesRegistry(t *testing.T) {
	var s RootState
	s.SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
	if s.Agents["opencode"].LatestVersion != "2.0.0" {
		t.Fatalf("Agents = %+v", s.Agents)
	}
}

func assertNoRootStateTempFiles(t *testing.T) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(stateRoot(), ".state.yaml.*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover root state temp files = %v, want none", matches)
	}
}
