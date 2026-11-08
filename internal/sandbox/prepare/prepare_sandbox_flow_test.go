package prepare

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/lifecycle"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtimemigration"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestPrepareSandboxReusesStoredOpenCodeVersion verifies that a normal run
// falls back to the opencode version recorded in updater.yaml instead of
// re-resolving "latest" from the network. Passing the stored version keeps the
// image identity (and thus the microsandbox load decision) stable across runs.
func TestPrepareSandboxReusesStoredOpenCodeVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// The resolver records the requested version and returns it, so the test
	// observes exactly what PrepareSandbox passed instead of a network lookup.
	var requested string
	withMockBuildVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
		requested = req
		return req, nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	if requested != "1.5.0" {
		t.Errorf("resolver requested = %q, want stored version %q", requested, "1.5.0")
	}
}

// TestPrepareSandboxUpgradeRebuildsImage pins that an accepted agent upgrade
// force-rebuilds the image and that the run reports the newer version being
// baked in.
func TestPrepareSandboxUpgradeRebuildsImage(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	// The upgrade prompt accepts the rebuild, so shallUpgrade is true.
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.0.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	if !contains(joinStrings(ui.VerboseCalls), "runner image rebuilt with a newer opencode version") {
		t.Errorf("expected a 'rebuilt with newer opencode' verbose, got %v", ui.VerboseCalls)
	}
}

// TestPrepareSandboxRecordsTheOfferedUpgradeOnAFreshProject pins that an
// upgrade offered before the project has any state is remembered once home
// resolution creates the state, so the next run does not offer the same
// release again.
func TestPrepareSandboxRecordsTheOfferedUpgradeOnAFreshProject(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(newOriginClone(t))
	installPrepareSandboxDockerMock(t)
	installPrepareSandboxMSBMock(t)
	withExistingAgentVersionValue(t, "1.0.0")
	withMockAgentLatestVersion(t, "2.0.0")
	k := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	ui := termio.NewTestMock(t)

	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Errorf("offered versions = %v, want 2.0.0 recorded", hs.OfferedVersions)
	}
}

// TestPrepareSandboxLoadsHomeYamlOnce verifies that a full startup loads the
// home manifests exactly once: a missing host source is warned about a
// single time. This guards against the regression where config files were
// loaded twice (once in resolveReconfigDecision and once in vm.SetUpSandbox), which made
// the warning (and the underlying BuildHomeFilesWithModes computation) run twice.
func TestPrepareSandboxLoadsHomeYamlOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home config whose host source does not exist triggers the
	// "does not exist on the host; skipping" warning. The source is a relative
	// path, resolved against the (empty) project config dir, so it cannot exist.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "home:\n  missing-file.txt: missing-file.txt\n")

	// Pin the resolved opencode version so image resolution needs no network.
	withMockAgentLatestVersion(t, "1.0.0")

	// The image is considered up to date: the same version is "latest", so
	// resolveBuildVersion does not offer a rebuild.
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	// Docker build + inspect are mocked so EnsureImage can "build" the runner
	// image and produce a deterministic digest.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()

	// The msb image cache already holds the digest image (ImageGet succeeds),
	// so EnsureImage short-circuits before exporting/loading anything. The
	// image carries the pinned opencode version label.
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	// Seed home state so ResolveHomeVolume reuses the existing volume and
	// digest matches (no image-change home-volume prompt).
	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.0.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	// The opencode daemon is healthy inside the VM, so ensureDaemon returns
	// immediately instead of starting and polling.
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	missingWarnings := 0
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "does not exist on the host; skipping") {
			missingWarnings++
		}
	}
	if missingWarnings != 1 {
		t.Errorf("expected exactly 1 'missing home source' warning, got %d (warnings: %v)",
			missingWarnings, ui.WarnCalls)
	}
}

func TestPrepareSandboxLoadsEnvSecretsOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project secret spec whose value lacks the "value@host" format makes the
	// loader warn. A run used to load the env and secret files three times (the
	// fingerprint seed, the reconfig decision, and VM creation), repeating that
	// warning; the loaded value is threaded instead, so it warns once (#342).
	testutil.WriteFile(t, cp.ProjectConfigDir(), configpaths.EnvSecretFileName, "TOKEN=missing-host\n")

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		FSValue_: vmFS,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	// No sandbox handle is registered, so GetSandbox reports "not found" and
	// PrepareSandbox takes the create path, which is what used to read the env
	// and secret files a third time.
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	if len(mock.CreatedSandboxes) != 1 {
		t.Fatalf("expected the project VM to be created, got %d creations", len(mock.CreatedSandboxes))
	}

	malformedWarnings := 0
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "not defined in format 'value@host'") {
			malformedWarnings++
		}
	}
	if malformedWarnings != 1 {
		t.Errorf("expected exactly 1 malformed secret warning, got %d (warnings: %v)",
			malformedWarnings, ui.WarnCalls)
	}
}

// TestPrepareSandboxBakesAndFingerprintsTheLoadedEnv pins the one-load
// agreement for a freshly created VM: the env and secrets read from the host
// files are exactly what the VM is configured with and what its persisted
// fingerprints record, so the next run compares against what is really baked
// in (#342).
func TestPrepareSandboxBakesAndFingerprintsTheLoadedEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteFile(t, cp.UserConfigDir(), configpaths.EnvFileName, "FROM_HOST=yes\n")
	testutil.WriteFile(t, cp.UserConfigDir(), configpaths.EnvSecretFileName, "TOKEN=value-a@api.example.com\n")

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	key := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	createdSb := &msb.MockSandbox{
		Name_:    vm.ProjectVMName(key),
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	// No sandbox handle is registered, so GetSandbox reports "not found" and
	// PrepareSandbox creates the VM this run.
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := seedHomeState(key, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if len(mock.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected the project VM to be created, got %d creations", len(mock.CreatedSandboxCalls))
	}
	var cfg msbSdk.SandboxConfig
	applySandboxOpts(&cfg, mock.CreatedSandboxCalls[0].Opts)
	if cfg.Env["FROM_HOST"] != "yes" {
		t.Errorf("created VM env FROM_HOST = %q, want %q (env: %v)", cfg.Env["FROM_HOST"], "yes", cfg.Env)
	}
	if len(cfg.Secrets) != 1 || cfg.Secrets[0].EnvVar != "TOKEN" || cfg.Secrets[0].Value != "value-a" {
		t.Errorf("created VM secrets = %#v, want the TOKEN secret from the host file", cfg.Secrets)
	}

	st, err := state.ReadState(key)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	wantEnv := state.BuildEnvState(map[string]string{"FROM_HOST": "yes"})
	if st.EnvState.Hash != wantEnv.Hash {
		t.Errorf("persisted EnvState.Hash = %q, want %q (the env baked into the VM)", st.EnvState.Hash, wantEnv.Hash)
	}
	wantSecrets := state.BuildSecretState(cfg.Secrets)
	if st.SecretState.Hash != wantSecrets.Hash {
		t.Errorf("persisted SecretState.Hash = %q, want %q (the secrets baked into the VM)",
			st.SecretState.Hash, wantSecrets.Hash)
	}
}

// TestPrepareSandboxRunsStartupHook verifies that a home entry marked
// hook: startup is executed through an interactive AttachWith as the configured
// user (root) during startup, before the opencode daemon is ensured.
func TestPrepareSandboxRunsStartupHook(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home config that both provisions a script and marks it a
	// root-run startup hook.
	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\nnohup echo vpn &\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml",
		"home:\n"+
			"  .vpn/connect.sh:\n"+
			"    source: connect.sh\n"+
			"    hook: startup\n"+
			"    root: true\n")

	withMockAgentLatestVersion(t, "1.0.0")
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusStopped,
		ConnectSb: connectSb,
		StartSb:   connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.0.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	if connectSb.AttachUser != "root" {
		t.Errorf("startup hook AttachWith user = %q, want %q", connectSb.AttachUser, "root")
	}
}

// TestPrepareSandboxInvokesRuntimeHomeMigrationOffer verifies the session-start
// wiring: the opt-in legacy home-volume offer runs once, with the msb client,
// as part of PrepareSandbox.
func TestPrepareSandboxInvokesRuntimeHomeMigrationOffer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	resolutions := msb.WithCountingMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	var offeredWith msb.Client
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, c msb.Client, _ termio.UI) error {
		offeredWith = c
		return nil
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if offeredWith != mock {
		t.Errorf("offer client = %v, want the msb client from msb.Get()", offeredWith)
	}
	if *resolutions != 1 {
		t.Errorf("msb.Get resolved %d times, want once per PrepareSandbox", *resolutions)
	}
}

// TestPrepareSandboxBlocksOnPendingRuntimeHomeMigration pins option D: when the
// offer reports the migration is still pending (a non-interactive session, a
// quit, or a failed copy), PrepareSandbox aborts before resolving the home
// volume, so no fresh home is created and the project's recorded reference is
// left intact for a later copy.
func TestPrepareSandboxBlocksOnPendingRuntimeHomeMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	var createdVolumes int
	mock := &msb.MockMsbClient{
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			createdVolumes++
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"},
		state.HomeState{AgentVersion: "1.5.0", AgentSource: state.AgentSourceTool},
	)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		return runtimemigration.ErrMigrationPending
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	); err != runtimemigration.ErrMigrationPending { //nolint:errorlint // identity is the assertion: a wrapped error must fail here
		// Identity, not errors.Is: the branch returns the sentinel unchanged so
		// the user sees its guidance, while the fall-through would wrap it. A
		// wrapping assertion would accept both, hiding the deliberate block.
		t.Fatalf("PrepareSandbox() error = %v, want the bare ErrMigrationPending (unwrapped)", err)
	}
	if createdVolumes != 0 {
		t.Errorf("CreateVolume called %d times, want 0 (no fresh home created)", createdVolumes)
	}
}

// TestPrepareSandboxRuntimeHomeMigrationStateErrorIsNonFatal verifies the spec's
// anti-goal: a corrupt or unreadable launcher-global state file must not block
// the session. The offer is best-effort, so PrepareSandbox warns and continues
// rather than failing, and it never prompts.
func TestPrepareSandboxRuntimeHomeMigrationStateErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		},
	)

	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("runtime_home_migration: [unterminated"), 0o600); err != nil {
		t.Fatalf("write corrupt root state: %v", err)
	}

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = runtimemigration.Offer
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	selectCalls := 0
	ui := termio.NewTestMock(t)
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		selectCalls++
		return "", nil
	}

	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox() error = %v, want success despite a corrupt root state", err)
	}
	defer sess.Cleanup()

	if selectCalls != 0 {
		t.Errorf("Select called %d times, want 0", selectCalls)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning about the unreadable root state, got none")
	}
}

// TestPrepareSandboxPersistsMountFingerprintOnVMCreation verifies that a
// freshly created project VM records the configured bind mounts in the state
// file. Without this fingerprint the next run would compare against an empty
// state, report a mount change and recreate the VM on every startup.
//
// The workspace is pinned to a plain temp checkout: resolve-identity reads the
// process CWD, and an outside worktree whose .git records an absolute shared git
// dir would add the shared-git-dir bind, so the recorded mount set would depend
// on where the suite runs.
func TestPrepareSandboxPersistsMountFingerprintOnVMCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(testutil.InitRepo(t))

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		FSValue_: vmFS,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	// No sandbox handle is registered, so GetSandbox reports "not found" and
	// PrepareSandbox takes the create path (boot == vm.VMBootCreated).
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	mnts := mounts.Mounts{
		"/home/dev/.m2": {Source: filepath.Join(t.TempDir(), "m2")},
	}
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode", Mounts: mnts}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if len(mock.CreatedSandboxes) != 1 {
		t.Fatalf("expected the project VM to be created, got %d creations", len(mock.CreatedSandboxes))
	}

	st, err := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.MountState.Hash != mounts.Fingerprint(mnts) {
		t.Errorf("MountState.Hash = %q, want %q", st.MountState.Hash, mounts.Fingerprint(mnts))
	}
	if !slices.Equal(st.MountState.Entries, mounts.Describe(mnts)) {
		t.Errorf("MountState.Entries = %v, want %v", st.MountState.Entries, mounts.Describe(mnts))
	}
}

// TestPrepareSandboxRecordsBakedAgentVersion verifies that a run records the
// release baked into the runner image as this project's upgrade baseline. The
// image label is the authority: the resolver's own release deliberately differs,
// so a baseline taken from the build resolution would record the wrong value.
// This is the writer of record for profiles without an in-VM version probe
// (opencode2), whose boot-time provenance cannot fill the baseline.
func TestPrepareSandboxRecordsBakedAgentVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	withMockBuildVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "9.9.9", nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.identity.agent-version": "2.0.0"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    vm.ProjectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		FSValue_: vmFS,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	k := state.Key{Slug: slug, Agent: "opencode"}
	// No agent_version recorded: recording must fill it from the baked image.
	if err := seedHomeState(k, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.AgentVersion != "2.0.0" {
		t.Errorf("AgentVersion = %q, want the baked 2.0.0 recorded", st.AgentVersion)
	}
}

// TestPrepareSandboxRecreatesVMOnLiveMountChange is the end-to-end counterpart of
// the resolveReconfigDecision live-mount tests: a project VM whose persisted mounts no
// longer match the requested tmp size is destroyed and recreated, proving the
// mount read-back reaches the VM lifecycle and not only the decision.
func TestPrepareSandboxRecreatesVMOnLiveMountChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	k := state.Key{Slug: slug, Agent: "opencode"}

	// The existing VM's persisted mounts carry the previous tmp size. Its image
	// and the seeded env/secret/network/mount fingerprints match the run, so the
	// mount read-back is the only thing that can drive the recreate.
	shellOut := map[string]msb.ShellResult{dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil)}
	existing := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(k),
		Status_:   msbSdk.SandboxStatusRunning,
		Cfg:       &msbSdk.SandboxConfig{},
		ConnectSb: &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil), ShellOut: shellOut},
		Mounts: map[string]msbSdk.MountConfig{
			mounts.TmpMountPath: msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: 1024}),
			mounts.WorkspaceMountPath: msbSdk.Mount.Bind(
				"/host/repo",
				msbSdk.MountOptions{QuotaMiB: options.DefaultWorkspaceQuotaMiB},
			),
			mounts.VMHomeDir: msbSdk.Mount.Named("home-vol", msbSdk.MountOptions{}),
		},
	}
	createdSb := &msb.MockSandbox{Name_: vm.ProjectVMName(k), FSValue_: msb.NewTestFS(nil, nil), ShellOut: shellOut}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	// After the VM is destroyed, GetSandbox must report it gone so the create path
	// runs; a static handle would leave the mock attaching to the old VM.
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		if existing.DidDestroy() {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: vm.ProjectVMName(k)}
		}
		return existing, nil
	}
	msb.WithMsbMock(t, mock)

	opts := options.RunOptions{Agent: "opencode", TmpSize: "2G"}
	seedUI := termio.NewTestMock(t)
	_, fingerprints := loadEnvFingerprints(opts, &seedUI)
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
		EnvState:     fingerprints.Env,
		SecretState:  fingerprints.Secret,
		NetworkState: fingerprints.Network,
		MountState:   fingerprints.Mount,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), sandboxRunOptions(t, opts), false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if !existing.DidDestroy() {
		t.Error("expected the existing VM to be destroyed for the live tmp-size change")
	}
	if len(mock.CreatedSandboxes) != 1 {
		t.Fatalf("expected the VM to be recreated once, got %d creations", len(mock.CreatedSandboxes))
	}
	if !contains(joinStrings(ui.WarnCalls), "/tmp tmpfs size") {
		t.Errorf("expected a tmp-size rebuild warning, got %v", ui.WarnCalls)
	}
	if !contains(joinStrings(ui.VerboseCalls), "recreate: true") {
		t.Errorf("expected the recreate decision to be logged, got %v", ui.VerboseCalls)
	}
}

// TestPrepareSandboxRunsDefaultAgentDaemon pins that a run with the default
// agent resolves to a daemon profile and drives that profile's own start and
// health commands. The other flow tests pin the v1 agent explicitly (their
// subject is the project, not the agent), so this test keeps whichever agent is
// the default wired through orchestration.
func TestPrepareSandboxRunsDefaultAgentDaemon(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	defaultAgent, ok := agent.Lookup(agent.DefaultAgentName)
	if !ok {
		t.Fatalf("default agent %q is not registered", agent.DefaultAgentName)
	}
	provider, ok := agent.AsDaemonProvider(defaultAgent)
	if !ok {
		t.Fatalf("default agent %q is not a daemon provider", agent.DefaultAgentName)
	}

	installPrepareSandboxDockerMock(t)
	mock := installPrepareSandboxMSBMock(t)
	created, ok := mock.CreatedSandbox.(*msb.MockSandbox)
	if !ok {
		t.Fatalf("created sandbox is %T, want *msb.MockSandbox", mock.CreatedSandbox)
	}
	detached := &[]msb.MockDetachedCall{}
	created.ExecDetachedCalls = detached
	seedMatchingHomeState(t, state.Key{Slug: git.MustCheckoutSlug(), Agent: agent.DefaultAgentName})

	// The first probe reports not-ready so the run takes the start-then-poll
	// path, exercising both the start and health commands; later probes report
	// healthy so the run finishes. The body carries both agents' healthy
	// markers, keeping the mock agnostic to which profile is the default.
	var commands []string
	healthProbes := 0
	opts := options.RunOptions{Seams: options.Seams{DaemonHealth: func(
		_ context.Context, _ msb.Sandbox, command string,
	) (string, int, error) {
		commands = append(commands, command)
		if command == provider.DaemonHealthCmd() {
			healthProbes++
			if healthProbes == 1 {
				return "", 0, nil
			}
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	}}}

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), resolvedRunOptions(t, opts), false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox(default %q): %v", agent.DefaultAgentName, err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	assertDetachedHelperLaunch(t, *detached, "agent-daemon", provider.DaemonCommand(false).Binary)
	if !slices.Contains(commands, provider.DaemonHealthCmd()) {
		t.Errorf("default agent %q daemon health was not probed; commands = %v", agent.DefaultAgentName, commands)
	}
}

// TestPrepareSandboxUsesCheckoutSlugForStateAndRepoSlugForLabel pins the #144
// split for two clones of one origin: each clone gets its own VM name (and
// runner image tag) from its checkout slug while the grouping label stays the
// shared repo slug, so `list --label project=<repo>` groups both.
func TestPrepareSandboxUsesCheckoutSlugForStateAndRepoSlugForLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	firstClone := newOriginClone(t)
	secondClone := newOriginClone(t)
	firstIdentity := mustIdentity(t, firstClone)
	secondIdentity := mustIdentity(t, secondClone)
	if firstIdentity.RepoSlug != secondIdentity.RepoSlug {
		t.Fatalf("repo slugs differ for one origin: %q vs %q", firstIdentity.RepoSlug, secondIdentity.RepoSlug)
	}
	if firstIdentity.CheckoutSlug == secondIdentity.CheckoutSlug {
		t.Fatalf("checkout slugs collide across clones: %q", firstIdentity.CheckoutSlug)
	}

	inspectedTag := installPrepareSandboxDockerMock(t)
	ui := termio.NewTestMock(t)

	t.Chdir(firstClone)
	firstKey := state.Key{Slug: firstIdentity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, firstKey)
	firstMock := installPrepareSandboxMSBMock(t)
	firstSession, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox in first clone: %v", err)
	}
	defer firstSession.Cleanup()
	firstTag := *inspectedTag
	firstName := vm.ProjectVMName(firstKey)
	firstLabels := createdProjectVMConfig(t, firstMock, firstName).Labels

	t.Chdir(secondClone)
	secondKey := state.Key{Slug: secondIdentity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, secondKey)
	secondMock := installPrepareSandboxMSBMock(t)
	secondSession, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox in second clone: %v", err)
	}
	defer secondSession.Cleanup()
	secondTag := *inspectedTag
	secondName := vm.ProjectVMName(secondKey)
	secondLabels := createdProjectVMConfig(t, secondMock, secondName).Labels

	if firstName == secondName {
		t.Fatalf("both clones created the same VM name %q", firstName)
	}
	if got := createdProjectVMNames(firstMock); len(got) != 1 || got[0] != firstName {
		t.Errorf("first clone created project VMs %v, want exactly [%s]", got, firstName)
	}
	if got := createdProjectVMNames(secondMock); len(got) != 1 || got[0] != secondName {
		t.Errorf("second clone created project VMs %v, want exactly [%s]", got, secondName)
	}
	if firstTag != naming.DockerImageRef(firstIdentity.CheckoutSlug, "opencode") {
		t.Errorf("first clone image tag = %q, want the checkout-slug tag", firstTag)
	}
	if secondTag != naming.DockerImageRef(secondIdentity.CheckoutSlug, "opencode") {
		t.Errorf("second clone image tag = %q, want the checkout-slug tag", secondTag)
	}
	for clone, labels := range map[string]map[string]string{"first": firstLabels, "second": secondLabels} {
		if got := labels[naming.LabelProject]; got != firstIdentity.RepoSlug {
			t.Errorf("%s clone %s label = %q, want the repo slug %q",
				clone, naming.LabelProject, got, firstIdentity.RepoSlug)
		}
	}
	if got := firstSession.Key(); got != firstKey {
		t.Errorf("first session key = %+v, want %+v", got, firstKey)
	}
	if got := secondSession.Key(); got != secondKey {
		t.Errorf("second session key = %+v, want %+v", got, secondKey)
	}
}

// TestPrepareSandboxSkipVMKeepsLegacySlugWhileMigrationPending pins the
// read-only prepare: with the VM skipped, a pending migration prints the "would"
// notice and the rest of the pipeline keeps the legacy repo slug, so no
// post-migration artifact is presented as if the migration had run.
func TestPrepareSandboxSkipVMKeepsLegacySlugWhileMigrationPending(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must produce distinct repo/checkout slugs, both %q", identity.RepoSlug)
	}

	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	seedMatchingHomeState(t, legacyKey)

	inspectedTag := installPrepareSandboxDockerMock(t)
	ui := termio.NewTestMock(t)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	session, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox (skip VM): %v", err)
	}
	defer session.Cleanup()

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Would migrate this checkout to its own project identity",
		state.SlugDir(identity.RepoSlug),
		state.SlugDir(identity.CheckoutSlug),
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("dry-run notice missing %q:\n%s", want, notice)
		}
	}
	if *inspectedTag != naming.DockerImageRef(identity.RepoSlug, "opencode") {
		t.Errorf("image tag = %q, want the legacy repo-slug tag %q",
			*inspectedTag, naming.DockerImageRef(identity.RepoSlug, "opencode"))
	}
	if session.Key().Slug != identity.RepoSlug {
		t.Errorf("session slug = %q, want the legacy repo slug %q", session.Key().Slug, identity.RepoSlug)
	}
	if statePresent(t, state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}) {
		t.Error("dry run moved state onto the checkout slug")
	}
}

// TestPrepareSandboxMigratesOnFirstRun pins the legacy-identity migration: with
// legacy state and a legacy VM present, the first run destroys the legacy VM,
// moves the state onto the checkout slug, and recreates the VM under the
// checkout name.
func TestPrepareSandboxMigratesOnFirstRun(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)

	legacyKey := state.Key{Slug: identity.RepoSlug, Agent: "opencode"}
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: "opencode"}
	seedMatchingHomeState(t, legacyKey)

	legacyHandle := &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(legacyKey),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: msb.NewMockSandbox(msb.SandboxOpts{}),
	}
	inspectedTag := installPrepareSandboxDockerMock(t)
	ui := termio.NewTestMock(t)

	mock := installPrepareSandboxMSBMock(t)
	mock.GetSandboxFn = func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == vm.ProjectVMName(legacyKey) {
			return legacyHandle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}

	t.Chdir(clone)
	session, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox (migrate): %v", err)
	}
	defer session.Cleanup()

	if !legacyHandle.Destroyed {
		t.Error("legacy origin-keyed VM was not destroyed")
	}
	if !statePresent(t, checkoutKey) {
		t.Error("state file did not arrive under the checkout slug")
	}
	if got := createdProjectVMNames(mock); len(got) != 1 || got[0] != vm.ProjectVMName(checkoutKey) {
		t.Errorf("created sandboxes = %v, want the checkout-slug name %q",
			got, vm.ProjectVMName(checkoutKey))
	}
	if *inspectedTag != naming.DockerImageRef(identity.CheckoutSlug, "opencode") {
		t.Errorf("image tag = %q, want the checkout-slug tag", *inspectedTag)
	}
	if session.Key() != checkoutKey {
		t.Errorf("session key = %+v, want %+v", session.Key(), checkoutKey)
	}
}

// TestPrepareSandboxAdoptionFailureReturnsError pins the fail-closed wiring on
// the run path: an unreadable legacy state aborts PrepareSandbox before it
// resolves a version, ensures an image, or creates a VM.
func TestPrepareSandboxAdoptionFailureReturnsError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	identity := mustIdentity(t, clone)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	legacyDir := state.KeyDir(state.Key{Slug: identity.RepoSlug, Agent: "opencode"})
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(
		context.Background(),
		sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
		false,
		&ui,
	); err == nil ||
		!strings.Contains(err.Error(), "check for a pending legacy identity") {
		t.Fatalf(
			"PrepareSandbox with an unreadable legacy state: error = %v, want the fail-closed predicate error",
			err,
		)
	}
}

// TestPrepareSandboxDryRunVMSkipsTheVM pins that --dry-run-vm skips the VM
// lifecycle and attaches at the default workspace directory. It launches from a
// checkout root so the target is exactly /workspace regardless of where the
// test binary runs.
func TestPrepareSandboxDryRunVMSkipsTheVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(testutil.InitRepo(t))

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		},
	)
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Seams: daemonHealthSeams(t)}),
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm): %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	if sess.Sandbox() != nil {
		t.Error("expected a nil sandbox for dry-run-vm")
	}
	if sess.Target() != "/workspace" {
		t.Errorf("expected default target /workspace, got %q", sess.Target())
	}
	if sess.GuestCWD() != "/workspace" {
		t.Errorf("expected guest CWD /workspace, got %q", sess.GuestCWD())
	}
}

// TestPrepareSandboxDryRunVMFreshProjectCreatesNoHome pins #429 end to end: a
// --dry-run-vm run on a project without state neither creates a home volume nor
// writes state, so the first real run creates and prefills the home itself.
func TestPrepareSandboxDryRunVMFreshProjectCreatesNoHome(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Chdir(testutil.InitRepo(t))

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			t.Errorf("dry-run-vm created home volume %q", name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), resolvedRunOptions(t, options.RunOptions{}), true, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm): %v", err)
	}
	if sess.Sandbox() != nil {
		t.Error("expected a nil sandbox for dry-run-vm")
	}
	if len(mock.CreatedSandboxes) != 0 {
		t.Errorf("expected no prefill sandbox, got %d", len(mock.CreatedSandboxes))
	}
	k := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("ReadState err = %v, want ErrStateNotFound", err)
	}
}

// TestPrepareSandboxDryRunVMServeOnlyPublishesPort pins that a serve-only run
// without a published port probes a free host port and records it on the
// Session, even when --dry-run-vm skips the VM.
func TestPrepareSandboxDryRunVMServeOnlyPublishesPort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubPrepareSandboxImage(t)

	slug := git.MustCheckoutSlug()
	k := state.Key{Slug: slug, Agent: "opencode"}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	opts := options.RunOptions{Agent: "opencode", ServeOnly: true}
	sess, err := PrepareSandbox(context.Background(), resolvedRunOptions(t, opts), true, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm, serve-only): %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	if sess.ServeHostPort() == 0 {
		t.Error("expected a probed serve host port on the Session")
	}
}

// TestPrepareSandboxDryRunVMLaunchSubdirectory pins the guest working
// directory: launched from a repository subdirectory, the bound /workspace is
// the checkout root and the agent starts in the launched subdirectory.
func TestPrepareSandboxDryRunVMLaunchSubdirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	repo := testutil.InitRepo(t)
	sub := filepath.Join(repo, "internal", "foo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	t.Chdir(sub)

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	seedHomeState(
		state.Key{Slug: slug, Agent: "opencode"},
		state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  "sha256:abc123",
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		},
	)
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Seams: daemonHealthSeams(t)}),
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox (dry-run-vm, subdirectory): %v", err)
	}
	if sess.Target() != "/workspace/internal/foo" {
		t.Errorf("expected target /workspace/internal/foo, got %q", sess.Target())
	}
	if sess.GuestCWD() != "/workspace/internal/foo" {
		t.Errorf("expected guest CWD /workspace/internal/foo, got %q", sess.GuestCWD())
	}
}

// TestPrepareSandboxSkipVMRecordsStagesInOrder pins the recorded order of a
// --dry-run-vm prepare: every prepare-owned stage is reached in lifecycle order, the
// VM ones included, so reordering, adding or removing a stage in
// PrepareSandbox's list changes the trace and fails here.
func TestPrepareSandboxSkipVMRecordsStagesInOrder(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	clone := newOriginClone(t)
	installPrepareSandboxDockerMock(t)
	ui := termio.NewTestMock(t)
	t.Chdir(clone)
	installPrepareSandboxMSBMock(t)

	trace := &lifecycle.Trace{}
	session, err := PrepareSandbox(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Agent: "opencode", Trace: trace}),
		true,
		&ui,
	)
	if err != nil {
		t.Fatalf("PrepareSandbox (skip VM): %v", err)
	}
	defer session.Cleanup()

	if got, want := trace.Stages(), prepareOwnedStages(t); !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

// TestPrepareSandboxStopsAtTheFailingStage pins how the pipeline fails: the
// first failing stage is the last one recorded in the trace, no later stage
// runs, and the stage's cause reaches the caller. Every case stubs the daemon
// health probe, so a regression that lets the run continue fails fast at the
// stage assertion instead of waiting out the production probe timeout.
func TestPrepareSandboxStopsAtTheFailingStage(t *testing.T) {
	type arrangement struct {
		opts   options.RunOptions
		skipVM bool
		ui     termio.UI
	}
	interactiveUI := func(t *testing.T, choose func(prompt string) (string, error)) termio.UI {
		ui := termio.NewTestMock(t)
		ui.IsInteractiveResult = true
		ui.SelectFn = func(prompt string, _ []termio.Choice, _ string) (string, error) { return choose(prompt) }
		return &ui
	}
	nonInteractiveUI := func(t *testing.T) termio.UI {
		ui := termio.NewTestMock(t)
		return &ui
	}
	// seedReusableHome records a home volume and image digest that match the
	// stubbed runner image, so a run reuses them unless the case changes that.
	seedReusableHome := func(t *testing.T, k state.Key, imageDigest string) {
		t.Helper()
		if err := seedHomeState(k, state.HomeState{
			HomeVolume:   "home-vol",
			ImageDigest:  imageDigest,
			AgentVersion: "1.5.0",
			AgentSource:  state.AgentSourceTool,
		}); err != nil {
			t.Fatalf("seedHomeState: %v", err)
		}
	}
	projectKey := func() state.Key { return state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"} }

	cases := []struct {
		name string
		// arrange installs the failure; cause is the error it injects, or nil
		// when the failure is not an injectable value (e.g. an unknown agent).
		arrange       func(t *testing.T, cause error) arrangement
		cause         error
		wantLastStage lifecycle.StageID
	}{
		{
			name: "unknown agent",
			arrange: func(t *testing.T, _ error) arrangement {
				t.Chdir(newOriginClone(t))
				return arrangement{
					opts:   sandboxRunOptions(t, options.RunOptions{Agent: "no-such-agent"}),
					skipVM: true,
					ui:     nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.ResolveIdentity,
		},
		{
			name:  "workspace directory removed",
			cause: fs.ErrNotExist,
			arrange: func(t *testing.T, _ error) arrangement {
				dir := t.TempDir()
				t.Chdir(dir)
				if err := os.Remove(dir); err != nil {
					t.Skipf("cannot remove cwd: %v", err)
				}
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.ResolveIdentity,
		},
		{
			name:  "upgrade prompt fails",
			cause: errors.New("selection failed"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				if err := seedHomeState(
					projectKey(),
					state.HomeState{AgentVersion: "1.0.0", AgentSource: state.AgentSourceTool},
				); err != nil {
					t.Fatalf("seedHomeState: %v", err)
				}
				withMockAgentLatestVersion(t, "2.0.0")
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   interactiveUI(t, func(string) (string, error) { return "", cause }),
				}
			},
			wantLastStage: lifecycle.ResolveVersion,
		},
		{
			name:  "agent release cannot be resolved for the build",
			cause: errors.New("cannot resolve opencode version"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				msb.WithMsbMock(t, &msb.MockMsbClient{})
				withMockBuildVersionResolver(
					t,
					func(context.Context, agent.Agent, string) (string, error) { return "", cause },
				)
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.EnsureImage,
		},
		{
			name:  "home volume cannot be created",
			cause: errors.New("volume creation failed"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				stubPrepareSandboxImage(t)
				msb.WithMsbMock(t, &msb.MockMsbClient{
					ImageGetFn: func(context.Context, string) error { return nil },
					CreateVolumeFn: func(context.Context, string, ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
						return nil, cause
					},
				})
				if err := seedHomeState(
					projectKey(),
					state.HomeState{AgentVersion: "1.5.0", AgentSource: state.AgentSourceTool},
				); err != nil {
					t.Fatalf("seedHomeState: %v", err)
				}
				// A real run: under skip-VM a fresh home volume is never created.
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.EnsureHome,
		},
		{
			name: "home manifest is malformed",
			arrange: func(t *testing.T, _ error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				// A non-string source value is rejected while reading the home manifest.
				testutil.WriteFile(t, configpaths.Get().ProjectConfigDir(), "config.yaml",
					"home:\n  .tool/x:\n    source: 123\n")
				stubPrepareSandboxImage(t)
				msb.WithMsbMock(t, &msb.MockMsbClient{
					ImageGetFn: func(context.Context, string) error { return nil },
					Volumes:    []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
				})
				seedReusableHome(t, projectKey(), "sha256:abc123")
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.PlanReconfig,
		},
		{
			name: "rebuild prompt is quit while another client is attached",
			arrange: func(t *testing.T, _ error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				stubPrepareSandboxImage(t)
				k := projectKey()
				existing := existingProjectVMHandle(k)
				msb.WithMsbMock(t, &msb.MockMsbClient{
					ImageGetFn:   func(context.Context, string) error { return nil },
					Volumes:      []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
					GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) { return existing, nil },
				})
				// An empty NetworkState counts as changed against the live VM, so
				// the run wants a rebuild; the attached client turns it into a prompt.
				seedReusableHome(t, k, "sha256:abc123")
				release, err := state.AcquireClientLease(k)
				if err != nil {
					t.Fatalf("AcquireClientLease: %v", err)
				}
				t.Cleanup(release)
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   interactiveUI(t, func(string) (string, error) { return "q", nil }),
				}
			},
			wantLastStage: lifecycle.PlanReconfig,
		},
		{
			name:  "home volume migration fails after an image switch",
			cause: errors.New("create volume failed"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				stubPrepareSandboxImage(t)
				k := projectKey()
				existing := existingProjectVMHandle(k)
				msb.WithMsbMock(t, &msb.MockMsbClient{
					ImageGetFn:   func(context.Context, string) error { return nil },
					Volumes:      []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
					GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) { return existing, nil },
					CreateVolumeFn: func(context.Context, string, ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
						return nil, cause
					},
				})
				// A recorded digest other than the built sha256:abc123 makes the run
				// switch images and ask how to handle the home volume.
				seedReusableHome(t, k, "sha256:old")
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui: interactiveUI(t, func(prompt string) (string, error) {
						if strings.Contains(prompt, "Docker image changed") {
							return "m", nil
						}
						return "", nil
					}),
				}
			},
			wantLastStage: lifecycle.PlanReconfig,
		},
		{
			name:  "project VM lookup fails",
			cause: errors.New("lookup failed"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				stubPrepareSandboxImage(t)
				msb.WithMsbMock(t, &msb.MockMsbClient{
					ImageGetFn:   func(context.Context, string) error { return nil },
					Volumes:      []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
					GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) { return nil, cause },
				})
				seedReusableHome(t, projectKey(), "sha256:abc123")
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.PlanReconfig,
		},
		{
			name:  "project VM lock cannot be acquired",
			cause: errors.New("flock boom"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(newOriginClone(t))
				installPrepareSandboxDockerMock(t)
				installPrepareSandboxMSBMock(t)
				seams := options.Seams{ProjectVMLock: func(state.Key) (func(), error) { return nil, cause }}
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode", Seams: seams}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.EnsureVM,
		},
		{
			name:  "sandbox setup fails",
			cause: errors.New("shell failed"),
			arrange: func(t *testing.T, cause error) arrangement {
				t.Chdir(testutil.InitRepo(t))
				stubPrepareSandboxImage(t)
				k := projectKey()
				mock := &msb.MockMsbClient{
					ImageGetFn: func(context.Context, string) error { return nil },
					Volumes:    []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
				}
				mock.SetGotSandbox(&msb.MockSandboxHandle{
					Name_:     vm.ProjectVMName(k),
					Status_:   msbSdk.SandboxStatusRunning,
					ConnectSb: &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil), ShellErr: cause},
				})
				msb.WithMsbMock(t, mock)
				seedReusableHome(t, k, "sha256:abc123")
				return arrangement{
					opts: sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
					ui:   nonInteractiveUI(t),
				}
			},
			wantLastStage: lifecycle.SetupSandbox,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			run := tc.arrange(t, tc.cause)
			trace := &lifecycle.Trace{}
			run.opts.Trace = trace

			_, err := PrepareSandbox(context.Background(), run.opts, run.skipVM, run.ui)

			if err == nil {
				t.Fatal("PrepareSandbox succeeded, want an error")
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.cause)
			}
			stages := trace.Stages()
			if len(stages) == 0 || stages[len(stages)-1] != tc.wantLastStage {
				t.Errorf("trace = %v, want it to end at %s", stages, tc.wantLastStage)
			}
		})
	}
}

// cloneOrigin is the shared origin URL for the identity fixtures, so two clones
// map to one repo slug while keeping distinct checkout slugs.
const cloneOrigin = "https://example.com/acme/widget.git"

// newOriginClone builds a minimal origin-holding repo layout (a .git dir whose
// config names the shared origin), the same fixture shape used by internal/git's
// identity tests. It returns the symlink-resolved path so assertions match what
// ResolveIdentity derives.
func newOriginClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	config := "[remote \"origin\"]\n\turl = " + cloneOrigin + "\n"
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	return root
}

// installPrepareSandboxDockerMock wires the docker and agent-version mocks a
// PrepareSandbox run needs, and returns a pointer to the runner image tag the
// build inspected (the tag the pipeline resolved).
func installPrepareSandboxDockerMock(t *testing.T) *string {
	t.Helper()
	withMockAgentLatestVersion(t, "1.0.0")
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	inspectedTag := new(string)
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			*inspectedTag = ref
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Env:    []string{"PATH=/usr/bin"},
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})
	return inspectedTag
}

// installPrepareSandboxMSBMock installs an msb mock whose project VM does not
// exist yet (so PrepareSandbox takes the create path) and whose created sandbox
// is usable by the daemon step.
func installPrepareSandboxMSBMock(t *testing.T) *msb.MockMsbClient {
	t.Helper()
	createdSb := &msb.MockSandbox{
		Name_:    "created-vm",
		FSValue_: msb.NewTestFS(nil, nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)
	return mock
}

// seedMatchingHomeState seeds a home state whose image/agent version matches the
// mocked image, so home resolution reuses the volume without prompting.
func seedMatchingHomeState(t *testing.T, k state.Key) {
	t.Helper()
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.0.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
}

// createdProjectVMConfig applies the recorded CreateSandbox options for the
// named project VM and returns the config it was created with. Other
// CreateSandbox calls (a home-volume prefill) are ignored.
func createdProjectVMConfig(t *testing.T, mock *msb.MockMsbClient, name string) msbSdk.SandboxConfig {
	t.Helper()
	for _, call := range mock.CreatedSandboxCalls {
		if call.Name != name {
			continue
		}
		var cfg msbSdk.SandboxConfig
		for _, opt := range call.Opts {
			opt(&cfg)
		}
		return cfg
	}
	t.Fatalf("no CreateSandbox call for %q; calls: %v", name, mock.CreatedSandboxes)
	return msbSdk.SandboxConfig{}
}

// createdProjectVMNames returns the names of the project-VM-shaped CreateSandbox
// calls, filtering out the home-volume prefill task sandbox.
func createdProjectVMNames(mock *msb.MockMsbClient) []string {
	var names []string
	for _, name := range mock.CreatedSandboxes {
		info := naming.ParseVMName(name)
		if info.Slug != "" && !info.Task {
			names = append(names, name)
		}
	}
	return names
}

// stubPrepareSandboxImage pins agent-version resolution and the Docker
// build/inspect path so PrepareSandbox resolves a runner image with digest
// "sha256:abc123" without the network or a Docker daemon.
func stubPrepareSandboxImage(t *testing.T) {
	t.Helper()

	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				ID: "sha256:abc123",
				Config: &dockerspec.DockerOCIImageConfig{
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})
}

// existingProjectVMHandle is a running project VM handle backed by an empty
// sandbox, so the reconfig decision reads a live VM without any Docker or KVM.
func existingProjectVMHandle(k state.Key) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Name_:     vm.ProjectVMName(k),
		Status_:   msbSdk.SandboxStatusRunning,
		Cfg:       &msbSdk.SandboxConfig{},
		ConnectSb: &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil)},
	}
}

// prepareOwnedStages returns the stages PrepareSandbox owns, in the order of
// specs/vm/lifecycle.md: resolve-identity through setup-sandbox.
func prepareOwnedStages(t *testing.T) []lifecycle.StageID {
	t.Helper()
	order := lifecycle.Order()
	first := slices.Index(order, lifecycle.ResolveIdentity)
	last := slices.Index(order, lifecycle.SetupSandbox)
	if first < 0 || last < first {
		t.Fatalf("lifecycle.Order() = %v lacks the prepare-owned stage range", order)
	}
	return order[first : last+1]
}

// opencodeProvider returns the opencode profile's DaemonProvider so tests can
// build the exact commands the production path shells out with.
func opencodeProvider(t *testing.T) agent.DaemonProvider {
	t.Helper()
	p, ok := agent.AsDaemonProvider(opencodeAgent(t))
	if !ok {
		t.Fatal("opencode agent does not implement DaemonProvider")
	}
	return p
}

// mustIdentity resolves a fixture cwd to its project identity, failing the test
// on the now-fallible ResolveIdentity: fixtures always point at real paths.
func mustIdentity(t testing.TB, cwd string) git.Identity {
	t.Helper()
	identity, err := git.ResolveIdentity(cwd)
	require.NoError(t, err)
	return identity
}

func applySandboxOpts(cfg *msbSdk.SandboxConfig, opts []msbSdk.SandboxOption) {
	for _, o := range opts {
		o(cfg)
	}
}

// assertDetachedHelperLaunch fails the test unless the recorded detached calls
// are exactly one launch of binary under the helper marker; the marker shape is
// what the kill path matches on, so the callers (dockerd, the daemon) are pinned
// to it.
func assertDetachedHelperLaunch(t *testing.T, calls []msb.MockDetachedCall, marker, binary string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("detached calls = %+v, want one", calls)
	}
	if calls[0].Cmd != "env" {
		t.Errorf("launch binary = %q, want %q", calls[0].Cmd, "env")
	}
	args := calls[0].Args
	if len(args) < 2 || args[0] != "AGENTS_SANDBOX_DETACHED_JOB="+marker || args[1] != binary {
		t.Errorf("launch args = %v, want the %s marker then %q", args, marker, binary)
	}
}

// dockerdBinaryCheckCmd is the probe vm.SetUpSandbox runs to detect dockerd in
// the VM. A mock answering it with a failure keeps a PrepareSandbox test from
// starting dockerd; it must match the command in package vm.
const dockerdBinaryCheckCmd = "command -v dockerd >/dev/null 2>&1"

// daemonHealthSeams returns options.Seams with the daemon health probe stubbed
// healthy for the opencode provider, so a PrepareSandbox test never probes a
// real daemon.
func daemonHealthSeams(t *testing.T) options.Seams {
	t.Helper()
	return options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	}}
}

// sandboxRunOptions resolves opts' capabilities and, unless a test already set
// one, stubs the daemon health probe via daemonHealthSeams: the options a
// PrepareSandbox test that reaches setup-sandbox needs.
func sandboxRunOptions(t *testing.T, opts options.RunOptions) options.RunOptions {
	t.Helper()
	if opts.Seams.DaemonHealth == nil {
		opts.Seams.DaemonHealth = daemonHealthSeams(t).DaemonHealth
	}
	return resolvedRunOptions(t, opts)
}

// withMockBuildVersionResolver overrides the release resolver PrepareSandbox
// injects into the image build, so a test can observe the requested version.
func withMockBuildVersionResolver(t *testing.T, resolve image.AgentVersionResolver) {
	t.Helper()
	orig := buildVersionResolver
	buildVersionResolver = resolve
	t.Cleanup(func() { buildVersionResolver = orig })
}

// TestPrepareSandboxServeOnlyPortExhausted covers the PlanReconfig stage's
// resolveServeHostPort error: a serve-only run whose user mappings claim the
// whole probe range aborts before any VM work rather than publishing the agent
// on a colliding port.
func TestPrepareSandboxServeOnlyPortExhausted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubPrepareSandboxImage(t)

	k := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  state.AgentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	ui := termio.NewTestMock(t)
	opts := options.RunOptions{Agent: "opencode", ServeOnly: true, Ports: exhaustedPortMappings()}
	if _, err := PrepareSandbox(context.Background(), resolvedRunOptions(t, opts), true, &ui); err == nil {
		t.Fatal("expected PrepareSandbox to fail when no free serve host port remains")
	} else if !strings.Contains(err.Error(), "no free host port") {
		t.Errorf("error = %v, want it to mention no free host port", err)
	}
}

// TestDesiredFingerprintsUnchangedWithoutPerSlugFiles pins the no-spurious-
// recreate guarantee: putting the slug in the scope chain while the per-slug dir
// holds no env/secret files leaves the env and secret fingerprints unchanged.
func TestDesiredFingerprintsUnchangedWithoutPerSlugFiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	scopes := configpaths.ConfigScopes(configpaths.Get(), "")
	testutil.WriteFile(t, scopes[0].Dir, configpaths.EnvFileName, "A=generic\n")
	testutil.WriteFile(t, scopes[1].Dir, configpaths.EnvFileName, "B=project\n")
	testutil.WriteFile(t, scopes[0].Dir, configpaths.EnvSecretFileName, "TOKEN=value@user.example\n")
	testutil.WriteFile(t, scopes[1].Dir, configpaths.EnvSecretFileName, "OTHER=value@project.example\n")

	ui := termio.NewTestMock(t)
	_, generic := loadEnvFingerprints(options.RunOptions{}, &ui)
	_, withSlug := loadEnvFingerprints(options.RunOptions{ConfigSlug: "widget-bbb"}, &ui)

	if !reflect.DeepEqual(generic.Env, withSlug.Env) {
		t.Errorf("env fingerprint changed with an absent per-slug env file: %#v vs %#v", generic.Env, withSlug.Env)
	}
	if !reflect.DeepEqual(generic.Secret, withSlug.Secret) {
		t.Errorf(
			"secret fingerprint changed with absent per-slug secret files: %#v vs %#v",
			generic.Secret,
			withSlug.Secret,
		)
	}
}

// TestPrepareSandboxStopsBeforeTheVMOnAnUnapprovedRead pins the run-side stop: a
// project input read as changed or gone since its approval (here the env file,
// read after the image is ready) keeps the run from creating or starting the
// project VM. The home volume's prefill sandbox carries no project input.
func TestPrepareSandboxStopsBeforeTheVMOnAnUnapprovedRead(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withMockBuildVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "9.9.9", nil
	})
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{ID: "sha256:abc123", Config: &dockerspec.DockerOCIImageConfig{}}, nil
		},
	})
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: &msb.MockSandbox{FSValue_: msb.NewTestFS(nil, nil)},
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	projectDir := configpaths.Get().ProjectConfigDirAbs()
	envFile := filepath.Join(projectDir, "env")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func() error{
		"changed": func() error { return os.WriteFile(envFile, []byte("A=1\nHTTP_PROXY=http://evil\n"), 0o600) },
		"deleted": func() error { return os.Remove(envFile) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(envFile, []byte("A=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := change(); err != nil {
				t.Fatal(err)
			}
			restore := projectguard.Install([]string{projectDir}, map[string]projectguard.Approval{
				envFile: projectguard.ApprovalOf([]byte("A=1\n")),
			})
			defer restore()

			ui := termio.NewTestMock(t)
			_, err := PrepareSandbox(
				context.Background(),
				sandboxRunOptions(t, options.RunOptions{Agent: "opencode"}),
				false,
				&ui,
			)
			if !errors.Is(err, projectguard.ErrNotApproved) {
				t.Fatalf("PrepareSandbox = %v, want ErrNotApproved", err)
			}
			projectVM := vm.ProjectVMName(state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"})
			if slices.Contains(mock.CreatedSandboxes, projectVM) {
				t.Errorf("created the project VM %s after an unapproved read", projectVM)
			}
		})
	}
}
