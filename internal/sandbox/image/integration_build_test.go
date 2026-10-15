//go:build integration

package image

import (
	"context"
	"os"
	"strings"
	"sync"
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

// These tests exercise the real build pipeline end to end: real base-image and
// registry pulls, real image builds, and the real msb image cache. The msb tier
// runs buildkitd inside a microsandbox VM (needs a functional /dev/kvm and the
// msb runtime plus network access); the moby tier builds through an existing
// Docker daemon's Engine API (needs only Docker). Both are excluded from the
// default suite by the integration build tag; run the tiers via
// `make test-integration` (moby, any Docker host) and `make test-integration-msb`
// (msb, a KVM host with msb and network access).
const integrationEnvVar = "AGENTS_SANDBOX_INTEGRATION"

// integrationOpencodeVersionEnv overrides the opencode release the integration
// builds bake. The version is pinned by default because resolving "latest"
// queries the GitHub releases API, which is rate-limited (unauthenticated, 60
// requests/hour per IP) and flakes the tier; unit tests cover the resolver.
const integrationOpencodeVersionEnv = "AGENTS_SANDBOX_INTEGRATION_OPENCODE_VERSION"

// defaultIntegrationOpencodeVersion is a known-good opencode release baked by the
// integration tier. TestRenderDockerfileAgentVersionRebuildsInstall must NOT use
// it (see that test): the run-wide shared worker cache would make it warm.
const defaultIntegrationOpencodeVersion = "1.18.34"

func integrationOpencodeVersion() string {
	if version := os.Getenv(integrationOpencodeVersionEnv); version != "" {
		return version
	}
	return defaultIntegrationOpencodeVersion
}

// integrationAgentVersion returns the release to bake for a. It pins opencode to
// avoid the rate-limited GitHub releases API; other agents (pi, ...) resolve
// through their own release source.
func integrationAgentVersion(t *testing.T, a agent.Agent) string {
	t.Helper()
	if a.Name() == "opencode" {
		return integrationOpencodeVersion()
	}
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve %s version: %v", a.Name(), err)
	}
	return version
}

// pinOpencodeVersion forces the image package's production version resolver to
// the pinned opencode release for the duration of the test, so a code path that
// resolves internally (EnsureImageWithClient) never reaches GitHub.
func pinOpencodeVersion(t *testing.T) {
	t.Helper()
	WithMockAgentVersion(t, integrationOpencodeVersion())
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationEnvVar) == "" {
		t.Skipf("integration test: set %s=1 on a KVM host with msb and network access", integrationEnvVar)
	}
}

// requireMsbBackend skips a test that depends on microsandbox-only behavior
// (the msb image cache, the shared builder sandbox, or the registry-backed
// builder cache) when the moby backend is selected.
func requireMsbBackend(t *testing.T) {
	t.Helper()
	sharedConfigPaths(t)
	if builder.SelectedName() != "msb" {
		t.Skip("msb-only test: requires the microsandbox backend")
	}
}

// requireMobyBackend skips a test that depends on the Docker Engine API backend
// when the msb backend is selected.
func requireMobyBackend(t *testing.T) {
	t.Helper()
	sharedConfigPaths(t)
	if builder.SelectedName() != "moby" {
		t.Skip("moby-only test: requires the Docker Engine API backend")
	}
}

// realMsbFactory captures the production msb factory at package initialization,
// before TestMain installs the fail-fast mock, so integration tests can opt back
// into the real runtime.
var realMsbFactory = msb.Get

// sharedIntegrationConfigDir is one state directory shared by every integration
// test in this binary, so the embedded registry's layer cache and the
// builder-VM record persist across tests. A per-test state dir would force every
// test to rebuild from scratch (only a test's own two builds could hit cache),
// making the tier far slower. The directory is removed after the binary exits
// via integrationTestCleanup.
var (
	sharedIntegrationConfigDirOnce sync.Once
	sharedIntegrationConfigDir     string
	sharedBuilderLabelOnce         sync.Once
	sharedBuilderLabel             string
)

func init() {
	integrationTestCleanup = func() {
		removeSharedBuilderSandbox()
		if sharedIntegrationConfigDir != "" {
			_ = os.RemoveAll(sharedIntegrationConfigDir)
		}
	}
}

// removeSharedBuilderSandbox removes the run-wide builder VM before its state
// directory is deleted. The builder VM is namespaced by the state directory, so
// each test run would otherwise leak a stopped builder VM — and its 128 GiB owned
// worker volume — forever: the idle reaper only stops an idle builder, it never
// removes it. In production the name is stable per state dir, so at most one
// builder exists; only the per-run temp state dirs of the integration tier
// accumulate. Uses the real msb factory (captured before the fail-fast mock is
// installed) and the label cached while the shared config paths were active.
func removeSharedBuilderSandbox() {
	if sharedBuilderLabel == "" {
		return
	}
	ctx := context.Background()
	handles, err := realMsbFactory().
		ListSandboxes(ctx, map[string]string{naming.LabelComponent: sharedBuilderLabel})
	if err != nil {
		return
	}
	for _, handle := range handles {
		_ = handle.Stop(ctx, msbSdk.WithStopTimeout(30*time.Second))
		_ = handle.Remove(ctx)
	}
}

// sharedConfigPaths installs config-path mocks rooted at the run-wide shared
// directory, so every integration test reads and writes the same registry cache
// and builder record.
func sharedConfigPaths(t *testing.T) {
	t.Helper()
	configpaths.WithMockConfigPathsAt(t, sharedIntegrationConfigPath())
	sharedBuilderLabelOnce.Do(func() { sharedBuilderLabel = builder.BuilderComponentLabelValue() })
}

func sharedIntegrationConfigPath() string {
	sharedIntegrationConfigDirOnce.Do(func() {
		dir, err := os.MkdirTemp("", "agents-sandbox-integration-config-")
		if err != nil {
			panic(err)
		}
		sharedIntegrationConfigDir = dir
	})
	return sharedIntegrationConfigDir
}

// useRealHostRuntime restores the real msb client and points config-path
// resolution at the run-wide shared directory for the duration of the test.
func useRealHostRuntime(t *testing.T) {
	t.Helper()
	original := msb.Get
	msb.Get = realMsbFactory
	sharedConfigPaths(t)
	t.Cleanup(func() { msb.Get = original })
}

// integrationUI returns a termio.UI that writes build progress and stage
// markers to the test log. The tests run against a real builder VM, where a
// build can take minutes with no output; forwarding to t.Log makes progress
// visible under `go test -v` instead of a silent stall.
func integrationUI(t *testing.T) termio.UI {
	t.Helper()
	mock := termio.NewTestMock(t)
	return &loggingUI{Mock: &mock, t: t}
}

// loggingUI embeds the recording mock, so tests can still inspect recorded
// calls, and additionally mirrors the verbose stream and spinner markers to the
// test log.
type loggingUI struct {
	*termio.Mock
	t *testing.T
}

func (u *loggingUI) Verbose(msg string) {
	u.Mock.Verbose(msg)
	u.t.Log(msg)
}

func (u *loggingUI) Verbosef(format string, args ...any) {
	u.Mock.Verbosef(format, args...)
	u.t.Logf(format, args...)
}

func (u *loggingUI) Spinner(msg string) termio.Spinner {
	u.t.Log(msg)
	return u.Mock.Spinner(msg)
}

func (u *loggingUI) Spinnerf(format string, args ...any) termio.Spinner {
	u.t.Logf(format, args...)
	return u.Mock.Spinnerf(format, args...)
}

// logProgress returns a builder progress callback that logs each line, so a
// long build shows its progress live under `go test -v`.
func logProgress(t *testing.T) func(string) {
	t.Helper()
	return func(line string) { t.Log(line) }
}

// TestBuildRunnerImageInBuilderVM builds the managed runner Dockerfile for
// opencode in a real builder VM, loads the exported archive into the msb cache,
// and confirms the image is then inspectable.
func TestBuildRunnerImageInBuilderVM(t *testing.T) {
	requireIntegration(t)
	requireMsbBackend(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	ui := integrationUI(t)
	a := agentOpencode(t)
	pinOpencodeVersion(t)

	info, err := EnsureImageWithClient(ctx, a, nil, "it-buildkit-full", BuildOptions{Force: true}, ui)
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
	stop, err := EnsureAvailable(ctx, msb.Get(), ImageInfo{Tag: tag, ArchivePath: archivePath}, ui)
	if err != nil {
		t.Fatalf("EnsureAvailable: %v", err)
	}
	stop()

	inspect, err := msb.Get().ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("ImageInspect(%s) after load: %v", tag, err)
	}
	if inspect == nil || inspect.Digest == "" {
		t.Fatalf("ImageInspect(%s) returned no digest: %+v", tag, inspect)
	}
}

// TestMsbSecondBuildHitsRegistryCache builds the same runner image twice,
// stopping and removing the builder sandbox between runs so a fresh buildkitd
// starts, and asserts the warm build reuses the embedded registry's persistent
// cache. Cache reuse is a CACHED marker the builder surfaces for cached vertices
// (cached RUN steps emit no logs).
func TestMsbSecondBuildHitsRegistryCache(t *testing.T) {
	requireIntegration(t)
	requireMsbBackend(t)
	useRealHostRuntime(t)
	ctx := context.Background()

	request := integrationBuildRequest(t, agentOpencode(t), "agents-sandbox/integration-cache:opencode-latest")

	coldDuration, coldLines := timeBuilderBuild(t, request, true)
	removeBuilderSandboxes(t, ctx)
	warmDuration, warmLines := timeBuilderBuild(t, request, false)

	t.Logf("cold build: %s, %d progress lines", coldDuration, len(coldLines))
	t.Logf("warm build: %s, %d progress lines", warmDuration, len(warmLines))

	if !containsCachedLine(warmLines) {
		t.Fatalf(
			"warm build reported no CACHED layers after the builder VM was recreated; "+
				"either the embedded registry cache did not persist or the builder did not surface cache hits "+
				"(cold=%s/%d lines, warm=%s/%d lines)",
			coldDuration, len(coldLines), warmDuration, len(warmLines),
		)
	}
}

// TestMsbParallelBuildsShareOneBuilderVM starts two concurrent builds after
// clearing any existing builder sandbox and asserts both succeed while a single
// shared builder sandbox serves them (the host-wide flock serializes creation
// and the persisted port record lets the second build reuse it).
func TestMsbParallelBuildsShareOneBuilderVM(t *testing.T) {
	requireIntegration(t)
	requireMsbBackend(t)
	useRealHostRuntime(t)
	ctx := context.Background()
	removeBuilderSandboxes(t, ctx)

	a := agentOpencode(t)
	requests := []builder.BuildRequest{
		integrationBuildRequest(t, a, "agents-sandbox/it-parallel-a"),
		integrationBuildRequest(t, a, "agents-sandbox/it-parallel-b"),
	}

	results := make([]builder.BuildResult, len(requests))
	errs := make([]error, len(requests))
	var wg sync.WaitGroup
	for i, request := range requests {
		wg.Add(1)
		go func(i int, request builder.BuildRequest) {
			defer wg.Done()
			results[i], errs[i] = builder.Get().Build(ctx, request)
		}(i, request)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent build %d: %v", i, err)
		}
		if archivePath := results[i].ArchivePath; archivePath != "" {
			_ = os.Remove(archivePath)
		}
	}
	if t.Failed() {
		dumpBuilderFailure(t, ctx)
	}

	handles, err := msb.Get().
		ListSandboxes(ctx, map[string]string{naming.LabelComponent: builder.BuilderComponentLabelValue()})
	if err != nil {
		t.Fatalf("list builder sandboxes: %v", err)
	}
	if len(handles) != 1 {
		t.Fatalf("expected one shared builder sandbox for concurrent builds, got %d", len(handles))
	}
}

// TestMobyBuildLoadsIntoMicrosandbox builds the runner image through the real
// Docker Engine API and loads the resulting archive into the microsandbox image
// cache, confirming the moby delivery path produces an inspectable image.
func TestMobyBuildLoadsIntoMicrosandbox(t *testing.T) {
	requireIntegration(t)
	requireMobyBackend(t)
	useRealHostRuntime(t)

	a := agentOpencode(t)
	ref := buildImageForRun(t, a, nil, false, "agents-sandbox/it-moby-"+sanitizeTag(t.Name()))

	inspect, err := msb.Get().ImageInspect(context.Background(), ref)
	if err != nil {
		t.Fatalf("ImageInspect(%s) after moby build and load: %v", ref, err)
	}
	if inspect == nil || inspect.Digest == "" {
		t.Fatalf("ImageInspect(%s) returned no digest: %+v", ref, inspect)
	}
}

// integrationBuildRequest renders the runner Dockerfile for a and assembles the
// builder request for tag, resolving the current agent version.
func integrationBuildRequest(t *testing.T, a agent.Agent, tag string) builder.BuildRequest {
	t.Helper()
	version := integrationAgentVersion(t, a)
	rendered, err := RenderDockerfile(a, version, nil, false)
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	contextDir := t.TempDir()
	if err := writeDockerfileContext(contextDir, rendered); err != nil {
		t.Fatalf("write build context: %v", err)
	}
	return builder.BuildRequest{
		ContextDir: contextDir,
		Tag:        tag,
		BaseRef:    baseImageRef(rendered),
		BuildArgs: buildArgStrings(userBuildArgs(
			os.Getuid(), os.Getgid(), a.ImageSpec(), version,
			computeDockerfileID(rendered, version), false, computeImageIdentity(nil, version, false),
		)),
		Platform: builder.HostPlatform(),
		Progress: logProgress(t),
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
	request.Progress = func(line string) {
		lines = append(lines, line)
		t.Log(line)
	}

	start := time.Now()
	result, err := builder.Get().Build(context.Background(), request)
	duration := time.Since(start)
	if err != nil {
		dumpBuilderFailure(t, context.Background())
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
	handles, err := msb.Get().
		ListSandboxes(ctx, map[string]string{naming.LabelComponent: builder.BuilderComponentLabelValue()})
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

// buildkitdLogPath mirrors the builder's unexported builderLogPath: buildkitd
// appends its output there on the builder VM's worker storage.
const buildkitdLogPath = "/var/lib/buildkit/buildkitd.log"

// dumpBuilderFailure logs the builder sandbox state and the buildkitd log tail
// after a failed build. A build solve that fails with a dropped BuildKit stream
// ("error reading from server: EOF") means buildkitd died or the sandbox was
// reaped; this is the only trace the launcher keeps, so surface it in the test.
func dumpBuilderFailure(t *testing.T, ctx context.Context) {
	t.Helper()
	handles, err := msb.Get().
		ListSandboxes(ctx, map[string]string{naming.LabelComponent: builder.BuilderComponentLabelValue()})
	if err != nil {
		t.Logf("builder diagnostics: list sandboxes: %v", err)
		return
	}
	if len(handles) == 0 {
		t.Log("builder diagnostics: no builder sandbox present (reaped or never created)")
		return
	}
	for _, handle := range handles {
		t.Logf("builder diagnostics: sandbox %s status=%v", handle.Name(), handle.Status())
		sandbox, connectErr := handle.Connect(ctx)
		if connectErr != nil {
			t.Logf("builder diagnostics: connect %s: %v", handle.Name(), connectErr)
			continue
		}
		result, shellErr := sandbox.Shell(ctx, "tail -n 80 "+buildkitdLogPath)
		_ = sandbox.Close()
		if shellErr != nil {
			t.Logf("builder diagnostics: read buildkitd log: %v", shellErr)
			continue
		}
		t.Logf("builder diagnostics: buildkitd log:\n%s", result.Stdout())
	}
}

// shellResult is the backend-agnostic outcome of running a command in a built
// image, mirroring the fields the microsandbox shell result exposes.
type shellResult struct {
	success  bool
	exitCode int
	stdout   string
	stderr   string
}

// buildImageForRun renders and builds the runner image for a, then loads the
// exported archive into the microsandbox image cache so the image is runnable.
// It returns the image reference to run and registers cleanup for the archive
// and the loaded image.
func buildImageForRun(t *testing.T, a agent.Agent, project []byte, docker bool, tag string) string {
	t.Helper()
	ctx := context.Background()
	version := integrationAgentVersion(t, a)
	dockerEnabled := effectiveDocker(project, docker)
	rendered := renderBytesVersion(t, a, version, project, docker)
	contextDir := t.TempDir()
	if err := writeDockerfileContext(contextDir, rendered); err != nil {
		t.Fatalf("write build context: %v", err)
	}

	result, err := builder.Get().Build(ctx, builder.BuildRequest{
		ContextDir: contextDir,
		Tag:        tag,
		BaseRef:    baseImageRef(rendered),
		BuildArgs: buildArgStrings(userBuildArgs(
			os.Getuid(), os.Getgid(), a.ImageSpec(), version,
			computeDockerfileID(rendered, version), dockerEnabled,
			computeImageIdentity(project, version, dockerEnabled),
		)),
		Platform: builder.HostPlatform(),
		Progress: logProgress(t),
	})
	if err != nil {
		dumpBuilderFailure(t, ctx)
		t.Fatalf("builder build %s: %v", tag, err)
	}
	archivePath := result.ArchivePath
	t.Cleanup(func() { _ = os.Remove(archivePath) })

	ui := integrationUI(t)
	stop, err := EnsureAvailable(ctx, msb.Get(), ImageInfo{Tag: tag, ArchivePath: archivePath}, ui)
	if err != nil {
		t.Fatalf("EnsureAvailable: %v", err)
	}
	stop()
	t.Cleanup(func() { _ = msb.Get().ImageRemove(context.Background(), tag, true) })
	return tag
}

// runInImage runs command in the image referenced by ref by creating a
// microsandbox from the image.
func runInImage(t *testing.T, ref, command string) shellResult {
	t.Helper()
	ctx := context.Background()
	sandboxName := "agents-sandbox-it-run-" + sanitizeTag(ref)
	sb, err := msb.Get().CreateSandbox(ctx, sandboxName, msbSdk.WithImage(ref), msbSdk.WithReplace())
	if err != nil {
		t.Fatalf("create sandbox for %s: %v", ref, err)
	}
	t.Cleanup(func() {
		_ = sb.Stop(context.Background())
		_ = sb.Close()
		_ = msb.Get().RemoveSandbox(context.Background(), sandboxName)
	})

	result, err := sb.Shell(ctx, command)
	if err != nil {
		t.Fatalf("shell %q in %s: %v", command, ref, err)
	}
	return shellResult{
		success:  result.Success(),
		exitCode: result.ExitCode(),
		stdout:   result.Stdout(),
		stderr:   result.Stderr(),
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
	requireMsbBackend(t)
	useRealHostRuntime(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}

	ref := buildImageForRun(t, a, nil, false, "agents-sandbox/it-node-agent-"+sanitizeTag(t.Name()))

	for _, command := range []string{"node --version", "npm --version"} {
		result := runInImage(t, ref, "bash -lc '"+command+"'")
		if !result.success {
			t.Errorf("%q failed in the built image (exit %d): %s", command, result.exitCode, result.stderr)
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
		//t.Log(line)
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
		dumpBuilderFailure(t, context.Background())
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
	version := integrationAgentVersion(t, a)

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
	requireMsbBackend(t)
	useRealHostRuntime(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	version := integrationAgentVersion(t, a)

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
	requireMsbBackend(t)
	useRealHostRuntime(t)
	a := agentOpencode(t)

	project := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n")

	// The run-wide shared config dir (sharedIntegrationConfigDir) also shares the
	// builder's worker cache across every test, so any release the rest of the
	// suite baked is already warm. The suite pins opencode to
	// defaultIntegrationOpencodeVersion, so the "new" version here must be a
	// different release or its agent layer is a false cache hit. Use two older
	// releases: defaults only move forward, so older ones never collide.
	oldVersion, newVersion := "1.18.32", "1.18.33"
	if oldVersion == integrationOpencodeVersion() || newVersion == integrationOpencodeVersion() {
		t.Fatalf("test versions must differ from the pinned default %q", integrationOpencodeVersion())
	}
	tag := "agents-sandbox/it-agent-version"
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
	requireMsbBackend(t)
	useRealHostRuntime(t)
	a := agentOpencode(t)

	project := []byte("FROM agents-sandbox/runner-base:latest\nENV PATH=\"/opt/my-tools:${PATH}\"\n")
	ref := buildImageForRun(t, a, project, false, "agents-sandbox/it-login-path-"+sanitizeTag(t.Name()))

	result := runInImage(t, ref, `bash -lc 'printf %s "$PATH"'`)
	if !result.success {
		t.Fatalf("login shell failed (exit %d): %s", result.exitCode, result.stderr)
	}
	loginPath := result.stdout
	for _, want := range []string{"/opt/agents-sandbox/bin", "/opt/my-tools"} {
		if !strings.Contains(loginPath, want) {
			t.Errorf("login PATH %q missing %q", loginPath, want)
		}
	}
}
