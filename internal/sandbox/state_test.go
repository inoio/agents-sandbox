package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateFile(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()
	stateDirSuffix = t.TempDir() + "/opencode-msb"

	slug := "testproj-aBc1234D"
	f := StateFile(slug)
	if filepath.Base(f) != "state.yaml" {
		t.Errorf("expected state.yaml basename, got %q", filepath.Base(f))
	}
	if !filepath.IsAbs(f) {
		t.Error("expected absolute path")
	}
}

func TestReadState_NoFileReturnsNil(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()

	stateDirSuffix = t.TempDir() + "/opencode-msb"

	slug := "nonexistentproj-xyz"
	_, err := ReadState(slug)
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound for missing file, got: %v", err)
	}
}

func TestWriteAndReadState(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()

	stateDirSuffix = t.TempDir() + "/opencode-msb"
	slug := "myproj-aBc1234D"
	digest := "sha256:deadbeef1234"

	err := WriteState(slug, HomeState{
		HomeVolume:  "opencode-msb-home-myproj-20260806T143022",
		ImageDigest: digest,
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	result, err := ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if result.HomeVolume != "opencode-msb-home-myproj-20260806T143022" {
		t.Errorf("HomeVolume = %q, want %q", result.HomeVolume, "opencode-msb-home-myproj-20260806T143022")
	}
	if result.ImageDigest != digest {
		t.Errorf("ImageDigest = %q, want %q", result.ImageDigest, digest)
	}

	fpath := filepath.Join(stateDirSuffix, slug, "state.yaml")
	if _, err := os.Stat(fpath); err != nil {
		t.Errorf("state file should exist at %s: %v", fpath, err)
	}
}

func TestWriteStateCreatesDirectory(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()

	stateDirSuffix = t.TempDir() + "/opencode-msb"
	slug := "newproj-a"

	err := WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	dir := filepath.Join(stateDirSuffix, slug)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("state dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestReadState_CorruptedYAML(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()

	stateDirSuffix = t.TempDir() + "/opencode-msb"
	slug := "corruptproj"

	sdir := filepath.Join(stateDirSuffix, slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadState(slug)
	if err == nil {
		t.Fatal("expected error for corrupted YAML")
	}
}

func TestRemoveState(t *testing.T) {
	old := stateDirSuffix
	defer func() { stateDirSuffix = old }()

	stateDirSuffix = t.TempDir() + "/opencode-msb"
	slug := "myproj"

	WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})

	statePath := filepath.Join(stateDirSuffix, slug, "state.yaml")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state file should exist before RemoveState")
	}

	RemoveState(slug)

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file should be removed, but still exists")
	}
	stateDir := filepath.Join(stateDirSuffix, slug)
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be removed, but still exists")
	}
}
