package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/opencontainers/go-digest"
	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

type fakeBuildkitClient struct {
	mu    sync.Mutex
	solve []client.SolveOpt
	// solveFn overrides the default solve behavior when set.
	solveFn func(opt client.SolveOpt) (*client.SolveResponse, error)
	// emitVertices are sent on the status channel during Solve, simulating
	// build step progress for cache-hit detection tests.
	emitVertices []*client.Vertex
	// resolveImageConfigFn overrides the default ResolveImageConfig behavior.
	resolveImageConfigFn func(ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error)
	closeCalls           atomic.Int32
}

func (f *fakeBuildkitClient) ListWorkers(context.Context) error { return nil }

func (f *fakeBuildkitClient) Solve(
	_ context.Context, _ *llb.Definition, opt client.SolveOpt, status chan *client.SolveStatus,
) (*client.SolveResponse, error) {
	// Mirror the real client, which owns closing the status channel.
	defer close(status)
	f.mu.Lock()
	f.solve = append(f.solve, opt)
	f.mu.Unlock()
	for _, v := range f.emitVertices {
		status <- &client.SolveStatus{Vertexes: []*client.Vertex{v}}
	}
	if f.solveFn != nil {
		return f.solveFn(opt)
	}
	return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
}

func (f *fakeBuildkitClient) ResolveImageConfig(
	_ context.Context, ref string, opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	if f.resolveImageConfigFn != nil {
		return f.resolveImageConfigFn(ref, opt)
	}
	return "", "", nil, errors.New("unexpected ResolveImageConfig call")
}

func (f *fakeBuildkitClient) Close() error {
	f.closeCalls.Add(1)
	return nil
}

func (f *fakeBuildkitClient) solveOpt(t *testing.T) client.SolveOpt {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.solve) != 1 {
		t.Fatalf("Solve called %d times, want 1", len(f.solve))
	}
	return f.solve[0]
}

// newBuildTestClient wires a Client whose builder VM resolves to a fixed host
// port and whose dial returns the supplied fake, so Build tests never boot a VM.
func newBuildTestClient(t *testing.T, fake buildkitClient) *Client {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	stubBuildkitDial(t, fake)

	handle := &msb.MockSandboxHandle{
		Name_: builderSandboxName(),
		Cfg: &msbSdk.SandboxConfig{
			PortBindings: []msbSdk.PortBinding{
				{Bind: "127.0.0.1", HostPort: 4242, GuestPort: builderGuestPort},
			},
		},
	}
	msbClient := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return handle, nil
		},
	}
	return &Client{backend: &msbBackend{vm: &VM{Msb: msbClient}}}
}

// stubBuildkitDial makes newBuildkitClient return fake without dialing.
func stubBuildkitDial(t *testing.T, fake buildkitClient) {
	t.Helper()
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })
	newBuildkitClient = func(context.Context, string) (buildkitClient, error) { return fake, nil }
}

// fakeBackend records the keep-alive lifecycle so a Build test can assert the
// builder VM is held open for the duration of the build.
type fakeBackend struct {
	keptAlive bool
	stopped   bool
}

func (b *fakeBackend) Ensure(context.Context) (int, error) { return 4242, nil }
func (b *fakeBackend) PruneCache(context.Context) error    { return nil }
func (b *fakeBackend) Export(string) exportSpec            { return exportSpec{Type: "oci"} }
func (b *fakeBackend) KeepAlive(context.Context) func() {
	b.keptAlive = true
	return func() { b.stopped = true }
}

// TestBuildKeepsBuilderAlive asserts Build starts the keep-alive before solving
// and stops it when the build returns, so a short builder idle timeout does not
// reap the VM mid-build.
func TestBuildKeepsBuilderAlive(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	stubBuildkitDial(t, fake)

	backend := &fakeBackend{}
	if _, err := (&Client{backend: backend}).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if !backend.keptAlive {
		t.Error("Build did not start the builder keep-alive")
	}
	if !backend.stopped {
		t.Error("Build did not stop the builder keep-alive")
	}
}

func stubResolveBaseDigest(t *testing.T, resolved string, resolveErr error) {
	t.Helper()
	restore := resolveBaseDigest
	t.Cleanup(func() { resolveBaseDigest = restore })
	resolveBaseDigest = func(context.Context, buildkitClient, string, string) (string, error) {
		return resolved, resolveErr
	}
}

func TestBuildSolveOptsAndExports(t *testing.T) {
	const baseDigest = "docker.io/library/base@sha256:resolved"
	stubResolveBaseDigest(t, baseDigest, nil)

	fake := &fakeBuildkitClient{}
	fake.solveFn = func(opt client.SolveOpt) (*client.SolveResponse, error) {
		writer, err := opt.Exports[0].Output(nil)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte("oci-archive-bytes")); err != nil {
			return nil, err
		}
		return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
	}

	req := BuildRequest{
		ContextDir: "/tmp/context",
		Tag:        "agents-sandbox:test",
		BaseRef:    "docker.io/library/base:latest",
		BuildArgs:  map[string]string{"AGENT": "opencode", "ID": "commit-1"},
		NoCache:    true,
		Platform:   "linux/arm64",
	}

	result, err := newBuildTestClient(t, fake).Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	opt := fake.solveOpt(t)
	if opt.Frontend != "dockerfile.v0" {
		t.Errorf("Frontend = %q, want dockerfile.v0", opt.Frontend)
	}
	if opt.LocalDirs["context"] != req.ContextDir || opt.LocalDirs["dockerfile"] != req.ContextDir {
		t.Errorf("LocalDirs = %v, want context/dockerfile at %q", opt.LocalDirs, req.ContextDir)
	}
	if opt.FrontendAttrs["platform"] != req.Platform {
		t.Errorf("platform attr = %q, want %q", opt.FrontendAttrs["platform"], req.Platform)
	}
	if value, ok := opt.FrontendAttrs["no-cache"]; !ok || value != "" {
		t.Errorf("no-cache attr = %q (present=%v), want present and empty when NoCache is true", value, ok)
	}
	for key, value := range req.BuildArgs {
		if got := opt.FrontendAttrs["build-arg:"+key]; got != value {
			t.Errorf("build-arg:%s = %q, want %q", key, got, value)
		}
	}
	if got := opt.FrontendAttrs["build-arg:BASE_IMAGE"]; got != baseDigest {
		t.Errorf("build-arg:BASE_IMAGE = %q, want %q", got, baseDigest)
	}
	if len(opt.Session) == 0 {
		t.Error("SolveOpt.Session is empty, want registry auth attachables")
	}
	if len(opt.Exports) != 1 || opt.Exports[0].Type != "oci" {
		t.Fatalf("Exports = %+v, want a single oci export", opt.Exports)
	}
	if opt.Exports[0].Output == nil {
		t.Fatal("oci export Output is nil")
	}
	if result.ArchivePath == "" {
		t.Fatal("ArchivePath is empty")
	}
	info, statErr := os.Stat(result.ArchivePath)
	if statErr != nil {
		t.Fatalf("ArchivePath does not exist: %v", statErr)
	}
	if info.Size() == 0 {
		t.Fatal("ArchivePath is empty on disk")
	}
	if result.BaseDigest != baseDigest {
		t.Errorf("BaseDigest = %q, want %q", result.BaseDigest, baseDigest)
	}
}

func TestBuildSolveOmitsNoCacheWhenFalse(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	if _, err := newBuildTestClient(t, fake).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if _, ok := fake.solveOpt(t).FrontendAttrs["no-cache"]; ok {
		t.Error("no-cache attr present when NoCache is false")
	}
}

func TestBuildToleratesSessionClosingExportWriter(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	fake.solveFn = func(opt client.SolveOpt) (*client.SolveResponse, error) {
		writer, err := opt.Exports[0].Output(nil)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte("oci-archive-bytes")); err != nil {
			return nil, err
		}
		// BuildKit's filesync session closes every writer it streams; the file
		// behind the export writer must survive so Build can close it once.
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
	}

	result, err := newBuildTestClient(t, fake).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	})
	if err != nil {
		t.Fatalf("Build failed after the session closed the export writer: %v", err)
	}
	info, statErr := os.Stat(result.ArchivePath)
	if statErr != nil {
		t.Fatalf("ArchivePath missing after build: %v", statErr)
	}
	if info.Size() == 0 {
		t.Error("archive is empty on disk")
	}
}

func TestBuildParsesExporterMetadata(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	configJSON, err := json.Marshal(map[string]any{
		"config": map[string]any{"Env": []string{"PATH=/usr/bin", "FOO=bar"}},
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	fake := &fakeBuildkitClient{}
	fake.solveFn = func(client.SolveOpt) (*client.SolveResponse, error) {
		return &client.SolveResponse{ExporterResponse: map[string]string{
			"containerimage.digest": "sha256:abc",
			"containerimage.config": string(configJSON),
		}}, nil
	}

	result, err := newBuildTestClient(t, fake).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if result.Digest != "sha256:abc" {
		t.Errorf("Digest = %q, want sha256:abc", result.Digest)
	}
	wantEnv := []string{"PATH=/usr/bin", "FOO=bar"}
	if len(result.Env) != len(wantEnv) {
		t.Fatalf("Env = %v, want %v", result.Env, wantEnv)
	}
	for i := range wantEnv {
		if result.Env[i] != wantEnv[i] {
			t.Errorf("Env[%d] = %q, want %q", i, result.Env[i], wantEnv[i])
		}
	}
}

// TestBuildPrefersConfigDigest verifies the returned Digest is the OCI config
// digest, matching what microsandbox's ImageConfig.Digest reports on reuse, so
// the reuse and build paths agree and do not trigger a spurious image-changed
// prompt. Falls back to the manifest digest only when the config digest is
// absent.
func TestBuildPrefersConfigDigest(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	fake.solveFn = func(client.SolveOpt) (*client.SolveResponse, error) {
		return &client.SolveResponse{ExporterResponse: map[string]string{
			"containerimage.digest":        "sha256:manifest",
			"containerimage.config.digest": "sha256:config",
		}}, nil
	}
	result, err := newBuildTestClient(t, fake).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if result.Digest != "sha256:config" {
		t.Errorf("Digest = %q, want the config digest sha256:config", result.Digest)
	}
}

func TestBuildAcceptsOCIExportType(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	fake.solveFn = func(opt client.SolveOpt) (*client.SolveResponse, error) {
		writer, err := opt.Exports[0].Output(nil)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte("archive")); err != nil {
			return nil, err
		}
		return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
	}
	builder := newBuildTestClient(t, fake)

	const callers = 2
	paths := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := builder.Build(context.Background(), BuildRequest{
				ContextDir: "/tmp/context",
				Platform:   "linux/amd64",
			})
			paths[i], errs[i] = result.ArchivePath, err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Build[%d] failed: %v", i, err)
		}
	}
	if paths[0] == paths[1] {
		t.Fatalf("concurrent builds share archive path %q", paths[0])
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("archive %q missing: %v", path, err)
		}
	}
}

func TestResolveBaseDigestWrapsResolutionError(t *testing.T) {
	const baseRef = "docker.io/private/secret:latest"
	stubResolveBaseDigest(t, "", errors.New("no credentials"))

	fake := &fakeBuildkitClient{}
	_, err := newBuildTestClient(t, fake).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		BaseRef:    baseRef,
		Platform:   "linux/amd64",
	})
	if err == nil {
		t.Fatal("Build returned nil error, want wrapped resolution error")
	}
	if want := "resolve base image " + baseRef + ":"; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error = %q, want prefix %q", err.Error(), want)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.solve) != 0 {
		t.Errorf("Solve called %d times despite resolution failure", len(fake.solve))
	}
}

func TestRealResolveBaseDigestReturnsRefAtDigest(t *testing.T) {
	fake := &fakeBuildkitClient{
		resolveImageConfigFn: func(ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
			if ref != "docker.io/library/base:latest" {
				t.Errorf("resolved ref = %q, want the requested base ref", ref)
			}
			if opt.Platform == nil || opt.Platform.Architecture != runtime.GOARCH {
				t.Errorf("platform = %+v, want architecture %q", opt.Platform, runtime.GOARCH)
			}
			return ref, digest.FromString("config"), []byte("{}"), nil
		},
	}

	got, err := realResolveBaseDigest(context.Background(), fake, "docker.io/library/base:latest", HostPlatform())
	if err != nil {
		t.Fatalf("realResolveBaseDigest failed: %v", err)
	}
	want := "docker.io/library/base:latest@" + digest.FromString("config").String()
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

func TestRealResolveBaseDigestQualifiesShortRef(t *testing.T) {
	fake := &fakeBuildkitClient{
		resolveImageConfigFn: func(ref string, _ sourceresolver.Opt) (string, digest.Digest, []byte, error) {
			if ref != "docker.io/library/debian:trixie-slim" {
				t.Errorf("resolved ref = %q, want the fully-qualified form", ref)
			}
			return ref, digest.FromString("config"), []byte("{}"), nil
		},
	}

	got, err := realResolveBaseDigest(context.Background(), fake, "debian:trixie-slim", HostPlatform())
	if err != nil {
		t.Fatalf("realResolveBaseDigest failed: %v", err)
	}
	want := "debian:trixie-slim@" + digest.FromString("config").String()
	if got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

func TestHostPlatform(t *testing.T) {
	want := "linux/amd64"
	if runtime.GOARCH == "arm64" {
		want = "linux/arm64"
	}
	if got := HostPlatform(); got != want {
		t.Errorf("HostPlatform() = %q, want %q", got, want)
	}
}

// TestForwardProgressEmitsCachedVertices asserts cached vertices are surfaced
// (as "<name> CACHED") alongside trimmed, non-empty log lines, so callers can
// observe BuildKit layer-cache reuse.
func TestForwardProgressEmitsCachedVertices(t *testing.T) {
	var lines []string
	forwardProgress(&client.SolveStatus{
		Logs: []*client.VertexLog{
			{Data: []byte("installing node\n")},
			{Data: []byte("   \n")},
		},
		Vertexes: []*client.Vertex{
			{Name: "dockerfile.v0", Cached: false},
			{Name: "[node 4/5] RUN install", Cached: true},
		},
	}, func(line string) { lines = append(lines, line) })

	if len(lines) != 2 {
		t.Fatalf("progress lines = %v, want the trimmed log line and the cached vertex", lines)
	}
	if lines[0] != "installing node" {
		t.Errorf("lines[0] = %q, want the trimmed log line", lines[0])
	}
	if !strings.Contains(lines[1], "CACHED") {
		t.Errorf("lines[1] = %q, want a CACHED marker for the cached vertex", lines[1])
	}
}

// TestForwardProgressSkipsUncachedVertices asserts uncached vertices produce no
// progress line.
func TestForwardProgressSkipsUncachedVertices(t *testing.T) {
	var lines []string
	forwardProgress(&client.SolveStatus{
		Vertexes: []*client.Vertex{{Name: "dockerfile.v0", Cached: false}},
	}, func(line string) { lines = append(lines, line) })
	if len(lines) != 0 {
		t.Errorf("progress lines = %v, want none for an uncached vertex", lines)
	}
}

// archiveSolveFn returns a solve function that writes a stub image archive when
// an image export is configured and tolerates a cache-only solve (no image).
func archiveSolveFn() func(client.SolveOpt) (*client.SolveResponse, error) {
	return func(opt client.SolveOpt) (*client.SolveResponse, error) {
		if len(opt.Exports) == 0 {
			return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
		}
		writer, err := opt.Exports[0].Output(nil)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write([]byte("oci-archive-bytes")); err != nil {
			return nil, err
		}
		return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
	}
}

func cacheExportTestRequest(t *testing.T) BuildRequest {
	t.Helper()
	return BuildRequest{
		ContextDir: t.TempDir(),
		Tag:        "agents-sandbox:test",
		BaseRef:    "docker.io/library/base:latest",
		Platform:   "linux/amd64",
	}
}

func TestBuildSkipsCacheExportOnFullHit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv(builderCacheModeEnv, cacheModeTmpfsExport)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	completed := time.Now()
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[internal] load metadata for base", Cached: false, Completed: &completed},
			{Name: "[1/1] RUN echo cached", Cached: true, Completed: &completed},
		},
		solveFn: archiveSolveFn(),
	}

	if _, err := newBuildTestClient(t, fake).Build(context.Background(), cacheExportTestRequest(t)); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(fake.solve) != 1 {
		t.Fatalf("Solve called %d times, want 1 (no export pass on a full hit)", len(fake.solve))
	}
	if len(fake.solve[0].CacheImports) != 1 {
		t.Errorf("first solve CacheImports = %+v, want the import", fake.solve[0].CacheImports)
	}
	if len(fake.solve[0].CacheExports) != 0 {
		t.Errorf("first solve CacheExports = %+v, want none", fake.solve[0].CacheExports)
	}
}

func TestBuildExportsCacheOnMiss(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv(builderCacheModeEnv, cacheModeTmpfsExport)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	completed := time.Now()
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[1/1] RUN echo rebuilt", Cached: false, Completed: &completed},
		},
		solveFn: archiveSolveFn(),
	}

	if _, err := newBuildTestClient(t, fake).Build(context.Background(), cacheExportTestRequest(t)); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(fake.solve) != 2 {
		t.Fatalf("Solve called %d times, want 2 (image solve + cache export)", len(fake.solve))
	}
	first, second := fake.solve[0], fake.solve[1]
	if len(first.CacheExports) != 0 || len(first.Exports) != 1 {
		t.Errorf(
			"first solve exports image only, got CacheExports=%v Exports=%d",
			first.CacheExports,
			len(first.Exports),
		)
	}
	if len(second.CacheExports) != 1 {
		t.Errorf("second solve CacheExports = %+v, want one", second.CacheExports)
	}
	if len(second.Exports) != 0 {
		t.Errorf("second solve Exports = %+v, want none (cache-only)", second.Exports)
	}
}

func TestBuildStepCacheDetector(t *testing.T) {
	completed := time.Now()
	detector := newBuildStepCacheDetector()
	detector.observe(&client.SolveStatus{Vertexes: []*client.Vertex{
		{Name: "[internal] load metadata", Cached: false, Completed: &completed},
		{Name: "[1/2] FROM debian:trixie", Cached: false, Completed: &completed},
		{Name: "[2/2] RUN a", Cached: false, Completed: &completed},
		{Name: "[2/2] RUN a", Cached: true, Completed: &completed},
	}})
	if !detector.fullHit() {
		t.Errorf(
			"fullHit() = false, want true (FROM/internal excluded; RUN transiently false then cached); steps=%d",
			detector.steps(),
		)
	}
	if detector.steps() != 1 {
		t.Errorf("steps = %d, want 1 (only the RUN step)", detector.steps())
	}

	missed := newBuildStepCacheDetector()
	missed.observe(&client.SolveStatus{Vertexes: []*client.Vertex{
		{Name: "[1/1] RUN a", Cached: false, Completed: &completed},
	}})
	if missed.fullHit() {
		t.Error("fullHit() = true for a step that never cached, want false")
	}
}

func TestIsDockerfileStep(t *testing.T) {
	cases := map[string]bool{
		"[1/2] RUN echo":           true,
		"[node 3/5] RUN npm":       true,
		"[1/2] FROM debian:trixie": false,
		"[internal] load metadata": false,
		"dockerfile.v0":            false,
	}
	for name, want := range cases {
		if got := isDockerfileStep(name); got != want {
			t.Errorf("isDockerfileStep(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCacheEnabledAttrsDropsNoCache(t *testing.T) {
	in := map[string]string{"platform": "linux/amd64", "no-cache": ""}
	out := cacheEnabledAttrs(in)
	if _, ok := out["no-cache"]; ok {
		t.Error("no-cache attribute was not removed")
	}
	if out["platform"] != "linux/amd64" {
		t.Errorf("platform = %q, want preserved", out["platform"])
	}
	if _, ok := in["no-cache"]; !ok {
		t.Error("input map was mutated")
	}
	plain := map[string]string{"platform": "linux/amd64"}
	if got := cacheEnabledAttrs(plain); len(got) != 1 || got["platform"] != "linux/amd64" {
		t.Errorf("cacheEnabledAttrs(no no-cache) = %v, want unchanged", got)
	}
}

func TestBuildExportPassDropsNoCache(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv(builderCacheModeEnv, cacheModeTmpfsExport)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	completed := time.Now()
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[1/1] RUN x", Cached: false, Completed: &completed},
		},
		solveFn: archiveSolveFn(),
	}
	req := cacheExportTestRequest(t)
	req.NoCache = true

	if _, err := newBuildTestClient(t, fake).Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(fake.solve) != 2 {
		t.Fatalf("Solve called %d times, want 2", len(fake.solve))
	}
	if _, ok := fake.solve[0].FrontendAttrs["no-cache"]; !ok {
		t.Error("first solve should carry the no-cache attribute")
	}
	if _, ok := fake.solve[1].FrontendAttrs["no-cache"]; ok {
		t.Error("export pass must drop no-cache so it reuses the just-built cache")
	}
}
