//go:build integration

package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// These tests exercise the real build pipeline end to end: a real microsandbox
// (msb) builder VM running buildkitd, real base-image and registry pulls, and
// the real msb image cache. They need a functional /dev/kvm, an installed msb
// runtime, and network access, so they cannot run inside the agents-sandbox dev
// VM (no KVM). They are excluded from the default suite by the integration build
// tag; run them on a KVM host:
//
//	AGENTS_SANDBOX_INTEGRATION=1 CGO_ENABLED=1 \
//	  go test -tags integration -count=1 -timeout 40m \
//	  -run 'TestBuildRunnerImage|TestBuilderCacheReuse|TestRunnerImage' \
//	  ./internal/sandbox/image/
const integrationEnvVar = "AGENTS_SANDBOX_INTEGRATION"

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationEnvVar) == "" {
		t.Skipf("integration test: set %s=1 on a KVM host with msb and network access", integrationEnvVar)
	}
}

// realMsbFactory captures the production msb factory at package initialization,
// before TestMain installs the fail-fast mock, so integration tests can opt back
// into the real runtime.
var realMsbFactory = msb.Get

// useRealHostRuntime restores the real msb client and isolates config-path
// resolution in a temporary directory for the duration of the test.
func useRealHostRuntime(t *testing.T) {
	t.Helper()
	original := msb.Get
	msb.Get = realMsbFactory
	configpaths.WithMockConfigPaths(t)
	t.Cleanup(func() { msb.Get = original })
}

// TestBuildRunnerImageInBuilderVM builds the managed runner Dockerfile for
// opencode in a real builder VM, loads the exported archive into the msb cache,
// and confirms the image is then inspectable.
func TestBuildRunnerImageInBuilderVM(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	ui := termio.NewTestMock(t)
	a := agentOpencode(t)

	info, err := EnsureImageWithClient(ctx, a, nil, "it-buildkit-full", BuildOptions{Force: true}, &ui)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	if info.ArchivePath == "" {
		t.Fatal("expected a non-empty ArchivePath from a forced build")
	}
	if info.Digest == "" {
		t.Error("expected a non-empty image digest")
	}
	archivePath := info.ArchivePath
	tag := info.Tag
	t.Cleanup(func() {
		_ = os.Remove(archivePath)
		_ = msb.Get().ImageRemove(context.Background(), tag, true)
	})

	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("expected the build to produce an archive at %s: %v", archivePath, err)
	}
	if err := EnsureLoaded(ctx, msb.Get(), archivePath, tag, &ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}

	inspect, err := msb.Get().ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("ImageInspect(%s) after load: %v", tag, err)
	}
	if inspect == nil || inspect.Digest == "" {
		t.Fatalf("ImageInspect(%s) returned no digest: %+v", tag, inspect)
	}
}

// TestBuilderCacheReuseAcrossVMs builds the same runner image twice, stopping
// and removing the builder sandbox between runs so a fresh buildkitd starts,
// and asserts the warm build reuses the persistent BuildKit cache volume. Cache
// reuse is a CACHED marker the builder surfaces for cached vertices (cached RUN
// steps emit no logs).
func TestBuilderCacheReuseAcrossVMs(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	a := agentOpencode(t)

	version, err := resolveAgentVersion(ctx, a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}
	rendered, err := RenderDockerfile(a, version, nil, false)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	contextDir := t.TempDir()
	if err := writeDockerfileContext(contextDir, rendered); err != nil {
		t.Fatalf("write build context: %v", err)
	}
	request := builder.BuildRequest{
		ContextDir: contextDir,
		Tag:        "agents-sandbox/integration-cache:opencode-latest",
		BaseRef:    baseImageRef(rendered),
		BuildArgs: buildArgStrings(userBuildArgs(
			os.Getuid(), os.Getgid(), a.ImageSpec(), version,
			computeDockerfileID(rendered, version), false, computeImageIdentity(nil, version, false),
		)),
		Platform: builder.HostPlatform(),
	}

	coldDuration, coldLines := timeBuilderBuild(t, request, true)
	removeBuilderSandboxes(t, ctx)
	warmDuration, warmLines := timeBuilderBuild(t, request, false)

	t.Logf("cold build: %s, %d progress lines", coldDuration, len(coldLines))
	t.Logf("warm build: %s, %d progress lines", warmDuration, len(warmLines))

	if !containsCachedLine(warmLines) {
		t.Fatalf(
			"warm build reported no CACHED layers after the builder VM was recreated; "+
				"either the BuildKit cache volume did not persist or the builder did not surface cache hits "+
				"(cold=%s/%d lines, warm=%s/%d lines)",
			coldDuration, len(coldLines), warmDuration, len(warmLines),
		)
	}
}

// containsCachedLine reports whether the builder surfaced a BuildKit cache-hit
// marker in its progress output.
func containsCachedLine(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, "CACHED") {
			return true
		}
	}
	return false
}

// timeBuilderBuild runs one build through the real builder, recording its
// wall-clock duration and streamed progress lines. The archive is removed on
// cleanup.
func timeBuilderBuild(t *testing.T, request builder.BuildRequest, noCache bool) (time.Duration, []string) {
	t.Helper()
	var lines []string
	request.NoCache = noCache
	request.Progress = func(line string) { lines = append(lines, line) }

	start := time.Now()
	result, err := builder.Get().Build(context.Background(), request)
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("builder build (noCache=%v): %v", noCache, err)
	}
	if result.ArchivePath == "" || result.Digest == "" {
		t.Fatalf("builder build (noCache=%v) returned an empty archive/digest: %+v", noCache, result)
	}
	archivePath := result.ArchivePath
	t.Cleanup(func() { _ = os.Remove(archivePath) })
	return duration, lines
}

// removeBuilderSandboxes stops and removes the shared builder VM so the next
// build boots a fresh buildkitd; the named cache volume it mounted persists. A
// graceful stop lets buildkitd flush its Bolt cache to the volume before the
// sandbox is torn down.
func removeBuilderSandboxes(t *testing.T, ctx context.Context) {
	t.Helper()
	handles, err := msb.Get().ListSandboxes(ctx, map[string]string{naming.LabelComponent: "buildkit"})
	if err != nil {
		t.Fatalf("list builder sandboxes: %v", err)
	}
	for _, handle := range handles {
		if err := handle.Stop(ctx, msbSdk.WithStopTimeout(30*time.Second)); err != nil {
			t.Fatalf("stop builder sandbox %s: %v", handle.Name(), err)
		}
		if err := handle.Remove(ctx); err != nil {
			t.Fatalf("remove builder sandbox %s: %v", handle.Name(), err)
		}
	}
}

// TestRunnerImageNodeAgentNodeRuns is the BuildKit regression guard for #132: a
// node-dependent agent installed onto the managed base must leave a working
// node/npm. The managed base does not install libatomic1, so a node tarball with
// a hard DT_NEEDED on libatomic.so.1 builds (for a native agent like opencode)
// but cannot start, and the npm agent's install RUN then fails the whole build.
// The pinned node must therefore be a release without that dependency; here we
// build pi (the only agent that needs node, an npm-installed node bundle) on the
// managed base and run node/npm in a sandbox created from the resulting image.
//
// TestBuildRunnerImageInBuilderVM only builds opencode, a native Bun binary that
// does not exercise node at all, which is why it did not catch #132.
func TestRunnerImageNodeAgentNodeRuns(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	ui := termio.NewTestMock(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}

	info, err := EnsureImageWithClient(ctx, a, nil, "it-buildkit-node-agent", BuildOptions{Force: true}, &ui)
	if err != nil {
		t.Fatalf("EnsureImageWithClient: %v", err)
	}
	archivePath, tag := info.ArchivePath, info.Tag
	t.Cleanup(func() {
		_ = os.Remove(archivePath)
		_ = msb.Get().ImageRemove(context.Background(), tag, true)
	})
	if err := EnsureLoaded(ctx, msb.Get(), archivePath, tag, &ui); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}

	sandboxName := "agents-sandbox-it-node-agent"
	sb, err := msb.Get().CreateSandbox(ctx, sandboxName, msbSdk.WithImage(tag), msbSdk.WithReplace())
	if err != nil {
		t.Fatalf("create node-agent sandbox: %v", err)
	}
	t.Cleanup(func() {
		_ = sb.Stop(context.Background())
		_ = sb.Close()
		_ = msb.Get().RemoveSandbox(context.Background(), sandboxName)
	})

	for _, command := range []string{"node --version", "npm --version"} {
		result, shellErr := sb.Shell(ctx, "bash -lc '"+command+"'")
		if shellErr != nil {
			t.Errorf("%q: shell error: %v", command, shellErr)
			continue
		}
		if !result.Success() {
			t.Errorf("%q failed in the built image (exit %d): %s", command, result.ExitCode(), result.Stderr())
		}
	}
}

// TestRunnerImageLoginShellPath builds a runner image whose project Dockerfile
// extends PATH, then runs a login shell in a sandbox created from that image and
// asserts the composed PATH (the project ENV entry and /opt/agents-sandbox/bin
// re-merged by the profile.d script) survives the login-shell PATH reset.
func TestRunnerImageLoginShellPath(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	ui := termio.NewTestMock(t)
	a := agentOpencode(t)

	const projectSlug = "it-buildkit-login"
	project := []byte("FROM agents-sandbox/runner-base:latest\nENV PATH=\"/opt/my-tools:${PATH}\"\n")
	projectFile := configpaths.Get().ProjectDockerfile()
	if err := os.MkdirAll(filepath.Dir(projectFile), 0o750); err != nil {
		t.Fatalf("create project config dir: %v", err)
	}
	if err := os.WriteFile(projectFile, project, 0o644); err != nil {
		t.Fatalf("write project Dockerfile: %v", err)
	}

	tag := runnerTag(projectSlug, a.Name())
	if err := Build(ctx, a, projectSlug, BuildOptions{Force: true}, &ui); err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = msb.Get().ImageRemove(context.Background(), tag, true) })

	sandboxName := "agents-sandbox-it-login-path"
	sb, err := msb.Get().CreateSandbox(ctx, sandboxName, msbSdk.WithImage(tag), msbSdk.WithReplace())
	if err != nil {
		t.Fatalf("create login-shell sandbox: %v", err)
	}
	t.Cleanup(func() {
		_ = sb.Stop(context.Background())
		_ = sb.Close()
		_ = msb.Get().RemoveSandbox(context.Background(), sandboxName)
	})

	result, err := sb.Shell(ctx, `bash -lc 'printf %s "$PATH"'`)
	if err != nil {
		t.Fatalf("login shell: %v", err)
	}
	if !result.Success() {
		t.Fatalf("login shell failed (exit %d): %s", result.ExitCode(), result.Stderr())
	}
	loginPath := result.Stdout()
	for _, want := range []string{"/opt/agents-sandbox/bin", "/opt/my-tools"} {
		if !strings.Contains(loginPath, want) {
			t.Errorf("login PATH %q missing %q", loginPath, want)
		}
	}
}
