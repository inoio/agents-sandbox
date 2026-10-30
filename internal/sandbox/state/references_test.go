package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

func TestFormatKeys(t *testing.T) {
	got := FormatKeys([]Key{
		{Slug: "legacy-aBc1234D"},
		{Slug: "proj-aBc1234D", Agent: "opencode"},
	})
	if got != "legacy-aBc1234D, proj-aBc1234D/opencode" {
		t.Fatalf("FormatKeys = %q, want %q", got, "legacy-aBc1234D, proj-aBc1234D/opencode")
	}
}

func TestHomeVolumeReferencesScansBothLayouts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	perAgent := Key{Slug: "proj-aBc1234D", Agent: "opencode"}
	legacy := Key{Slug: "legacy-aBc1234D"}
	vol := "agents-sandbox-home-proj-aBc1234D-opencode-20260812T123456"
	if err := writeState(perAgent, HomeState{HomeVolume: vol}); err != nil {
		t.Fatalf("write per-agent state: %v", err)
	}
	if err := writeState(legacy, HomeState{HomeVolume: vol}); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	references, err := HomeVolumeReferences()
	if err != nil {
		t.Fatalf("HomeVolumeReferences: %v", err)
	}
	got := map[Key]bool{}
	for _, key := range references[vol] {
		got[key] = true
	}
	if len(got) != 2 || !got[perAgent] || !got[legacy] {
		t.Fatalf("references[%q] = %v, want the per-agent and legacy keys", vol, references[vol])
	}
}

func TestHomeVolumeReferencesProtectsPendingRecoveredVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := Key{Slug: "proj-aBc1234D", Agent: "opencode"}
	vol := "agents-sandbox-home-legacy-aBc1234D-opencode-20260812T123456"
	if err := writeState(key, HomeState{RecoveredHomeVolume: vol}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	references, err := HomeVolumeReferences()
	if err != nil {
		t.Fatalf("HomeVolumeReferences: %v", err)
	}
	keys := references[vol]
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("references[%q] = %v, want [%v]", vol, keys, key)
	}
}

func TestHomeVolumeReferencesSkipsStateWithoutAVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := Key{Slug: "proj-aBc1234D", Agent: "opencode"}
	if err := writeState(key, HomeState{ImageDigest: "sha256:abc"}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	references, err := HomeVolumeReferences()
	if err != nil {
		t.Fatalf("HomeVolumeReferences: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("references = %v, want empty (state records no home volume)", references)
	}
}

func TestHomeVolumeReferencesIgnoresRootState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{Migrations: map[string]Migration{
		"runtime_home": {Status: MigrationCompleted},
	}}); err != nil {
		t.Fatalf("write root state: %v", err)
	}

	references, err := HomeVolumeReferences()
	if err != nil {
		t.Fatalf("HomeVolumeReferences: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("references = %v, want empty (root state is not a project state)", references)
	}
}

// missingStatePaths overrides only UserStateDir so the state root is genuinely
// absent: the shared mock recreates the directory on every UserStateDir call, so
// it cannot exercise the "user deleted launcher state" path on its own.
type missingStatePaths struct {
	configpaths.ConfigPaths

	stateDir string
}

func (m missingStatePaths) UserStateDir() string { return m.stateDir }

func TestHomeVolumeReferencesEmptyWhenNoStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	base := configpaths.Get()
	configpaths.Get = func() configpaths.ConfigPaths {
		return missingStatePaths{ConfigPaths: base, stateDir: filepath.Join(t.TempDir(), "absent")}
	}

	references, err := HomeVolumeReferences()
	if err != nil {
		t.Fatalf("HomeVolumeReferences: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("references = %v, want empty", references)
	}
}

func TestHomeVolumeReferencesErrorsOnUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	configpaths.WithMockConfigPaths(t)
	blocked := filepath.Join(configpaths.Get().UserStateDir(), "blocked")
	if err := os.MkdirAll(blocked, 0o000); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o750) })
	if _, err := HomeVolumeReferences(); err == nil {
		t.Fatal("HomeVolumeReferences() error = nil, want a walk error")
	}
}

func TestHomeVolumeReferencesErrorsOnCorruptStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := KeyDir(Key{Slug: "proj-aBc1234D", Agent: "opencode"})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("home_volume: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, err := HomeVolumeReferences(); err == nil {
		t.Fatal("HomeVolumeReferences() error = nil, want a parse failure to fail closed")
	}
}

func TestImageReferencesScansPerAgentStates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	key := Key{Slug: "proj-aBc1234D", Agent: "opencode"}
	if err := writeState(key, HomeState{ImageDigest: "sha256:abc"}); err != nil {
		t.Fatalf("write per-agent state: %v", err)
	}

	references, err := ImageReferences()
	if err != nil {
		t.Fatalf("ImageReferences: %v", err)
	}
	ref := naming.RunnerImageRef(key.Slug, key.Agent)
	keys := references[ref]
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("references[%q] = %v, want [%v]", ref, keys, key)
	}
	// A project also owns the pre-change prefixed reference an earlier version's
	// image sits under, so a legacy image is not removable while the state exists.
	legacy := naming.DockerImageRef(key.Slug, key.Agent)
	legacyKeys := references[legacy]
	if len(legacyKeys) != 1 || legacyKeys[0] != key {
		t.Fatalf("references[%q] = %v, want [%v]", legacy, legacyKeys, key)
	}
}

func TestImageReferencesIgnoresLegacyAgentlessState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := Key{Slug: "legacy-aBc1234D"}
	if err := writeState(legacy, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	references, err := ImageReferences()
	if err != nil {
		t.Fatalf("ImageReferences: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("references = %v, want empty (no per-agent runner ref for a legacy key)", references)
	}
}

func TestImageReferencesErrorsOnUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0 does not block directory reads")
	}
	configpaths.WithMockConfigPaths(t)
	blocked := filepath.Join(configpaths.Get().UserStateDir(), "blocked")
	if err := os.MkdirAll(blocked, 0o000); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o750) })
	if _, err := ImageReferences(); err == nil {
		t.Fatal("ImageReferences() error = nil, want a walk error")
	}
}

func TestImageReferencesErrorsOnCorruptStateFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	dir := KeyDir(Key{Slug: "proj-aBc1234D", Agent: "opencode"})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("image_digest: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, err := ImageReferences(); err == nil {
		t.Fatal("ImageReferences() error = nil, want a parse failure to fail closed")
	}
}
