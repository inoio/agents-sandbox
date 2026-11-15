package harness

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// seedProjectDirs creates every project dir a smoke run builds an image in, so
// harnessRunnerImageTags can resolve all of them.
func seedProjectDirs(t *testing.T, root string) {
	t.Helper()
	for _, pair := range harnessProjectAgents() {
		if err := os.MkdirAll(projectDir(root, pair.Project), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// expectedTags lists the tags a smoke run builds, keyed by agent: each harness's
// project dir builds its own agent, and the docker-storage project builds
// opencode.
func expectedTags(t *testing.T, root string) map[string]string {
	t.Helper()
	want := map[string]string{}
	for _, name := range agent.Names() {
		want[name] = tagFor(t, projectDir(root, name), name)
	}
	want[dockerStorageProject] = tagFor(t, projectDir(root, dockerStorageProject), "opencode")
	return want
}

func tagFor(t *testing.T, dir, agentName string) string {
	t.Helper()
	identity, err := git.ResolveIdentity(dir)
	if err != nil {
		t.Fatalf("ResolveIdentity(%q): %v", dir, err)
	}
	return naming.DockerImageRef(identity.CheckoutSlug, agentName)
}

func TestHarnessRunnerImageTagsCoverEveryProject(t *testing.T) {
	root := t.TempDir()
	seedProjectDirs(t, root)
	got, err := harnessRunnerImageTags(root)
	if err != nil {
		t.Fatalf("harnessRunnerImageTags: %v", err)
	}
	want := expectedTags(t, root)
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %d (one per project)", got, len(want))
	}
	seen := map[string]bool{}
	for _, tag := range got {
		seen[tag] = true
	}
	for _, tag := range want {
		if !seen[tag] {
			t.Errorf("missing tag %q; got %v", tag, got)
		}
	}
}

func TestHarnessRunnerImageTagsArePathScoped(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	seedProjectDirs(t, first)
	seedProjectDirs(t, second)
	firstTags, err := harnessRunnerImageTags(first)
	if err != nil {
		t.Fatal(err)
	}
	secondTags, err := harnessRunnerImageTags(second)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range firstTags {
		if slices.Contains(secondTags, tag) {
			t.Errorf("two roots share the tag %q; a cold run would untag a cached one", tag)
		}
	}
}

func TestHarnessRunnerImageTagsMissingProjectDir(t *testing.T) {
	if _, err := harnessRunnerImageTags(t.TempDir()); err == nil {
		t.Error("harnessRunnerImageTags with no project dirs = nil error, want a resolve failure")
	}
}

func TestCleanupRunnerImagesIsBestEffort(t *testing.T) {
	root := t.TempDir()
	seedProjectDirs(t, root)
	var removed []string
	restore := removeDockerImage
	t.Cleanup(func() { removeDockerImage = restore })
	removeDockerImage = func(_ []string, tag string) ([]byte, error) {
		removed = append(removed, tag)
		if len(removed) == 1 {
			return []byte("boom"), fmt.Errorf("docker rmi failed")
		}
		return nil, nil
	}
	cleanupRunnerImages(t, root) // must not fail the test or stop after the first error
	if len(removed) != len(agent.Names())+1 {
		t.Errorf("attempted %d removals, want one per project (%d)", len(removed), len(agent.Names())+1)
	}
}
