package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateFile(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "testproj-aBc1234D"
	f := stateFile(slug)
	if filepath.Base(f) != "state.yaml" {
		t.Errorf("expected state.yaml basename, got %q", filepath.Base(f))
	}
	if !filepath.IsAbs(f) {
		t.Error("expected absolute path")
	}
}

func TestReadState_NoFileReturnsErrStateNotFound(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")

	slug := "nonexistentproj-xyz"
	result, err := ReadState(slug)
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound for missing file, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for missing file, got: %v", result)
	}
}

func TestWriteAndReadState(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
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

	fpath := filepath.Join(StateDir, slug, "state.yaml")
	if _, err := os.Stat(fpath); err != nil {
		t.Errorf("state file should exist at %s: %v", fpath, err)
	}
}

func TestWriteStateCreatesDirectory(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "newproj-a"

	err := WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	dir := filepath.Join(StateDir, slug)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("state dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestReadState_CorruptedYAML(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "corruptproj"

	sdir := filepath.Join(StateDir, slug)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode-msb")
	slug := "myproj"

	WriteState(slug, HomeState{HomeVolume: "vol", ImageDigest: "d1"})

	statePath := filepath.Join(StateDir, slug, "state.yaml")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state file should exist before RemoveState")
	}

	RemoveState(slug)

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file should be removed, but still exists")
	}
	stDir := filepath.Join(StateDir, slug)
	if _, err := os.Stat(stDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be removed, but still exists")
	}
}

func TestWriteAndReadState_EnvSecretRoundTrip(t *testing.T) {
	old := StateDir
	defer func() { StateDir = old }()

	StateDir = t.TempDir() + "/opencode-msb"
	slug := "myproj-abc123"

	input := HomeState{
		HomeVolume:  "opencode-msb-home-myproj",
		ImageDigest: "sha256:feedface",
		EnvState: EnvState{
			Hash:  "sha256:envhash",
			Names: []string{"DB_URL", "API_KEY"},
		},
		SecretState: SecretState{
			Hash:  "sha256:secrethash",
			Names: []string{"db_password", "api_secret"},
		},
	}

	if err := WriteState(slug, input); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	result, err := ReadState(slug)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if result.EnvState.Hash != input.EnvState.Hash {
		t.Errorf("EnvState.Hash = %q, want %q", result.EnvState.Hash, input.EnvState.Hash)
	}
	if len(result.EnvState.Names) != len(input.EnvState.Names) {
		t.Fatalf("EnvState.Names length = %d, want %d", len(result.EnvState.Names), len(input.EnvState.Names))
	}
	for i, want := range input.EnvState.Names {
		if result.EnvState.Names[i] != want {
			t.Errorf("EnvState.Names[%d] = %q, want %q", i, result.EnvState.Names[i], want)
		}
	}
	if result.SecretState.Hash != input.SecretState.Hash {
		t.Errorf("SecretState.Hash = %q, want %q", result.SecretState.Hash, input.SecretState.Hash)
	}
	if len(result.SecretState.Names) != len(input.SecretState.Names) {
		t.Fatalf("SecretState.Names length = %d, want %d", len(result.SecretState.Names), len(input.SecretState.Names))
	}
	for i, want := range input.SecretState.Names {
		if result.SecretState.Names[i] != want {
			t.Errorf("SecretState.Names[%d] = %q, want %q", i, result.SecretState.Names[i], want)
		}
	}
}

func TestHomeState_ZeroEnvSecretOmitted(t *testing.T) {
	old := StateDir
	defer func() { StateDir = old }()

	StateDir = t.TempDir() + "/opencode-msb"
	slug := "emptyproj"

	err := WriteState(slug, HomeState{
		HomeVolume:  "vol",
		ImageDigest: "d1",
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(StateDir, slug, "state.yaml"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}

	content := string(data)
	if strings.Contains(content, "env_state") {
		t.Error("expected env_state to be omitted from YAML when zero")
	}
	if strings.Contains(content, "secret_state") {
		t.Error("expected secret_state to be omitted from YAML when zero")
	}
}
