package main

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/pruning"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// The runner image's identity: the build arg the renderer stamps into the
// agent-version label, the label itself, and the content ID the fake daemon
// reports for every build.
const (
	journeyAgentVersionArg   = "IDENTITY_AGENT_VERSION"
	journeyAgentVersionLabel = "org.agents-sandbox.identity.agent-version"
	journeyImageID           = "sha256:9f2c1a7b4d6e0f3199999999999999999999999999999999999999999999999999"
)

// journeyHost is a stateful fake of the host a sequence of CLI invocations runs
// against: the Docker daemon's runner image (with the agent version the last
// build baked into its label) and the microsandbox image cache (refs loaded,
// removed and listed). Each invocation sees what the previous one left behind,
// so a journey test asserts cross-stage effects (build → run) instead of one
// command in isolation. The fake image carries only the version label, so every
// invocation rebuilds it; each build keeps the same content ID, and the tests
// observe builds by the version they bake.
type journeyHost struct {
	mu            sync.Mutex
	runnerTag     string
	bakedVersion  string
	builtVersions []string
	msbImages     map[string]bool
}

func newJourneyHost() *journeyHost {
	return &journeyHost{msbImages: map[string]bool{}}
}

// dockerClient fakes the Docker daemon: any base image is present, an inspect
// of the runner tag reports the last build (or no image before the first), and
// a build records the version it baked.
func (host *journeyHost) dockerClient() *docker.MockDockerClient {
	return &docker.MockDockerClient{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			host.mu.Lock()
			defer host.mu.Unlock()
			if ref != host.runnerTag {
				return client.ImageInspectResult{ID: "sha256:base", Config: &dockerspec.DockerOCIImageConfig{}}, nil
			}
			if host.bakedVersion == "" {
				return client.ImageInspectResult{}, errors.New("no such image")
			}
			return client.ImageInspectResult{
				ID: journeyImageID,
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{journeyAgentVersionLabel: host.bakedVersion},
				},
			}, nil
		},
		ImageBuildFn: func(_ context.Context, _ io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
			host.mu.Lock()
			defer host.mu.Unlock()
			version := ""
			if arg := options.BuildArgs[journeyAgentVersionArg]; arg != nil {
				version = *arg
			}
			host.bakedVersion = version
			host.builtVersions = append(host.builtVersions, version)
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	}
}

// installMsbImageCache makes mock's image calls read and write the host's
// microsandbox image cache. Every cached image counts as recently used.
func (host *journeyHost) installMsbImageCache(mock *sandboxmsb.MockMsbClient) {
	mock.ImageGetFn = func(_ context.Context, ref string) error {
		host.mu.Lock()
		defer host.mu.Unlock()
		if !host.msbImages[ref] {
			return errors.New("image not cached")
		}
		return nil
	}
	mock.ImageInspectFn = func(context.Context, string) (*msbSdk.ImageConfig, error) {
		return &msbSdk.ImageConfig{Digest: journeyImageID}, nil
	}
	mock.ImageLoadFn = func(_ context.Context, ref string, _ io.Reader) error {
		host.mu.Lock()
		defer host.mu.Unlock()
		host.msbImages[ref] = true
		return nil
	}
	mock.ImageRemoveFn = func(_ context.Context, ref string, _ bool) error {
		host.mu.Lock()
		defer host.mu.Unlock()
		delete(host.msbImages, ref)
		return nil
	}
	mock.ImageListFn = func(context.Context) ([]sandboxmsb.ImageHandle, error) {
		host.mu.Lock()
		defer host.mu.Unlock()
		handles := make([]sandboxmsb.ImageHandle, 0, len(host.msbImages))
		for _, ref := range slices.Sorted(maps.Keys(host.msbImages)) {
			handles = append(handles, sandboxmsb.MockImageHandle{Reference_: ref, LastUsedAt_: time.Now()})
		}
		return handles, nil
	}
}

func (host *journeyHost) cachedImages() []string {
	host.mu.Lock()
	defer host.mu.Unlock()
	return slices.Sorted(maps.Keys(host.msbImages))
}

func (host *journeyHost) builds() []string {
	host.mu.Lock()
	defer host.mu.Unlock()
	return slices.Clone(host.builtVersions)
}

// startJourney installs the run fixtures for a journey in a fresh project
// directory, backed by host, with latestRelease as the agent's newest release.
// It returns the msb mock, the project's key for the default agent, and a
// function that executes one CLI invocation against the shared state, each with
// a fresh root command, the way separate processes would start.
func startJourney(
	t *testing.T,
	host *journeyHost,
	latestRelease string,
) (*sandboxmsb.MockMsbClient, state.Key, func(args ...string)) {
	t.Helper()
	initTestProjectDir(t)
	keys := currentRunImageKeys(defaultAgentName)
	if len(keys) == 0 {
		t.Fatal("the project directory resolves no identity")
	}
	host.runnerTag = naming.DockerImageRef(keys[0].Slug, keys[0].Agent)
	mock := &sandboxmsb.MockMsbClient{}
	host.installMsbImageCache(mock)
	_, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{})
	docker.WithDockerMock(t, host.dockerClient())
	withLatestAgentRelease(t, latestRelease)
	execute := func(args ...string) {
		t.Helper()
		root := buildRootCmd(ui)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	return mock, keys[0], execute
}

// runRealStartupPrune makes the startup auto-prune run the real prune for every
// invocation (AutoPrune itself runs once per process), with whatever retain
// keys the command passes.
func runRealStartupPrune(t *testing.T) {
	t.Helper()
	original := autoPrune
	autoPrune = func(ctx context.Context, age time.Duration, dryRun bool, ui termio.UI, retain ...state.Key) {
		if _, err := pruning.Prune(ctx, age, dryRun, ui, retain...); err != nil {
			t.Errorf("startup prune: %v", err)
		}
	}
	t.Cleanup(func() { autoPrune = original })
}

// staleProjectVM is the project's stopped VM, idle far beyond the prune age, so
// the startup prune retires its key in this pass.
func staleProjectVM(k state.Key) sandboxmsb.SandboxHandle {
	return &sandboxmsb.MockSandboxHandle{
		Name_:      "stale-" + k.Slug,
		Status_:    msbSdk.SandboxStatusStopped,
		UpdatedAt_: time.Now().Add(-365 * 24 * time.Hour),
		Cfg: &msbSdk.SandboxConfig{Labels: map[string]string{
			naming.LabelSlug:  k.Slug,
			naming.LabelAgent: k.Agent,
		}},
	}
}

// TestBuildThenRunKeepsTheBuiltImage pins the image-prune regression end to
// end (#451, #452): a project whose `build` loaded its runner image while its
// only VM is stopped and stale has that VM's key retired by the next `run`'s
// startup prune, yet the run passes its own keys, so the prune keeps the image
// and the run reuses it instead of reimporting it.
func TestBuildThenRunKeepsTheBuiltImage(t *testing.T) {
	host := newJourneyHost()
	runRealStartupPrune(t)
	mock, k, execute := startJourney(t, host, "1.2.0")

	execute(cmdBuild)
	built := host.cachedImages()
	if len(built) != 1 || len(mock.LoadedImages) != 1 {
		t.Fatalf("build cached %v after loads %v, want one loaded runner image", built, mock.LoadedImages)
	}

	mock.Sandboxes = []sandboxmsb.SandboxHandle{staleProjectVM(k)}
	trace := captureLifecycleTrace(t)
	execute(cmdRun)

	if !slices.Contains(trace.Stages(), lifecycle.Prune) {
		t.Fatalf("trace = %v, want the run to reach prune", trace.Stages())
	}
	if got := host.cachedImages(); !slices.Equal(got, built) {
		t.Errorf("msb images after run = %v, want the built image %v kept", got, built)
	}
	if len(mock.LoadedImages) != 1 {
		t.Errorf("image loads = %v, want only the build's: the run must reuse the image", mock.LoadedImages)
	}
}

// TestAgentVersionFollowsTheImageLabel pins the agent-version regression end
// to end for the default label-driven profile (opencode2; #452, #453): after a
// `build` and a `run`, HomeState.agent_version equals the image's label, and a
// later unpinned run whose cache went stale (an older release recorded) neither
// rebuilds toward that older release nor keeps it: the label wins and the
// cache is rewritten. That run is offline (the latest-release lookup fails), so
// no upgrade offer can mask a stale baseline.
func TestAgentVersionFollowsTheImageLabel(t *testing.T) {
	const released, stale = "1.2.0", "1.1.0"
	host := newJourneyHost()
	_, k, execute := startJourney(t, host, released)

	execute(cmdBuild)
	execute(cmdRun)
	assertRecordedAgentVersion(t, k, released)

	if err := state.WithHomeState(k, func(hs *state.HomeState, exists bool) (bool, error) {
		hs.AgentVersion = stale
		return exists, nil
	}); err != nil {
		t.Fatalf("make the agent_version cache stale: %v", err)
	}
	// Expire the machine-global upgrade check so the final run consults the
	// network instead of reusing the earlier run's cached successful check.
	expireAgentUpgradeCheck(t)
	var offlineConsulted bool
	withOfflineUpgradeCheck(t, func() { offlineConsulted = true })
	execute(cmdRun)
	if !offlineConsulted {
		t.Error("the final run never consulted the offline upgrade check: it reused the earlier run's cached check")
	}

	if slices.Contains(host.builds(), stale) {
		t.Errorf("builds = %v, want no rebuild toward the stale cached %s", host.builds(), stale)
	}
	assertRecordedAgentVersion(t, k, released)
}

// withLatestAgentRelease makes both latest-release lookups (the image build's
// and the run's upgrade check) report version, so no upgrade is offered. A build
// that requests a specific release gets that release.
func withLatestAgentRelease(t *testing.T, version string) {
	t.Helper()
	image.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, requested string) (string, error) {
		if requested != "" {
			return requested, nil
		}
		return version, nil
	})
	original := sandbox.SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
		return version, nil
	})
	t.Cleanup(func() { sandbox.SetAgentLatestVersion(original) })
}

// expireAgentUpgradeCheck clears the machine-global agent upgrade check so the
// next run treats the network check as due (a zero LastChecked is always due).
func expireAgentUpgradeCheck(t *testing.T) {
	t.Helper()
	ok, err := state.WithRootState(false, func(txn *state.RootTxn) error {
		txn.State().SetAgentCheck(defaultAgentName, state.AgentCheckState{})
		return txn.Commit()
	})
	if err != nil || !ok {
		t.Fatalf("expire the agent upgrade check: ok=%v err=%v", ok, err)
	}
}

// withOfflineUpgradeCheck makes the run's latest-release lookup fail, as it
// does without network access, and calls consulted on each lookup so a test can
// assert the run actually reached the network.
func withOfflineUpgradeCheck(t *testing.T, consulted func()) {
	t.Helper()
	original := sandbox.SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
		consulted()
		return "", errors.New("offline")
	})
	t.Cleanup(func() { sandbox.SetAgentLatestVersion(original) })
}

func assertRecordedAgentVersion(t *testing.T, k state.Key, want string) {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("read the project state: %v", err)
	}
	if hs == nil || hs.AgentVersion != want {
		t.Errorf("HomeState.agent_version = %+v, want %q (the image label)", hs, want)
	}
}
