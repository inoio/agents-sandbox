package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// seedProjectDirs creates every project dir a smoke run builds an image in, so
// harnessRunnerImageTags can resolve all of them.
func seedProjectDirs(t *testing.T, root string) {
	t.Helper()
	for _, pair := range suiteProjectAgents() {
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

// TestSuiteProjectAgentsCoverEveryBuiltImage pins the one list the prebuild and
// cleanup share: every harness in its own project dir, plus the docker-enabled
// docker-storage pair.
func TestSuiteProjectAgentsCoverEveryBuiltImage(t *testing.T) {
	pairs := suiteProjectAgents()
	if len(pairs) != len(agent.Names())+1 {
		t.Fatalf("pairs = %d, want one per harness plus docker-storage (%d)", len(pairs), len(agent.Names())+1)
	}
	for _, name := range agent.Names() {
		if !slices.Contains(pairs, projectAgent{Project: name, Agent: name}) {
			t.Errorf("missing pair for harness %s; got %v", name, pairs)
		}
	}
	if !slices.Contains(pairs, projectAgent{Project: dockerStorageProject, Agent: "opencode", Docker: true}) {
		t.Errorf("missing the docker-enabled docker-storage pair; got %v", pairs)
	}
	docker := 0
	for _, pair := range pairs {
		if pair.Docker {
			docker++
		}
	}
	if docker != 1 {
		t.Errorf("docker-enabled pairs = %d, want exactly 1", docker)
	}
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

// TestHarnessRunnerImageTagsSkipsUnresolvableProject pins the spec's failure
// mode: a project dir missing (a prebuild that failed before creating it) skips
// only that tag, so the tags that were built are still returned for cleanup.
// The missing dir is the first project, so a walk that stops on the skip is
// observable as the later tags going missing too.
func TestHarnessRunnerImageTagsSkipsUnresolvableProject(t *testing.T) {
	root := t.TempDir()
	seedProjectDirs(t, root)
	missing := agent.Names()[0]
	if err := os.RemoveAll(projectDir(root, missing)); err != nil {
		t.Fatal(err)
	}
	got, err := harnessRunnerImageTags(root)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want it wrapping fs.ErrNotExist for the missing project dir", err)
	}
	if len(got) != len(agent.Names()) {
		t.Fatalf("tags = %d, want the resolvable projects only (%d)", len(got), len(agent.Names()))
	}
	for _, name := range agent.Names() {
		if name == missing {
			continue
		}
		if !slices.Contains(got, tagFor(t, projectDir(root, name), name)) {
			t.Errorf("missing tag for the resolvable project %s", name)
		}
	}
	dockerTag := tagFor(t, projectDir(root, dockerStorageProject), "opencode")
	if !slices.Contains(got, dockerTag) {
		t.Errorf("missing the docker-storage tag %q after a skip; got %v", dockerTag, got)
	}
}

func TestHarnessRunnerImageTagsAllMissing(t *testing.T) {
	tags, err := harnessRunnerImageTags(t.TempDir())
	if err == nil {
		t.Error("no project dirs = nil error, want every skipped dir reported")
	}
	if len(tags) != 0 {
		t.Errorf("tags = %v, want none when no project dir resolves", tags)
	}
}

// recordingTB captures the diagnostics cleanupRunnerImages emits. Embedding
// testing.TB satisfies its private method; Logf is the recorded call.
type recordingTB struct {
	testing.TB

	logs []string
}

func (r *recordingTB) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

// TestRunnerImageRemoveTimeoutIsThirtySeconds pins the literal bound: a
// one-second change is not observable through a docker stub that answers at
// once, so assert the value itself.
func TestRunnerImageRemoveTimeoutIsThirtySeconds(t *testing.T) {
	if runnerImageRemoveTimeout != 30*time.Second {
		t.Errorf("runnerImageRemoveTimeout = %v, want 30s", runnerImageRemoveTimeout)
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
	recorder := &recordingTB{TB: t}
	cleanupRunnerImages(recorder, root) // must not fail the test or stop after the first error
	if len(removed) != len(agent.Names())+1 {
		t.Errorf("attempted %d removals, want one per project (%d)", len(removed), len(agent.Names())+1)
	}
	if len(recorder.logs) != 1 {
		t.Fatalf("logs = %q, want exactly the failed removal", recorder.logs)
	}
	if log := recorder.logs[0]; !strings.Contains(log, removed[0]) || !strings.Contains(log, "boom") {
		t.Errorf("log = %q, want the failed tag %q and docker's output", log, removed[0])
	}
}

// TestCleanupRunnerImagesRemovesNothingWithoutProjectDirs pins the derive-failure
// path: when no tag can be derived, cleanup logs and removes nothing.
func TestCleanupRunnerImagesRemovesNothingWithoutProjectDirs(t *testing.T) {
	calls := 0
	restore := removeDockerImage
	t.Cleanup(func() { removeDockerImage = restore })
	removeDockerImage = func(_ []string, _ string) ([]byte, error) {
		calls++
		return nil, nil
	}
	recorder := &recordingTB{TB: t}
	cleanupRunnerImages(recorder, t.TempDir())
	if calls != 0 {
		t.Errorf("removals attempted = %d, want 0 when no tag can be derived", calls)
	}
	if len(recorder.logs) != 1 || !strings.Contains(recorder.logs[0], "could not be derived") {
		t.Errorf("logs = %q, want one report of the underived tags", recorder.logs)
	}
}

// TestRemoveDockerImagePreservesParentLayers pins the concrete docker command:
// removing a runner tag must not delete the classic builder's untagged parent
// images, which are its layer cache (the repo's removeTestImage rule).
func TestRemoveDockerImagePreservesParentLayers(t *testing.T) {
	stubDir := t.TempDir()
	logPath := filepath.Join(stubDir, "docker.log")
	stub := filepath.Join(stubDir, "docker")
	// The absolute shebang needs no PATH entry; exec.Command resolves `docker`
	// through the process PATH, so the stub dir must be the PATH.
	if err := os.WriteFile(
		stub,
		[]byte("#!/bin/bash\nprintf '%s\\n' \"$*\" >\"$DOCKER_STUB_LOG\"\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir)
	env := []string{"DOCKER_STUB_LOG=" + logPath}
	tag := "agents-sandbox/runner-stub-aaaaaaaaaaaaaa:opencode-latest"
	if _, err := removeDockerImage(env, tag); err != nil {
		t.Fatalf("removeDockerImage: %v", err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "rmi --no-prune " + tag + "\n"; string(got) != want {
		t.Errorf("docker argv = %q, want %q", string(got), want)
	}
}
