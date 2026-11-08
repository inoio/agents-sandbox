package image

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func verboseContains(ui *termio.Mock, substr string) bool {
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, substr) {
			return true
		}
	}
	return false
}

// dockerConfigWith builds a Docker image config carrying the given agent label
// and environment for mocked ImageInspect results.
func dockerConfigWith(version string, env []string) *dockerspec.DockerOCIImageConfig {
	labels := map[string]string{}
	if version != "" {
		labels[agentLabelKey] = version
	}
	return &dockerspec.DockerOCIImageConfig{
		Env: env, Labels: labels,
	}
}

// runnerAndBaseMock serves the runner image under its tag with runnerLabels and
// the distro base under its ref with baseID (an empty baseID means the base is
// absent). Any pull fails the test: the build decision must stay local.
func runnerAndBaseMock(t *testing.T, runnerLabels map[string]string, baseID string) {
	t.Helper()
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == distroBaseRef {
				if baseID == "" {
					return client.ImageInspectResult{}, errors.New("No such image")
				}
				return client.ImageInspectResult{ID: baseID}, nil
			}
			return client.ImageInspectResult{Config: &dockerspec.DockerOCIImageConfig{Labels: runnerLabels}}, nil
		},
		ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			t.Errorf("unexpected pull of %s during the build decision", ref)
			return nil, errors.New("unexpected pull")
		},
	})
}

// seededRunnerLabels returns the labels of a runner image built from the
// current plan with the given base provenance label.
func seededRunnerLabels(t *testing.T, baseLabel string) map[string]string {
	t.Helper()
	plan, err := renderBuildPlan(agentOpencode(t), parseProjectDockerfile(nil), dockerOff, "1.2.3", "1.2.3")
	if err != nil {
		t.Fatalf("renderBuildPlan: %v", err)
	}
	identity := plan.identity
	identity.BaseImage = baseLabel
	labels := identityLabels(identity)
	labels[dockerfileIDLabelKey] = plan.dockerfileID
	return labels
}

// testBuildRequest assembles a docker-off buildRequest for the build-level tests
// that call buildImage/buildDockerImage directly, mapping the loose values those
// tests vary onto the request fields. Tests exercising docker enablement go
// through EnsureImageWithClient or set the request's docker field directly.
func testBuildRequest(
	rendered []byte,
	agentVersion, baseDigest, dockerfileID string,
	identity imageIdentity,
) buildRequest {
	return buildRequest{
		plan: buildPlan{
			rendered:     rendered,
			agentVersion: agentVersion,
			dockerfileID: dockerfileID,
			identity:     identity,
		},
		baseDigest: baseDigest,
	}
}

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

const hostOpenerShimPath = "data/xdg-open.sh"

// identityLabels is the label map an image built from id carries. Production
// writes those labels through the identity build args and the Dockerfile's LABEL
// lines, so this is a test-side oracle only.
func identityLabels(id imageIdentity) map[string]string {
	return map[string]string{
		projectDockerfileHashLabelKey: id.ProjectDockerfileHash,
		agentVersionLabelKey:          id.AgentVersion,
		dockerLabelKey:                id.Docker,
		userLabelKey:                  id.User,
		baseLabelKey:                  id.BaseImage,
	}
}

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}

// vendoredTrustHelperPath is the on-disk source of the trust helper asset. The
// test reads it directly so the assertion fails when the build context ships
// any other bytes, rather than comparing the asset against itself.
const vendoredTrustHelperPath = "data/msb-trust.sh"

// vendoredTrustHelperLicensePath records the provenance of the vendored helper,
// including the hash the tests hold it to.
const vendoredTrustHelperLicensePath = "data/msb-trust.LICENSE"

// withMockAgentVersion pins the package's default agent-version resolver to v
// for the duration of the test, restoring it when the test ends. It is the
// in-package replacement for the retired exported WithMockAgentVersion hook:
// production callers inject a resolver through BuildOptions.ResolveVersion, and
// image's own tests override the package default.
func withMockAgentVersion(t *testing.T, v string) {
	t.Helper()
	withMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return v, nil
	})
}

// withMockAgentVersionResolver installs a custom default resolver for the test.
func withMockAgentVersionResolver(t *testing.T, resolve AgentVersionResolver) {
	t.Helper()
	orig := defaultAgentVersionResolver
	defaultAgentVersionResolver = resolve
	t.Cleanup(func() { defaultAgentVersionResolver = orig })
}

// synthesizeBareWorktree builds a bare checkout whose single worktree lives
// inside the bare root, by hand: <root>/.git is a bare git dir (core.bare) and
// <root>/main/.git is a link file pointing into it. It pins the host home and
// XDG directories, installs the real path resolver, and chdirs into the
// worktree. The worktree-level .agents-sandbox is created only when
// withWorktreeConfig is set.
func synthesizeBareWorktree(t *testing.T, withWorktreeConfig bool) (worktree string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	configpaths.WithBareCheckoutLocator(t, git.BareCheckoutRoots)

	root := t.TempDir()
	worktree = filepath.Join(root, "main")
	writeFileAt(t, filepath.Join(root, ".git", "config"), "[core]\n\tbare = true\n")
	// A real linked worktree's registration carries a commondir marker; the
	// identity resolver requires it to tell a worktree from a --separate-git-dir
	// checkout, so the synthetic layout must include it.
	writeFileAt(t, filepath.Join(root, ".git", "worktrees", "main", "commondir"), "../..\n")
	writeFileAt(t, filepath.Join(worktree, ".git"), "gitdir: ../.git/worktrees/main\n")
	if withWorktreeConfig {
		if err := os.MkdirAll(filepath.Join(worktree, ".agents-sandbox"), 0o750); err != nil {
			t.Fatalf("MkdirAll(worktree config): %v", err)
		}
	}
	t.Chdir(worktree)
	return worktree
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// assertWarnsIgnoredWorktreeConfig fails unless ui recorded the bare-checkout
// warning, pinning that an exported entry point still runs the warning after the
// path resolver moved from a package global onto an injected argument.
func assertWarnsIgnoredWorktreeConfig(t *testing.T, ui *termio.Mock) {
	t.Helper()
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "read from the bare root, not the worktree") {
		t.Errorf("WarnCalls = %v, want the ignored-worktree-config warning", ui.WarnCalls)
	}
}
