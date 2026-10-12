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

// stageInstallCached reports whether the build surfaced a cached install RUN for
// the given tool stage. BuildKit names cached vertices "<vertex name> CACHED",
// and the vertex name embeds the stage alias and the RUN op (for example
// "[agents-sandbox-node 4/5] RUN ..."), so a cached install RUN in a stage is a
// line carrying the stage bracket, the RUN op, and the CACHED marker together.
// Keying on the stage bracket keeps a cached RUN in another stage (such as the
// user body in the runner stage) from being attributed here, and a cached FROM
// in the same stage (which has no RUN) from counting.
func stageInstallCached(lines []string, stage string) bool {
	for _, line := range lines {
		if strings.Contains(line, "["+stage) &&
			strings.Contains(line, "RUN") &&
			strings.Contains(line, "CACHED") {
			return true
		}
	}
	return false
}

// buildBuilderDockerfile renders project for agentVersion and builds it through
// the real builder VM under tag, capturing streamed progress lines when lines is
// non-nil. The exported archive is removed on cleanup; the image is not loaded
// into the msb cache, so the builder's persistent layer cache is what these
// tests probe.
func buildBuilderDockerfile(
	t *testing.T, a agent.Agent, project []byte, agentVersion string, docker bool, tag string, lines *[]string,
) {
	t.Helper()
	dockerEnabled := effectiveDocker(project, docker)
	rendered := renderBytesVersion(t, a, agentVersion, project, docker)
	contextDir := t.TempDir()
	if err := writeDockerfileContext(contextDir, rendered); err != nil {
		t.Fatalf("write build context: %v", err)
	}
	progress := func(line string) {
		if lines != nil {
			*lines = append(*lines, line)
		}
	}
	result, err := builder.Get().Build(context.Background(), builder.BuildRequest{
		ContextDir: contextDir,
		Tag:        tag,
		BaseRef:    baseImageRef(rendered),
		BuildArgs: buildArgStrings(userBuildArgs(
			os.Getuid(), os.Getgid(), a.ImageSpec(), agentVersion,
			computeDockerfileID(rendered, agentVersion), dockerEnabled,
			computeImageIdentity(project, agentVersion, dockerEnabled),
		)),
		Platform: builder.HostPlatform(),
		Progress: progress,
	})
	if err != nil {
		t.Fatalf("builder build %s: %v", tag, err)
	}
	t.Cleanup(func() { _ = os.Remove(result.ArchivePath) })
}

// sanitizeTag renders a test name into a build-tag-safe lowercase string.
func sanitizeTag(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// TestRenderDockerfileBuilds builds a real image for every Dockerfile
// composition case in RenderDockerfile, covering the matrix of:
//
//	{no project Dockerfile, managed baseRef, custom base} x {docker, no docker}
//
// plus the docker marker injection cases. It gives refactoring safety for the
// per-tool multistage composition: each case renders a Dockerfile and asserts
// the whole thing actually builds through the real BuildKit builder. The
// custom-base cases use Fedora (dnf) so they exercise a non-Debian distro.
func TestRenderDockerfileBuilds(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	a := agentOpencode(t)
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}

	cases := []struct {
		name              string
		projectDockerfile []byte
		docker            bool
	}{
		{"no-dockerfile", nil, false},
		{"no-dockerfile-docker", nil, true},
		{"managed-base", []byte("FROM agents-sandbox/runner-base:latest\nRUN echo managed\n"), false},
		{
			"managed-base-docker-implied",
			[]byte("FROM agents-sandbox/runner-base-docker:latest\nRUN echo managed\n"),
			false,
		},
		{"managed-base-dind-implied", []byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo managed\n"), false},
		{"custom-base-fedora", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar && dnf clean all\n"), false},
		{"custom-base-fedora-docker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n"), true},
		{"custom-base-docker-marker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
				"# agents-sandbox:docker\n" +
				"RUN echo after-docker\n"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag := "agents-sandbox/it-" + sanitizeTag(tc.name)
			buildBuilderDockerfile(t, a, tc.projectDockerfile, version, tc.docker, tag, nil)
		})
	}
}

// TestRenderDockerfileToolLayersStayCached is the regression guard for the
// per-tool composition: mutating only the user body must not invalidate the
// node, agent, or docker install layers. It builds once, then rebuilds with a
// changed body and asserts the tool-stage install RUNs are cache hits. It uses
// pi, the one agent whose render includes all three tool stages (node + agent +
// docker).
func TestRenderDockerfileToolLayersStayCached(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}

	base := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v1\n")
	changed := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v2\n")

	tag := "agents-sandbox/it-cache"
	buildBuilderDockerfile(t, a, base, version, true, tag, nil)
	var lines []string
	buildBuilderDockerfile(t, a, changed, version, true, tag, &lines)

	for _, stage := range []string{nodeStageAlias, agentStageAlias, dockerStageAlias} {
		if !stageInstallCached(lines, stage) {
			t.Errorf("expected stage %q install RUN to be a cache hit after only the user body changed; output:\n%s",
				stage, strings.Join(lines, "\n"))
		}
	}
}

// TestRenderDockerfileAgentVersionRebuildsInstall is the regression guard for
// the stale-agent-layer bug: a changed agent version must re-run the agent
// install RUN so the new release is baked, while the docker install layer stays
// cached.
func TestRenderDockerfileAgentVersionRebuildsInstall(t *testing.T) {
	requireIntegration(t)
	useRealHostRuntime(t)
	a := agentOpencode(t)

	project := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n")

	tag := "agents-sandbox/it-agent-version"
	oldVersion, newVersion := "1.18.35", "1.19.0"
	buildBuilderDockerfile(t, a, project, oldVersion, true, tag, nil)
	var lines []string
	buildBuilderDockerfile(t, a, project, newVersion, true, tag, &lines)

	if stageInstallCached(lines, agentStageAlias) {
		t.Errorf("the agent install must re-run on a version bump; output:\n%s", strings.Join(lines, "\n"))
	}
	if !stageInstallCached(lines, dockerStageAlias) {
		t.Errorf("the docker install must stay cached on an agent version bump; output:\n%s",
			strings.Join(lines, "\n"))
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
