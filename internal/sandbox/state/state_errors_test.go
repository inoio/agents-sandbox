package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestReadState_NonNotFoundReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "dirslate-a", Agent: "opencode"}

	// A directory at the state file path makes os.ReadFile fail with a
	// non-"not exist" error, distinct from ErrStateNotFound.
	if err := os.MkdirAll(stateFile(k), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadState(k); err == nil {
		t.Fatal("expected error when state file path is a directory")
	}
}

func TestWriteState_MkdirAllError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "blockedstate-a", Agent: "opencode"}

	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	// A file where the slug dir should live makes MkdirAll fail.
	testutil.WritePath(t, filepath.Join(stateRoot(), k.Slug), "not a directory")

	if err := writeState(k, HomeState{HomeVolume: "vol"}); err == nil {
		t.Fatal("expected error when the state dir cannot be created")
	}
}

func TestWriteState_RenameErrorCleansTemp(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "renproj-a", Agent: "opencode"}

	sdir := KeyDir(k)
	// A directory at the final state file path makes os.Rename fail.
	if err := os.MkdirAll(filepath.Join(sdir, "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeState(k, HomeState{HomeVolume: "vol"}); err == nil {
		t.Fatal("expected error when the state file cannot be renamed")
	}

	// The unique temp file should have been cleaned up after the failed rename.
	matches, err := filepath.Glob(filepath.Join(sdir, ".state.yaml.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files = %v, want none", matches)
	}
}

func TestRemoveHomeStateIf_LegacyRemoveError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the read-only directory permission this test relies on")
	}
	configpaths.WithMockConfigPaths(t)
	slug := "legacyerrproj"

	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("home_volume: vol\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A read-only slug dir lets the document be read but makes os.Remove fail,
	// so the removal error branch is hit rather than an unreadable-document one.
	if err := os.Chmod(sdir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sdir, 0o700) })

	if err := RemoveHomeStateIf(Key{Slug: slug, Agent: ""}, removeAlways); err == nil {
		t.Fatal("expected error when the legacy state file cannot be removed")
	}
}

func TestRemoveHomeStateIfLockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := RemoveHomeStateIf(Key{Slug: "proj", Agent: "opencode"}, removeAlways); !errors.Is(err, wantErr) {
		t.Fatalf("RemoveHomeStateIf() error = %v, want %v", err, wantErr)
	}
}
