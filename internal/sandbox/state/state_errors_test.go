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

func TestRemoveHomeState_LegacyRemoveError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "legacyerrproj"

	// A non-empty directory at the legacy state file path makes os.Remove
	// fail with a non-"not exist" error.
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(filepath.Join(sdir, "state.yaml", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHomeState(Key{Slug: slug, Agent: ""}); err == nil {
		t.Fatal("expected error when the legacy state file cannot be removed")
	}
}

func TestRemoveHomeStateLockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	wantErr := errors.New("mkdir boom")
	orig := mkdirHomeLock
	mkdirHomeLock = func(string, os.FileMode) error { return wantErr }
	t.Cleanup(func() { mkdirHomeLock = orig })

	if err := RemoveHomeState(Key{Slug: "proj", Agent: "opencode"}); !errors.Is(err, wantErr) {
		t.Fatalf("RemoveHomeState() error = %v, want %v", err, wantErr)
	}
}
