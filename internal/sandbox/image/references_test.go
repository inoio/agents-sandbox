package image

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func TestInUseImageReferencesUnionsProjectStatesAndKeptSandboxes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	projectKey := state.Key{Slug: "proj-aBc1234D", Agent: "opencode"}
	if err := seedHomeState(projectKey, state.HomeState{ImageDigest: "sha256:abc"}); err != nil {
		t.Fatalf("write project state: %v", err)
	}
	pinnedKey := state.Key{Slug: "live-1mjusbm3wikhb0", Agent: "opencode"}
	pinned := &msbtest.MockSandboxHandle{
		Name_: "agents-sandbox-vm-live-1mjusbm3wikhb0-opencode",
		Cfg:   &msb.SandboxConfig{Image: "agents-sandbox/runner-live-1mjusbm3wikhb0:digestOld"},
	}

	references, err := InUseImageReferences([]msb.SandboxHandle{pinned})
	if err != nil {
		t.Fatalf("InUseImageReferences: %v", err)
	}
	ownedRef := naming.RunnerImageRef(projectKey.Slug, projectKey.Agent)
	if keys := references[ownedRef]; len(keys) != 1 || keys[0] != projectKey {
		t.Fatalf("references[%q] = %v, want [%v] from the project state", ownedRef, keys, projectKey)
	}
	if keys := references["agents-sandbox/runner-live-1mjusbm3wikhb0:digestOld"]; len(keys) != 1 ||
		keys[0] != pinnedKey {
		t.Fatalf("digest-pinned ref keys = %v, want [%v] from the kept sandbox", keys, pinnedKey)
	}
}

func TestInUseImageReferencesDeduplicatesProjectAndSandbox(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{}); err != nil {
		t.Fatalf("write project state: %v", err)
	}
	same := &msbtest.MockSandboxHandle{
		Name_: "agents-sandbox-vm-proj-1mjusbm3wikhb0-opencode",
		Cfg:   &msb.SandboxConfig{Image: naming.RunnerImageRef(key.Slug, key.Agent)},
	}

	references, err := InUseImageReferences([]msb.SandboxHandle{same})
	if err != nil {
		t.Fatalf("InUseImageReferences: %v", err)
	}
	if keys := references[naming.RunnerImageRef(key.Slug, key.Agent)]; len(keys) != 1 {
		t.Fatalf("references = %v, want the key once (no duplicate from the sandbox)", keys)
	}
}

// TestInUseImageReferencesProtectsContentRefHeldByKeptSandbox pins that the
// in-use guard protects a content ref a kept sandbox holds: state.ImageReferences
// does not enumerate content refs, so the live-sandbox scan must catch it.
func TestInUseImageReferencesProtectsContentRefHeldByKeptSandbox(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	key := state.Key{Slug: "proj-1mjusbm3wikhb0", Agent: "opencode"}
	contentRef := naming.ContentImageRef(key.Slug, key.Agent, "9f2c1a7b4d6e0f31")
	handle := &msbtest.MockSandboxHandle{
		Name_: "agents-sandbox-vm-proj-1mjusbm3wikhb0-opencode",
		Cfg:   &msb.SandboxConfig{Image: contentRef},
	}

	references, err := InUseImageReferences([]msb.SandboxHandle{handle})
	if err != nil {
		t.Fatalf("InUseImageReferences: %v", err)
	}
	if keys := references[contentRef]; len(keys) != 1 || keys[0] != key {
		t.Fatalf("references[%q] = %v, want [%v] from the kept sandbox", contentRef, keys, key)
	}
}

func TestInUseImageReferencesSkipsSandboxesWithoutImage(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	empty := &msbtest.MockSandboxHandle{
		Name_: "agents-sandbox-vm-proj-aBc1234D-opencode",
		Cfg:   &msb.SandboxConfig{},
	}

	references, err := InUseImageReferences([]msb.SandboxHandle{empty})
	if err != nil {
		t.Fatalf("InUseImageReferences: %v", err)
	}
	if len(references) != 0 {
		t.Fatalf("references = %v, want empty (sandbox has no image ref)", references)
	}
}

func TestInUseImageReferencesFailsClosedOnUnreadableSandboxConfig(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	broken := &msbtest.MockSandboxHandle{
		Name_:     "agents-sandbox-vm-proj-aBc1234D-opencode",
		ConfigErr: errors.New("sandbox gone"),
	}

	if _, err := InUseImageReferences([]msb.SandboxHandle{broken}); !errors.Is(err, broken.ConfigErr) {
		t.Fatalf("InUseImageReferences() error = %v, want the sandbox config error (fail closed)", err)
	}
}

func TestInUseImageReferencesFailsClosedOnCorruptState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	dir := state.KeyDir(state.Key{Slug: "proj-aBc1234D", Agent: "opencode"})
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.yaml"), []byte("image_digest: ["), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, err := InUseImageReferences(nil); err == nil {
		t.Fatal("InUseImageReferences() error = nil, want a corrupt state to fail closed")
	}
}

func TestInUseImageReferencesKeepsPositionalSandbox(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ref := naming.RunnerImageRef("proj-1mjusbm3wikhb0", "opencode")
	handle := &msbtest.MockSandboxHandle{Name_: "proj-1mjusbm3wikhb0-opencode", Cfg: &msb.SandboxConfig{Image: ref}}
	client := &msbtest.MockMsbClient{Sandboxes: []msb.SandboxHandle{handle}}
	msbtest.WithMsbMock(t, client)

	references, err := inUseImageReferences(context.Background(), client)
	if err != nil {
		t.Fatalf("inUseImageReferences() = %v", err)
	}
	if len(references[ref]) != 1 {
		t.Fatalf(
			"references[%q] = %v, want one (every sandbox in the private runtime home is ours)",
			ref,
			references[ref],
		)
	}
}
