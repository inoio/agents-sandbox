package vm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/runtimemigration"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
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
	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

// TestPrepareSandboxUpgradeRebuildsImage covers the upgrade branch of
// PrepareSandbox: when the user accepts an opencode upgrade, the image is
// force-rebuilt and the run reports the newer version being baked in.
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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, ui)
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

// TestPrepareSandboxLoadsHomeYamlOnce verifies that a full startup loads the
// home manifests exactly once: a missing host source is warned about a
// single time. This guards against the regression where config files were
// loaded twice (once in resolveReconfigDecision and once in setUpSandbox), which made
// the warning (and the underlying BuildHomeFilesWithModes computation) run twice.
func TestPrepareSandboxLoadsHomeYamlOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home config whose host source does not exist triggers the
	// "does not exist on the host; skipping" warning. The source is a relative
	// path, resolved against the (empty) project config dir, so it cannot exist.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "config.yaml", "home:\n  missing-file.txt: missing-file.txt\n")

	// Pin the resolved opencode version so image resolution needs no network.
	sandboximage.WithMockAgentVersion(t, "1.0.0")

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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	// The opencode daemon is healthy inside the VM, so ensureDaemon returns
	// immediately instead of starting and polling.
	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	key := state.Key{Slug: git.MustCheckoutSlug(), Agent: "opencode"}
	createdSb := &msb.MockSandbox{
		Name_:    projectVMName(key),
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
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

	sandboximage.WithMockAgentVersion(t, "1.0.0")
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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	var offeredWith msb.Client
	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, c msb.Client, _ termio.UI) error {
		offeredWith = c
		return nil
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if offeredWith != mock {
		t.Errorf("offer client = %v, want the msb client from msb.Get()", offeredWith)
	}
}

// TestPrepareSandboxBlocksOnPendingRuntimeHomeMigration pins option D: when the
// offer reports the migration is still pending (a non-interactive session, a
// quit, or a failed copy), PrepareSandbox aborts before resolving the home
// volume, so no fresh home is created and the project's recorded reference is
// left intact for a later copy.
func TestPrepareSandboxBlocksOnPendingRuntimeHomeMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
		state.HomeState{AgentVersion: "1.5.0", AgentSource: agentSourceTool},
	)

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = func(_ context.Context, _ msb.Client, _ termio.UI) error {
		return runtimemigration.ErrMigrationPending
	}
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
		Name_:     projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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
			AgentSource:  agentSourceTool,
		},
	)

	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("runtime_home_migration: [unterminated"), 0o600); err != nil {
		t.Fatalf("write corrupt root state: %v", err)
	}

	origOffer := offerRuntimeHomeMigration
	offerRuntimeHomeMigration = runtimemigration.Offer
	t.Cleanup(func() { offerRuntimeHomeMigration = origOffer })

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	selectCalls := 0
	ui := termio.NewTestMock(t)
	ui.SelectFn = func(_ string, _ []termio.Choice, _ string) (string, error) {
		selectCalls++
		return "", nil
	}

	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

// TestRunStartupHooksDefaultsToDevUser verifies that a startup hook without an
// explicit user runs as the default sandbox user (dev), and that a script
// without a shebang falls back to /bin/sh. This is the path most users hit,
// unlike the root-run case covered by the integration flow.
func TestRunStartupHooksDefaultsToDevUser(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: false},
	}, &ui)

	if sb.AttachUser != DefaultSandboxUser {
		t.Errorf("startup hook AttachWith user = %q, want default %q", sb.AttachUser, DefaultSandboxUser)
	}
	if sb.AttachCmd != "/bin/sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want fallback %q", sb.AttachCmd, "/bin/sh")
	}
	want := []string{"/home/dev/.vpn/connect.sh"}
	if !slices.Equal(sb.AttachArgs, want) {
		t.Errorf("startup hook AttachWith args = %v, want %v", sb.AttachArgs, want)
	}
}

// TestRunStartupHooksExecutesTargetDirectly verifies that a hook with a shebang
// is executed as the script path itself, so the VM kernel applies the shebang
// (issue #136).
func TestRunStartupHooksExecutesTargetDirectly(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.foo.sh", Source: "x", Root: false, HasShebang: true},
	}, &ui)

	if sb.AttachCmd != "/home/dev/.foo.sh" {
		t.Errorf("startup hook AttachWith cmd = %q, want the script path", sb.AttachCmd)
	}
	if len(sb.AttachArgs) != 0 {
		t.Errorf("startup hook AttachWith args = %v, want none", sb.AttachArgs)
	}
}

// TestRunStartupHooksRunsAsRoot verifies that a hook with Root set attaches as
// the root user.
func TestRunStartupHooksRunsAsRoot(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: true},
	}, &ui)

	if sb.AttachUser != "root" {
		t.Errorf("startup hook AttachWith user = %q, want %q", sb.AttachUser, "root")
	}
}

// TestPrepareSandboxPersistsMountFingerprintOnVMCreation verifies that a
// freshly created project VM records the configured bind mounts in the state
// file. Without this fingerprint the next run would compare against an empty
// state, report a mount change and recreate the VM on every startup.
func TestPrepareSandboxPersistsMountFingerprintOnVMCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
					Labels: map[string]string{"org.agents-sandbox.agent": "opencode"},
				},
			}, nil
		},
	})

	slug := git.MustCheckoutSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{
		Name_:    projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
		FSValue_: vmFS,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}
	// No sandbox handle is registered, so GetSandbox reports "not found" and
	// PrepareSandbox takes the create path (boot == vmBootCreated).
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
		AgentSource:  agentSourceTool,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	mnts := mounts.Mounts{
		"/home/dev/.m2": {Source: filepath.Join(t.TempDir(), "m2")},
	}
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode", Mounts: mnts}, false, &ui)
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
	if len(st.MountState.Names) != 1 || st.MountState.Names[0] != "/home/dev/.m2" {
		t.Errorf("MountState.Names = %v, want [/home/dev/.m2]", st.MountState.Names)
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
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
		Name_:    projectVMName(state.Key{Slug: slug, Agent: "opencode"}),
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

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Agent: "opencode"}, false, &ui)
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

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, req string) (string, error) {
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
		Name_:     projectVMName(k),
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
	createdSb := &msb.MockSandbox{Name_: projectVMName(k), FSValue_: msb.NewTestFS(nil, nil), ShellOut: shellOut}
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	// After the VM is destroyed, GetSandbox must report it gone so the create path
	// runs; a static handle would leave the mock attaching to the old VM.
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		if existing.DidDestroy() {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: projectVMName(k)}
		}
		return existing, nil
	}
	msb.WithMsbMock(t, mock)

	opts := options.RunOptions{Agent: "opencode", TmpSize: "2G"}
	seedUI := termio.NewTestMock(t)
	fingerprints := desiredFingerprints(opts, envsecret.LoadEnvAndSecrets(&seedUI))
	if err := seedHomeState(k, state.HomeState{
		HomeVolume:   "home-vol",
		ImageDigest:  "sha256:abc123",
		AgentVersion: "1.5.0",
		AgentSource:  agentSourceTool,
		EnvState:     fingerprints.Env,
		SecretState:  fingerprints.Secret,
		NetworkState: fingerprints.Network,
		MountState:   fingerprints.Mount,
	}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	origDaemon := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(origDaemon) })

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), opts, false, &ui)
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
	if !contains(joinStrings(ui.VerboseCalls), "/tmp tmpfs size") {
		t.Errorf("expected a tmp-size rebuild reason, got %v", ui.VerboseCalls)
	}
	if !contains(joinStrings(ui.VerboseCalls), "recreate: true") {
		t.Errorf("expected the recreate decision to be logged, got %v", ui.VerboseCalls)
	}
}
