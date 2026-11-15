package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// projectsDirName is the suite root's subdirectory holding one throwaway project
// dir per harness.
const projectsDirName = "projects"

// dockerStorageProject is the throwaway project directory the docker-storage
// subtest runs from; it builds the opencode agent.
const dockerStorageProject = "docker-storage"

// projectDir is the throwaway project directory a harness runs in. It fixes the
// project slug the runner image is keyed by, so the prebuild's image matches the
// one the subtest's `run` loads.
func projectDir(root, name string) string {
	return filepath.Join(root, projectsDirName, name)
}

// projectAgent is one project dir and the agent its runner image is built for.
type projectAgent struct {
	Project string
	Agent   string
}

// harnessProjectAgents lists the project/agent pairs the suite builds images
// for: every registered harness in its own project dir, plus the docker-storage
// project's docker-enabled opencode image.
func harnessProjectAgents() []projectAgent {
	names := agent.Names()
	pairs := make([]projectAgent, 0, len(names)+1)
	for _, name := range names {
		pairs = append(pairs, projectAgent{Project: name, Agent: name})
	}
	return append(pairs, projectAgent{Project: dockerStorageProject, Agent: "opencode"})
}

// harnessRunnerImageTags returns the Docker runner-image tags the suite built,
// deriving each project's slug the way the launcher does so no tag is guessed.
func harnessRunnerImageTags(root string) ([]string, error) {
	pairs := harnessProjectAgents()
	tags := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		dir := projectDir(root, pair.Project)
		identity, err := git.ResolveIdentity(dir)
		if err != nil {
			return nil, fmt.Errorf("resolve identity for %s: %w", dir, err)
		}
		tags = append(tags, naming.DockerImageRef(identity.CheckoutSlug, pair.Agent))
	}
	return tags, nil
}

// runnerImageRemoveTimeout bounds one docker rmi so a wedged daemon cannot hang
// cleanup.
const runnerImageRemoveTimeout = 30 * time.Second

// removeDockerImage is the seam cleanupRunnerImages removes a tag through; a
// test replaces it to assert the attempt set without a docker daemon.
var removeDockerImage = func(env []string, tag string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runnerImageRemoveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "rmi", tag)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// cleanupRunnerImages best-effort removes the runner-image tags a fresh smoke
// run built, so a cold run leaves no docker residue. A tag that cannot be
// removed (absent, or a docker failure) is logged, never fatal: cleanup must not
// mask a test result.
func cleanupRunnerImages(t *testing.T, root string) {
	t.Helper()

	tags, err := harnessRunnerImageTags(root)
	if err != nil {
		t.Logf("cleanup: derive runner image tags: %v", err)
		return
	}
	env := isolatedEnv(root, root, os.Environ())
	for _, tag := range tags {
		if out, err := removeDockerImage(env, tag); err != nil {
			t.Logf("cleanup: remove runner image %s: %v\n%s", tag, err, out)
		}
	}
}
