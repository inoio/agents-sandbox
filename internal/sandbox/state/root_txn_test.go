package state

import (
	"errors"
	"os"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestWithRootStateNonBlockingDefersWhenHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	held, ok, err := LockRootState(true)
	if err != nil || !ok {
		t.Fatalf("hold: ok=%v err=%v", ok, err)
	}
	defer held()
	ran := false
	ok, err = WithRootState(false, func(*RootTxn) error { ran = true; return nil })
	if err != nil || ok || ran {
		t.Fatalf("ok=%v err=%v ran=%v, want deferred", ok, err, ran)
	}
}

func TestWithRootStateCommitsState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ok, err := WithRootState(true, func(tx *RootTxn) error {
		tx.State().SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
		return tx.Commit()
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	got, _ := ReadRootState()
	if got.AgentCheck("opencode").LatestVersion != "2.0.0" {
		t.Fatalf("not committed: %+v", got)
	}
}

func TestWithRootStateMissingIsNotExisting(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ok, err := WithRootState(true, func(tx *RootTxn) error {
		if tx.Exists() {
			t.Error("Exists() = true, want false for a missing file")
		}
		return nil
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestWithRootStatePresentExistsTrue(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := WriteRootState(RootState{Agents: map[string]AgentCheckState{
		"opencode": {LatestVersion: "1.0.0"},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err := WithRootState(true, func(tx *RootTxn) error {
		if !tx.Exists() {
			t.Error("Exists() = false, want true for a present file")
		}
		if tx.State().AgentCheck("opencode").LatestVersion != "1.0.0" {
			t.Errorf("state not loaded: %+v", tx.State())
		}
		return nil
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestWithRootStateCorruptReturnsErrorWithoutRunning(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootStateFile(), []byte("migrations: [unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	_, err := WithRootState(true, func(*RootTxn) error { ran = true; return nil })
	if err == nil {
		t.Fatal("err = nil, want a parse error")
	}
	if ran {
		t.Fatal("fn ran on a corrupt document")
	}
}

func TestWithRootStateCallbackErrorPropagates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("boom")
	ok, err := WithRootState(true, func(*RootTxn) error { return wantErr })
	if !ok || !errors.Is(err, wantErr) {
		t.Fatalf("ok=%v err=%v, want ok=true and %v", ok, err, wantErr)
	}
}

func TestWithRootStateLockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirRootLock
	mkdirRootLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirRootLock = orig })

	ok, err := WithRootState(true, func(*RootTxn) error { return nil })
	if ok || !errors.Is(err, wantErr) {
		t.Fatalf("ok=%v err=%v, want ok=false and %v", ok, err, wantErr)
	}
}

func TestWithRootStateStatError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("stat boom")
	orig := statRootStateFile
	statRootStateFile = func(string) (os.FileInfo, error) { return nil, wantErr }
	t.Cleanup(func() { statRootStateFile = orig })

	ok, err := WithRootState(true, func(*RootTxn) error { return nil })
	if !ok || !errors.Is(err, wantErr) {
		t.Fatalf("ok=%v err=%v, want ok=true and %v", ok, err, wantErr)
	}
}

func TestRootTxnCommitError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("marshal boom")
	orig := marshalRootState
	marshalRootState = func(any) ([]byte, error) { return nil, wantErr }
	t.Cleanup(func() { marshalRootState = orig })

	ok, err := WithRootState(true, func(tx *RootTxn) error { return tx.Commit() })
	if !ok || !errors.Is(err, wantErr) {
		t.Fatalf("ok=%v err=%v, want ok=true and %v", ok, err, wantErr)
	}
}
