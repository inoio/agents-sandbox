package image

import (
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
)

// dockerOn and dockerOff are the docker settings most tests pass to the render
// entry points. dockerOn is explicit: a non-explicit setting is the resolved
// default (off), so only an explicit value enables docker without a content
// signal in the project Dockerfile.
var (
	dockerOn  = options.DockerSetting{Enabled: true, Explicit: true}
	dockerOff = options.DockerSetting{}
)

func agentOpencode(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// testAgentVersion is the agent version most render tests use; it matches the
// version most tests install via WithMockAgentVersion so identity assertions
// stay consistent with the rendered content.
const testAgentVersion = "1.2.3"

// renderBytes renders a runner Dockerfile for tests with the default test
// version, failing on a render error.
func renderBytes(t *testing.T, a agent.Agent, project []byte, docker options.DockerSetting) []byte {
	t.Helper()
	return renderBytesVersion(t, a, testAgentVersion, project, docker)
}

// renderBytesVersion renders a runner Dockerfile for a pinned agent version,
// failing on a render error.
func renderBytesVersion(
	t *testing.T,
	a agent.Agent,
	version string,
	project []byte,
	docker options.DockerSetting,
) []byte {
	t.Helper()
	out, err := RenderDockerfile(a, version, project, docker)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	return out
}

// TestReadProjectDockerfileBareWorktree pins the image path to the bare-root
// project config: readProjectDockerfile must read the bare root's Dockerfile and
// ignore the worktree copy, which the resolver reports as ignored.
func TestReadProjectDockerfileBareWorktree(t *testing.T) {
	worktree := synthesizeBareWorktree(t, false)
	root := filepath.Dir(worktree)
	writeFileAt(t, filepath.Join(root, ".agents-sandbox", "Dockerfile"), "FROM bare-root\n")
	writeFileAt(t, filepath.Join(worktree, ".agents-sandbox", "Dockerfile"), "FROM worktree\n")

	if got := string(readProjectDockerfile()); got != "FROM bare-root\n" {
		t.Errorf("readProjectDockerfile() = %q, want %q", got, "FROM bare-root\n")
	}
}
