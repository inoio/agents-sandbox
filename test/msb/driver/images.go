package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

// projectsDirName is the suite root's subdirectory holding one throwaway project
// dir per harness.
const projectsDirName = "projects"

// DockerStorageProject is the throwaway project directory the docker-storage
// subtest runs from; it builds the opencode agent.
const DockerStorageProject = "docker-storage"

// ProjectDir is the throwaway project directory a harness runs in. It fixes the
// project slug the runner image is keyed by, so the prebuild's image matches the
// one the subtest's `run` loads.
func ProjectDir(root, name string) string {
	return filepath.Join(root, projectsDirName, name)
}

// ProjectAgent is one project dir and the agent its runner image is built for.
// Docker marks the image built with `--docker` (a docker-enabled VM).
type ProjectAgent struct {
	Project string
	Agent   string
	Docker  bool
}

// SuiteProjectAgents lists the runner images the suite builds: every registered
// harness in its own project dir, plus the docker-storage project's
// docker-enabled opencode image. It is the single source the prebuild and the
// cleanup both read, so they cannot drift.
func SuiteProjectAgents() []ProjectAgent {
	names := agent.Names()
	var pairs []ProjectAgent
	for _, name := range names {
		pairs = append(pairs, ProjectAgent{Project: name, Agent: name, Docker: false})
	}
	return append(pairs, ProjectAgent{Project: DockerStorageProject, Agent: opencodeAgentName, Docker: true})
}

// HarnessRunnerImageTags returns the Docker runner-image tags the suite built,
// deriving each project's slug the way the launcher does so no tag is guessed.
// A project dir that does not resolve (a prebuild that failed before creating
// it) skips only that tag: the resolvable tags come back with the skips joined
// into the error, so a partway-failed run still cleans the images it built.
func HarnessRunnerImageTags(root string) ([]string, error) {
	pairs := SuiteProjectAgents()
	tags := make([]string, 0, len(pairs))
	var failures []error
	for _, pair := range pairs {
		dir := ProjectDir(root, pair.Project)
		identity, err := git.ResolveIdentity(dir)
		if err != nil {
			failures = append(failures, fmt.Errorf("resolve identity for %s: %w", dir, err))
			continue
		}
		tags = append(tags, naming.DockerImageRef(identity.CheckoutSlug, pair.Agent))
	}
	return tags, errors.Join(failures...)
}

// runnerImageRemoveTimeout bounds one docker rmi so a wedged daemon cannot hang
// cleanup.
const runnerImageRemoveTimeout = 30 * time.Second

// removeDockerImage is the seam CleanupRunnerImages removes a tag through; a
// test replaces it to assert the attempt set without a docker daemon.
var removeDockerImage = func(env []string, tag string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runnerImageRemoveTimeout)
	defer cancel()
	//testenv:allow the smoke suite removes real runner images
	cmd := exec.CommandContext(ctx, "docker", "rmi", "--no-prune", tag)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// CleanupRunnerImages best-effort removes the runner-image tags a fresh smoke
// run built, so a cold run leaves no docker residue. A tag that cannot be
// removed (absent, or a docker failure) is logged, never fatal: cleanup must not
// mask a test result.
func CleanupRunnerImages(tb TB, root string) {
	// gomutants:disable-next-line STATEMENT_REMOVE reason="Helper only shifts the location a cleanup log reports; no behaviour depends on it"
	tb.Helper()

	tags, err := HarnessRunnerImageTags(root)
	if err != nil {
		tb.Logf("cleanup: some runner image tags could not be derived: %v", err)
	}
	//testenv:allow cleanup builds the isolated env from the suite's own
	env := IsolatedEnv(root, root, os.Environ())
	for _, tag := range tags {
		if out, err := removeDockerImage(env, tag); err != nil {
			tb.Logf("cleanup: remove runner image %s: %v\n%s", tag, err, out)
		}
	}
}
