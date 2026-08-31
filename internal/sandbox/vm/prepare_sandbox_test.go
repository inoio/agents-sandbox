package vm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	mobyimage "github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/homeconfig"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	sandboximage "github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/mounts"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// TestPrepareSandboxReusesStoredOpenCodeVersion verifies that a normal run
// falls back to the opencode version recorded in updater.yaml instead of
// re-resolving "latest" from the network. Passing the stored version keeps the
// image identity (and thus the microsandbox load decision) stable across runs.
func TestPrepareSandboxReusesStoredOpenCodeVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.5.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	// The resolver records the requested version and returns it, so the test
	// observes exactly what PrepareSandbox passed instead of a network lookup.
	var requested string
	sandboximage.WithMockOpenCodeVersionResolver(t, func(_ context.Context, req string) (string, error) {
		requested = req
		return req, nil
	})
	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Env:    []string{"PATH=/usr/bin"},
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.5.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.5.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	state.WriteState(slug, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"})

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui)
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
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })

	// The upgrade prompt accepts the rebuild, so shallUpgrade is true.
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	var buildNoCache bool
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			buildNoCache = opts.NoCache
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Env:    []string{"PATH=/usr/bin"},
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "2.0.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "2.0.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	state.WriteState(slug, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"})

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, ui)
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
	if !buildNoCache {
		t.Error("expected the image build to bypass cache when upgrading")
	}
}

// TestPrepareSandboxLoadsHomeYamlOnce verifies that a full startup loads the
// home.yaml manifests exactly once: a missing host source is warned about a
// single time. This guards against the regression where config files were
// loaded twice (once in decideReconfig and once in setUpSandbox), which made
// the warning (and the underlying BuildHomeFiles computation) run twice.
func TestPrepareSandboxLoadsHomeYamlOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home.yaml whose host source does not exist triggers the
	// "does not exist on the host; skipping" warning. The source is a relative
	// path, resolved against the (empty) project config dir, so it cannot exist.
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml", "missing-file.txt: missing-file.txt\n")

	// Pin the resolved opencode version so image resolution needs no network.
	sandboximage.WithMockOpenCodeVersion(t, "1.0.0")

	// The image is considered up to date: the same version is "latest", so
	// resolveBuildVersion does not offer a rebuild.
	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })

	// Docker build + inspect are mocked so EnsureImage can "build" the runner
	// image and produce a deterministic digest.
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Env:    []string{"PATH=/usr/bin"},
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()

	// The msb image cache already holds the digest image (ImageGet succeeds),
	// so EnsureImage short-circuits before exporting/loading anything. The
	// image carries the pinned opencode version label.
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	// Seed home state so ResolveHomeVolume reuses the existing volume and
	// digest matches (no image-change home-volume prompt).
	state.WriteState(slug, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"})

	// The opencode daemon is healthy inside the VM, so ensureDaemon returns
	// immediately instead of starting and polling.
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui)
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
		t.Errorf("expected exactly 1 'missing home.yaml source' warning, got %d (warnings: %v)",
			missingWarnings, ui.WarnCalls)
	}
}

// TestPrepareSandboxRunsStartupHook verifies that a home.yaml entry marked
// hook: startup is executed through an interactive AttachWith as the configured
// user (root) during startup, before the opencode daemon is ensured.
func TestPrepareSandboxRunsStartupHook(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()

	// A project home.yaml that both provisions a script and marks it a
	// root-run startup hook.
	testutil.WritePath(t, filepath.Join(cp.ProjectConfigDir(), "connect.sh"), "#!/bin/sh\nnohup echo vpn &\n")
	testutil.WriteFile(t, cp.ProjectConfigDir(), "home.yaml",
		".vpn/connect.sh:\n  source: connect.sh\n  hook: startup\n  root: true\n")

	sandboximage.WithMockOpenCodeVersion(t, "1.0.0")
	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "1.0.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Env:    []string{"PATH=/usr/bin"},
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusStopped,
		ConnectSb: connectSb,
		StartSb:   connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	state.WriteState(slug, state.HomeState{HomeVolume: "home-vol", ImageDigest: "sha256:abc123"})

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui)
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

// TestRunStartupHooksDefaultsToDevUser verifies that a startup hook without an
// explicit user runs as the default sandbox user (dev), and that a missing
// interpreter falls back to /bin/sh. This is the path most users hit, unlike
// the root-run case covered by the integration flow.
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
}

// TestRunStartupHooksUsesShebangInterpreter verifies that a hook with a
// detected interpreter is run via that interpreter rather than a hardcoded
// shell.
func TestRunStartupHooksUsesShebangInterpreter(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "vm"}
	ui := termio.NewTestMock(t)

	runStartupHooks(context.Background(), sb, []homeconfig.HookSpec{
		{Target: "/home/dev/.vpn/connect.sh", Source: "x", Root: false, Interpreter: "/bin/bash"},
	}, &ui)

	if sb.AttachCmd != "/bin/bash" {
		t.Errorf("startup hook AttachWith cmd = %q, want %q", sb.AttachCmd, "/bin/bash")
	}
	if len(sb.AttachArgs) != 1 || sb.AttachArgs[0] != "/home/dev/.vpn/connect.sh" {
		t.Errorf("startup hook AttachWith args = %v, want the script path", sb.AttachArgs)
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

// TestPrepareSandboxWarnsWhenRecordingVersionFails covers the branch where
// persisting the baked opencode version fails: PrepareSandbox warns and
// continues rather than failing the run.
func TestPrepareSandboxWarnsWhenRecordingVersionFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Make the updater state file unreadable (a directory in its place) so
	// recordUpgradeVersion's load fails with a non-not-found error, while the
	// rest of the state directory remains usable.
	stateDir := configpaths.Get().UserStateDir()
	updaterPath := filepath.Join(stateDir, "updater.yaml")
	if err := os.MkdirAll(updaterPath, 0o700); err != nil {
		t.Fatal(err)
	}

	sandboximage.WithMockOpenCodeVersion(t, "1.0.0")

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Env:    []string{"PATH=/usr/bin"},
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	connectSb := &msb.MockSandbox{Name_: "vm", FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	sh := &msb.MockSandboxHandle{
		Name_:     projectVMName(slug),
		Status_:   msbSdk.SandboxStatusRunning,
		ConnectSb: connectSb,
	}
	mock := &msb.MockMsbClient{
		ImageGetFn: func(_ context.Context, _ string) error { return nil },
		ImageInspectFn: func(_ context.Context, _ string) (*msbSdk.ImageConfig, error) {
			return &msbSdk.ImageConfig{
				Env:    []string{"PATH=/usr/bin"},
				Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.0.0"},
			}, nil
		},
		Volumes: []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	mock.SetGotSandbox(sh)
	msb.WithMsbMock(t, mock)

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	if sess == nil {
		t.Fatal("expected a non-nil session")
	}
	defer sess.Cleanup()

	if !contains(joinStrings(ui.WarnCalls), "could not record opencode version in updater state") {
		t.Errorf("expected a version-recording warning, got %v", ui.WarnCalls)
	}
}

// TestPrepareSandboxPersistsMountFingerprintOnVMCreation verifies that a
// freshly created project VM records the configured bind mounts in the state
// file. Without this fingerprint the next run would compare against an empty
// state, report a mount change and recreate the VM on every startup.
func TestPrepareSandboxPersistsMountFingerprintOnVMCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockOpenCodeVersionResolver(t, func(_ context.Context, req string) (string, error) {
		return req, nil
	})
	origUpgradeInfo := openCodeUpgradeInfo
	openCodeUpgradeInfo = func(_ context.Context) (string, error) { return "1.5.0", nil }
	t.Cleanup(func() { openCodeUpgradeInfo = origUpgradeInfo })
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.5.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, _ client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
		ImageInspectFn: func(_ context.Context, _ string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{
				InspectResponse: mobyimage.InspectResponse{
					ID: "sha256:abc123",
					Config: &dockerspec.DockerOCIImageConfig{
						ImageConfig: ocispec.ImageConfig{
							Labels: map[string]string{sandboximage.OpenCodeVersionLabel: "1.5.0"},
						},
					},
				},
			}, nil
		},
	})

	slug := git.ProjectSlug()
	vmFS := msb.NewTestFS(nil, nil)
	createdSb := &msb.MockSandbox{Name_: projectVMName(slug), FSValue_: vmFS, ShellOut: map[string]msb.ShellResult{
		dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
	}}
	// No sandbox handle is registered, so GetSandbox reports "not found" and
	// PrepareSandbox takes the create path (boot == vmBootCreated).
	mock := &msb.MockMsbClient{
		ImageGetFn:     func(_ context.Context, _ string) error { return nil },
		CreatedSandbox: createdSb,
		Volumes:        []msb.VolumeHandle{&msb.MockVolumeHandle{Name_: "home-vol"}},
	}
	msb.WithMsbMock(t, mock)

	if err := state.WriteState(slug, state.HomeState{
		HomeVolume:  "home-vol",
		ImageDigest: "sha256:abc123",
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	defer SetDaemonShellFunc(origDaemon)

	mnts := mounts.Mounts{
		"/home/dev/.m2": {Source: filepath.Join(t.TempDir(), "m2")},
	}
	ui := termio.NewTestMock(t)
	sess, err := PrepareSandbox(context.Background(), options.RunOptions{Mounts: mnts}, &ui)
	if err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	defer sess.Cleanup()

	if len(mock.CreatedSandboxes) != 1 {
		t.Fatalf("expected the project VM to be created, got %d creations", len(mock.CreatedSandboxes))
	}

	st, err := state.ReadState(slug)
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
