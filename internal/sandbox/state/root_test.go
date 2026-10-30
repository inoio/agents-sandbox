package state

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

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
	if err := writeRootState(RootState{Migrations: map[string]Migration{
		"runtime_home": {Status: MigrationCopying, Copied: []string{"a"}, Excluded: []string{"b"}},
	}}); err != nil {
		t.Fatalf("writeRootState: %v", err)
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
	if err := writeRootState(RootState{Migrations: map[string]Migration{
		"runtime_home": {Status: MigrationCompleted},
	}}); err != nil {
		t.Fatalf("writeRootState: %v", err)
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

func TestMigrationAcknowledgedRoundTrips(t *testing.T) {
	if MigrationAcknowledged != "acknowledged" {
		t.Fatalf("MigrationAcknowledged = %q, want \"acknowledged\"", MigrationAcknowledged)
	}
	rs := &RootState{}
	rs.SetMigration("legacy_runtime_notice", Migration{Status: MigrationAcknowledged})
	got, ok := rs.Migration("legacy_runtime_notice")
	if !ok || got.Status != MigrationAcknowledged {
		t.Fatalf("round-tripped migration = %+v (ok=%v), want acknowledged", got, ok)
	}
}

func TestWithRootStatePreservesOtherMigrations(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{Migrations: map[string]Migration{
		"other": {Status: MigrationCompleted},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := WithRootState(true, func(tx *RootTxn) error {
		tx.State().SetMigration("runtime_home", Migration{Status: MigrationDismissed})
		return tx.Commit()
	}); err != nil {
		t.Fatalf("WithRootState: %v", err)
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

	if _, err := ReadRootState(); !errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("ReadRootState() error = %v, want ErrStateUnparsable", err)
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

	if err := writeRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); !errors.Is(
		err,
		wantErr,
	) {
		t.Fatalf("writeRootState() error = %v, want %v", err, wantErr)
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

	if err := writeRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); err == nil {
		t.Fatal("writeRootState() error = nil, want MkdirAll failure")
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

	if err := writeRootState(
		RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}},
	); err == nil {
		t.Fatal("writeRootState() error = nil, want rename failure")
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
			errs <- writeRootState(RootState{Migrations: map[string]Migration{"runtime_home": {Status: MigrationDismissed}}})
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent writeRootState() error = %v", err)
		}
	}
	if _, err := ReadRootState(); err != nil {
		t.Fatalf("ReadRootState() after concurrent writes: %v", err)
	}
}

func TestRootStateAgentCheckRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{Updates: Updates{Agents: map[string]AgentCheckState{
		"opencode": {LastChecked: time.Unix(100, 0).UTC(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("writeRootState: %v", err)
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
	if err := writeRootState(RootState{Updates: Updates{Agents: map[string]AgentCheckState{
		"opencode": {LastChecked: time.Unix(100, 0).UTC(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("writeRootState: %v", err)
	}
	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatalf("read root state: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	updates, ok := raw["updates"].(map[string]any)
	if !ok {
		t.Fatalf("updates = %v, want a mapping", raw["updates"])
	}
	agents, ok := updates["agents"].(map[string]any)
	if !ok {
		t.Fatalf("agents = %v, want a mapping", updates["agents"])
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
	if s.Updates.Agents["opencode"].LatestVersion != "2.0.0" {
		t.Fatalf("Updates.Agents = %+v", s.Updates.Agents)
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

func TestRootStateAdoptedHomeVolumeRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{AdoptedHomeVolumes: map[string]AdoptedHomeVolume{
		adoptedHomeVolumeKey("widget-aaa", "opencode"): {CheckoutSlug: "widget-bbb", Volume: "home-1"},
	}}); err != nil {
		t.Fatalf("writeRootState: %v", err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	claim, ok := got.AdoptedHomeVolume("widget-aaa", "opencode")
	if !ok || claim.CheckoutSlug != "widget-bbb" || claim.Volume != "home-1" {
		t.Fatalf("claim = %+v (ok %v)", claim, ok)
	}
	if _, ok := got.AdoptedHomeVolume("widget-aaa", "pi"); ok {
		t.Error("claim leaked across agents")
	}
}

func TestRootStateForgetAdoptedHomeVolumeMatchesCheckoutAndVolume(t *testing.T) {
	var s RootState
	s.ClaimAdoptedHomeVolume("r", "opencode", "checkout-a", "vol-a")
	s.ClaimAdoptedHomeVolume("r", "pi", "checkout-a", "vol-b")
	s.ClaimAdoptedHomeVolume("r2", "opencode", "checkout-b", "vol-a")

	s.ForgetAdoptedHomeVolume("checkout-a", "vol-a")

	if _, ok := s.AdoptedHomeVolume("r", "opencode"); ok {
		t.Error("matching claim not removed")
	}
	if _, ok := s.AdoptedHomeVolume("r", "pi"); !ok {
		t.Error("another agent's claim of the same checkout removed")
	}
	if _, ok := s.AdoptedHomeVolume("r2", "opencode"); !ok {
		t.Error("the same volume under another checkout removed")
	}
}

// TestClearAdoptedHomeVolumeAbsentIsNoOp covers the write-skipping guard: clearing
// a claim that is not there must not create a root state file where none existed.
func TestClearAdoptedHomeVolumeAbsentIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := ClearAdoptedHomeVolume("checkout-a", "vol-a"); err != nil {
		t.Fatalf("ClearAdoptedHomeVolume: %v", err)
	}
	if _, err := os.Stat(rootStateFile()); !os.IsNotExist(err) {
		t.Errorf("ClearAdoptedHomeVolume created a root state file (stat err %v)", err)
	}
}

// TestClearAdoptedHomeVolumeEmptyVolumeIsNoOp covers the guard for a volume-less
// call: a recovered volume string that is empty has no claim to retire, so the
// transaction must not run at all.
func TestClearAdoptedHomeVolumeEmptyVolumeIsNoOp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := ClearAdoptedHomeVolume("checkout-a", ""); err != nil {
		t.Fatalf("ClearAdoptedHomeVolume: %v", err)
	}
	if _, err := os.Stat(rootStateFile()); !os.IsNotExist(err) {
		t.Errorf("ClearAdoptedHomeVolume created a root state file (stat err %v)", err)
	}
}

// TestClearAdoptedHomeVolumeCommitError covers the persistence failure: a claim
// that is present is removed from the document, so the change is written and a
// write failure is returned rather than silently dropped.
func TestClearAdoptedHomeVolumeCommitError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := writeRootState(RootState{AdoptedHomeVolumes: map[string]AdoptedHomeVolume{
		adoptedHomeVolumeKey("widget-aaa", "opencode"): {CheckoutSlug: "checkout-a", Volume: "vol-a"},
	}}); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	wantErr := errors.New("marshal boom")
	orig := marshalRootState
	marshalRootState = func(any) ([]byte, error) { return nil, wantErr }
	t.Cleanup(func() { marshalRootState = orig })

	if err := ClearAdoptedHomeVolume("checkout-a", "vol-a"); !errors.Is(err, wantErr) {
		t.Fatalf("ClearAdoptedHomeVolume() error = %v, want %v", err, wantErr)
	}
}
