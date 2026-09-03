package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

func TestStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "testproj-aBc1234D", Agent: "opencode"}
	f := stateFile(k)
	if filepath.Base(f) != "state.yaml" {
		t.Errorf("expected state.yaml basename, got %q", filepath.Base(f))
	}
	if !filepath.IsAbs(f) {
		t.Error("expected absolute path")
	}
}

func TestReadState_NoFileReturnsErrStateNotFound(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	k := Key{Slug: "nonexistentproj-xyz", Agent: "opencode"}
	result, err := ReadState(k)
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound for missing file, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for missing file, got: %v", result)
	}
}

func TestWriteAndReadState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "myproj-aBc1234D", Agent: "opencode"}
	digest := "sha256:deadbeef1234"

	err := WriteState(k, HomeState{
		HomeVolume:  "opencode-sandbox-home-myproj-20260806T143022",
		ImageDigest: digest,
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	result, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if result.HomeVolume != "opencode-sandbox-home-myproj-20260806T143022" {
		t.Errorf("HomeVolume = %q, want %q", result.HomeVolume, "opencode-sandbox-home-myproj-20260806T143022")
	}
	if result.ImageDigest != digest {
		t.Errorf("ImageDigest = %q, want %q", result.ImageDigest, digest)
	}

	fpath := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent, "state.yaml")
	if _, err := os.Stat(fpath); err != nil {
		t.Errorf("state file should exist at %s: %v", fpath, err)
	}
}

func TestWriteStateCreatesDirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "newproj-a", Agent: "opencode"}

	err := WriteState(k, HomeState{HomeVolume: "vol", ImageDigest: "d1"})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	dir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("state dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestReadState_CorruptedYAML(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "corruptproj", Agent: "opencode"}

	sdir := KeyDir(k)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "not: valid: yaml: [")

	_, err := ReadState(k)
	if err == nil {
		t.Fatal("expected error for corrupted YAML")
	}
}

func TestNewHomeState(t *testing.T) {
	st := NewHomeState("vol", "sha256:digest")
	if st.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want vol", st.HomeVolume)
	}
	if st.ImageDigest != "sha256:digest" {
		t.Errorf("ImageDigest = %q, want sha256:digest", st.ImageDigest)
	}
	if st.EnvState.Hash != "" || len(st.EnvState.Names) != 0 {
		t.Errorf("EnvState should be zeroed, got %+v", st.EnvState)
	}
	if st.SecretState.Hash != "" || len(st.SecretState.Names) != 0 {
		t.Errorf("SecretState should be zeroed, got %+v", st.SecretState)
	}
}

func TestRemoveState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "myproj", Agent: "opencode"}

	WriteState(k, HomeState{HomeVolume: "vol", ImageDigest: "d1"})

	statePath := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent, "state.yaml")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("state file should exist before RemoveState")
	}

	RemoveState(k)

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state file should be removed, but still exists")
	}
	stDir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
	if _, err := os.Stat(stDir); !os.IsNotExist(err) {
		t.Errorf("state dir should be removed, but still exists")
	}
}

func TestRemoveState_LegacySlugOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "legacyproj"

	// Legacy agent-less state lives directly under the slug dir.
	sdir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, sdir, "state.yaml", "home_volume: vol\n")

	// A sibling per-agent state dir and the per-project claims dir must survive.
	agentDir := filepath.Join(sdir, "opencode")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	claimsDir := filepath.Join(sdir, "claims")
	if err := os.MkdirAll(claimsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := RemoveState(Key{Slug: slug, Agent: ""}); err != nil {
		t.Fatalf("RemoveState legacy: %v", err)
	}

	// Legacy state file removed, sibling state and claims survive.
	if _, err := os.Stat(filepath.Join(sdir, "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("legacy state file should be removed")
	}
	if _, err := os.Stat(agentDir); err != nil {
		t.Errorf("sibling per-agent dir should survive: %v", err)
	}
	if _, err := os.Stat(claimsDir); err != nil {
		t.Errorf("claims dir should survive: %v", err)
	}
}

func TestRemoveState_LegacyMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "missingproj", Agent: ""}
	if err := RemoveState(k); err != nil {
		t.Fatalf("RemoveState on missing legacy state should be a no-op, got: %v", err)
	}
}

func TestWriteAndReadState_EnvSecretRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "myproj-abc123", Agent: "opencode"}

	input := HomeState{
		HomeVolume:  "opencode-sandbox-home-myproj",
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

	if err := WriteState(k, input); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	result, err := ReadState(k)
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
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "emptyproj", Agent: "opencode"}

	err := WriteState(k, HomeState{
		HomeVolume:  "vol",
		ImageDigest: "d1",
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent, "state.yaml"))
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

func TestWriteAndReadState_MountRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := Key{Slug: "mountproj-abc123", Agent: "opencode"}

	input := HomeState{
		HomeVolume:  "opencode-sandbox-home-mountproj",
		ImageDigest: "sha256:feedface",
		MountState: MountState{
			Hash:  "sha256:mounthash",
			Names: []string{"/home/dev/.m2"},
		},
	}
	if err := WriteState(k, input); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	result, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if result.MountState.Hash != input.MountState.Hash {
		t.Errorf("MountState.Hash = %q, want %q", result.MountState.Hash, input.MountState.Hash)
	}
	if len(result.MountState.Names) != 1 || result.MountState.Names[0] != "/home/dev/.m2" {
		t.Errorf("MountState.Names = %v, want [/home/dev/.m2]", result.MountState.Names)
	}
}

func TestStateKeyScopesDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := configpaths.Get().UserStateDir()
	k := Key{Slug: "proj-aBc1234D", Agent: "pi"}
	if err := WriteState(k, HomeState{HomeVolume: "vol", ImageDigest: "d1"}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	// State lands under <root>/<slug>/<agent>/, not <root>/<slug>/.
	if _, err := os.Stat(filepath.Join(root, "proj-aBc1234D", "pi", "state.yaml")); err != nil {
		t.Errorf("expected state under slug/agent dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "proj-aBc1234D", "state.yaml")); !os.IsNotExist(err) {
		t.Errorf("expected no state under legacy slug dir: %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol" || got.ImageDigest != "d1" {
		t.Errorf("ReadState = %+v, want vol/d1", got)
	}
	// A different agent has no state.
	if _, err := ReadState(Key{Slug: "proj-aBc1234D", Agent: "opencode"}); err == nil {
		t.Error("expected ErrStateNotFound for the other agent")
	}
}

func TestKeyDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	got := KeyDir(Key{Slug: "s-abc", Agent: "opencode"})
	want := filepath.Join(configpaths.Get().UserStateDir(), "s-abc", "opencode")
	if got != want {
		t.Errorf("KeyDir = %q, want %q", got, want)
	}
}
