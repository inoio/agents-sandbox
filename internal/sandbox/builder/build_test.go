package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
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

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

type fakeBuildkitClient struct {
	mu    sync.Mutex
	solve []client.SolveOpt
	// solveFn overrides the default solve behavior when set.
	solveFn func(opt client.SolveOpt) (*client.SolveResponse, error)
	// emitVertices are sent on the status channel during Solve, simulating
	// build step progress for cache-hit detection tests.
	emitVertices []*client.Vertex
	// emitDelay is slept before emitting vertices, so timing tests can observe
	// a non-trivial solve warmup.
	emitDelay time.Duration
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
	if f.emitDelay > 0 {
		time.Sleep(f.emitDelay)
	}
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

// solveOpt returns the single solve Build issues, which carries the image
// export and the registry cache export together.
func (f *fakeBuildkitClient) solveOpt(t *testing.T) client.SolveOpt {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.solve) == 0 {
		t.Fatal("Solve was not called")
	}
	return f.solve[0]
}

// newBuildTestClient wires a Client whose backend streams an OCI archive and
// whose dial returns the supplied fake, so Build tests never boot a VM. Tests
// exercising the archive path use this; the registry-push path is covered by
// pushBackend.
func newBuildTestClient(t *testing.T, fake buildkitClient) *Client {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	stubBuildkitDial(t, fake)
	stubCacheRegistry(t, testCacheEndpoint())
	return &Client{backend: &fakeBackend{}}
}

// pushBackend is a backend whose Export requests a registry push (Stream false),
// mirroring the msb backend, so Build's push path can be tested without a live
// registry. It reuses fakeBackend's Ensure/KeepAlive and records the push ref
// Build derives for the solved image.
type pushBackend struct {
	fakeBackend

	pushRef string
}

func (b *pushBackend) Export(pushRef string) exportSpec {
	b.pushRef = pushRef
	return exportSpec{Type: "image", Attrs: map[string]string{
		"name":              pushRef,
		"push":              buildkitAttrTrue,
		"oci-mediatypes":    buildkitAttrTrue,
		"registry.insecure": buildkitAttrTrue,
	}, Stream: false}
}

// TestBuildPushesImageToRegistryWithoutArchive asserts that on the msb backend's
// push path Build emits a single image export with no Output writer, names it
// with the guest-authority push ref, and produces no local archive.
func TestBuildPushesImageToRegistryWithoutArchive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	fake := &fakeBuildkitClient{}
	stubBuildkitDial(t, fake)
	stubCacheRegistry(t, testCacheEndpoint())

	const tag = "project/opencode:latest"
	backend := &pushBackend{}
	result, err := (&Client{backend: backend}).Build(context.Background(), BuildRequest{
		ContextDir: t.TempDir(),
		Tag:        tag,
		Platform:   "linux/amd64",
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if result.ArchivePath != "" {
		t.Errorf("ArchivePath = %q, want empty on the registry-push path", result.ArchivePath)
	}

	opt := fake.solveOpt(t)
	if len(opt.Exports) != 1 {
		t.Fatalf("Exports = %+v, want a single image export", opt.Exports)
	}
	entry := opt.Exports[0]
	if entry.Type != "image" {
		t.Errorf("export type = %q, want image", entry.Type)
	}
	if entry.Output != nil {
		t.Error("export Output is set, want nil on the registry-push path")
	}
	if entry.Attrs["push"] != buildkitAttrTrue {
		t.Errorf("export push attr = %q, want %q", entry.Attrs["push"], buildkitAttrTrue)
	}
	wantRef := testCacheEndpoint().PushRef(tag)
	if entry.Attrs["name"] != wantRef {
		t.Errorf("export name = %q, want the guest push ref %q", entry.Attrs["name"], wantRef)
	}
	if backend.pushRef != wantRef {
		t.Errorf("backend received push ref %q, want %q", backend.pushRef, wantRef)
	}
}

// testCacheEndpoint is a fixed embedded-registry endpoint for build tests.
func testCacheEndpoint() cacheregistry.Endpoint {
	return cacheregistry.Endpoint{Host: cacheregistry.GuestHost, Port: 5000, Token: "test-token"}
}

// stubCacheRegistry replaces the embedded-registry acquisition and prune seams
// so tests never spawn the registry daemon.
func stubCacheRegistry(t *testing.T, endpoint cacheregistry.Endpoint) {
	t.Helper()
	restoreEnsure := ensureCacheRegistry
	restorePrune := pruneCacheRegistry
	t.Cleanup(func() {
		ensureCacheRegistry = restoreEnsure
		pruneCacheRegistry = restorePrune
	})
	ensureCacheRegistry = func(context.Context) (cacheregistry.Endpoint, error) {
		return endpoint, nil
	}
	pruneCacheRegistry = func(context.Context, cacheregistry.Endpoint, func(string) bool) error {
		return nil
	}
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

// Export streams an OCI archive to the solve's output writer, mirroring a
// backend that delivers a local archive (the moby backend). The msb backend
// pushes to the registry instead, which pushBackend models.
func (b *fakeBackend) Export(string) exportSpec {
	return exportSpec{Type: "oci", Stream: true}
}

func (b *fakeBackend) KeepAlive(context.Context) func() {
	b.keptAlive = true
	return func() { b.stopped = true }
}

// TestBuildKeepsBuilderAlive asserts Build starts the keep-alive before solving
// and stops it when the build returns, so a short builder idle timeout does not
// reap the VM mid-build.
func TestBuildKeepsBuilderAlive(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)
	stubCacheRegistry(t, testCacheEndpoint())

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

// TestBuildKeepsCacheRegistryAlive asserts Build holds the cache registry's idle
// timer open for the duration of the build, so the shared idle timeout cannot
// reap the daemon mid-build and reset the cache-export connection.
func TestBuildKeepsCacheRegistryAlive(t *testing.T) {
	stubResolveBaseDigest(t, "base@sha256:x", nil)
	stubCacheRegistry(t, testCacheEndpoint())
	stubBuildkitDial(t, &fakeBuildkitClient{})

	var started, stopped bool
	restore := registryKeepAlive
	t.Cleanup(func() { registryKeepAlive = restore })
	registryKeepAlive = func(context.Context, cacheregistry.Endpoint) func() {
		started = true
		return func() { stopped = true }
	}

	if _, err := (&Client{backend: &fakeBackend{}}).Build(context.Background(), BuildRequest{
		ContextDir: "/tmp/context",
		Platform:   "linux/amd64",
	}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if !started {
		t.Error("Build did not start the registry keep-alive")
	}
	if !stopped {
		t.Error("Build did not stop the registry keep-alive")
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
		if len(opt.Exports) == 0 {
			return &client.SolveResponse{ExporterResponse: map[string]string{}}, nil
		}
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

// TestBuildProgressReportsStepsAndCachedMarkers asserts a report surfaces trimmed
// log lines, each Dockerfile step's name as it starts, and a CACHED marker for a
// step served from cache — and that a repeated status update for the same vertex
// (BuildKit re-sends every vertex) emits the step only once.
func TestBuildProgressReportsStepsAndCachedMarkers(t *testing.T) {
	var lines []string
	reporter := newBuildProgressReporter(func(line string) { lines = append(lines, line) })

	started := time.Now()
	status := &client.SolveStatus{
		Logs: []*client.VertexLog{
			{Data: []byte("installing node\n")},
			{Data: []byte("   \n")},
		},
		Vertexes: []*client.Vertex{
			{Name: "dockerfile.v0"},
			{Name: "[internal] load metadata for base"},
			{Name: "[2/5] RUN apt-get update", Started: &started},
			{Name: "[node 4/5] RUN install", Cached: true},
		},
	}
	reporter.report(status)
	// BuildKit re-sends vertices in later updates (logs are incremental), so a
	// repeated vertex must not yield a duplicate step line.
	reporter.report(&client.SolveStatus{Vertexes: status.Vertexes})

	want := []string{
		"installing node",
		"[2/5] RUN apt-get update",
		"[node 4/5] RUN install CACHED",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("progress lines = %v, want %v", lines, want)
	}
}

// TestBuildProgressSkipsQueuedAndInternalVertices asserts a queued step (no
// timestamps yet, not cached) and buildkit-internal vertices produce no progress
// line.
func TestBuildProgressSkipsQueuedAndInternalVertices(t *testing.T) {
	var lines []string
	reporter := newBuildProgressReporter(func(line string) { lines = append(lines, line) })
	reporter.report(&client.SolveStatus{
		Vertexes: []*client.Vertex{
			{Name: "dockerfile.v0"},
			{Name: "[internal] load metadata for base"},
			{Name: "[1/5] FROM debian:trixie-slim"},
		},
	})
	if len(lines) != 0 {
		t.Errorf("progress lines = %v, want none", lines)
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

func TestBuildExportsCacheInSingleSolve(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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
	// Image and cache export are folded into one solve, so the cache is exported
	// even on a full hit without a second, redundant graph traversal.
	if len(fake.solve) != 1 {
		t.Fatalf("Solve called %d times, want 1 (image and cache export in one solve)", len(fake.solve))
	}
	if len(fake.solve[0].CacheImports) != 1 {
		t.Errorf("solve CacheImports = %+v, want the import", fake.solve[0].CacheImports)
	}
	if len(fake.solve[0].CacheExports) != 1 {
		t.Errorf("solve CacheExports = %+v, want one", fake.solve[0].CacheExports)
	}
}

func TestBuildExportsCacheOnMiss(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	completed := time.Now()
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[1/1] RUN echo rebuilt", Cached: false, Completed: &completed},
		},
		solveFn: archiveSolveFn(),
	}

	var lines []string
	req := cacheExportTestRequest(t)
	req.Progress = func(line string) { lines = append(lines, line) }

	if _, err := newBuildTestClient(t, fake).Build(context.Background(), req); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(lines) != 1 || lines[0] != "[1/1] RUN echo rebuilt" {
		t.Errorf("progress lines = %v, want only the image solve's step", lines)
	}
	// The image and cache exports run in the same solve: one solve carrying both
	// the image export and the registry cache export.
	if len(fake.solve) != 1 {
		t.Fatalf("Solve called %d times, want 1", len(fake.solve))
	}
	first := fake.solve[0]
	if len(first.Exports) != 1 || len(first.CacheExports) != 1 {
		t.Errorf(
			"solve should export image and cache, got Exports=%d CacheExports=%v",
			len(first.Exports),
			first.CacheExports,
		)
	}
}

// TestBuildRecordsTimings asserts a build records its phase timings: total and
// solve are non-zero, and the solve warmup captures the delay before the first
// status (the cache import + graph setup).
func TestBuildRecordsTimings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	completed := time.Now()
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[1/1] RUN echo rebuilt", Cached: false, Completed: &completed},
		},
		emitDelay: 20 * time.Millisecond,
		solveFn:   archiveSolveFn(),
	}

	result, err := newBuildTestClient(t, fake).Build(context.Background(), cacheExportTestRequest(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	timings := result.Timings
	if timings.Total <= 0 || timings.Solve <= 0 {
		t.Fatalf("timings not recorded: %+v", timings)
	}
	if timings.SolveWarmup < 20*time.Millisecond {
		t.Errorf(
			"SolveWarmup = %s, want >= %s (the delay before the first status)",
			timings.SolveWarmup,
			20*time.Millisecond,
		)
	}
	if timings.Export < 0 {
		t.Errorf("Export = %s, want non-negative", timings.Export)
	}
}

// TestBuildRecordsStepTimings asserts a build surfaces each Dockerfile step's
// reported duration, so a solve that is large despite few changed layers can be
// attributed to specific steps.
func TestBuildRecordsStepTimings(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	stubResolveBaseDigest(t, "base@sha256:x", nil)

	started := time.Now()
	completed := started.Add(250 * time.Millisecond)
	fake := &fakeBuildkitClient{
		emitVertices: []*client.Vertex{
			{Name: "[agents-sandbox-agent 1/1] RUN install", Started: &started, Completed: &completed, Cached: true},
		},
		solveFn: archiveSolveFn(),
	}

	result, err := newBuildTestClient(t, fake).Build(context.Background(), cacheExportTestRequest(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(result.VertexTimings) != 1 {
		t.Fatalf("VertexTimings = %v, want one entry", result.VertexTimings)
	}
	if result.VertexTimings[0].Duration != 250*time.Millisecond {
		t.Errorf("vertex duration = %s, want 250ms", result.VertexTimings[0].Duration)
	}
	if !result.VertexTimings[0].Cached {
		t.Error("vertex should be reported cached")
	}
	if !result.VertexTimings[0].Step {
		t.Error("vertex should be tagged as a Dockerfile step")
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
	if detector.steps() != 1 {
		t.Errorf("steps = %d, want 1 (only the RUN step)", detector.steps())
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

func TestBuildNoCacheSingleSolveStillExportsCache(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
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
	// With the exports folded, the single solve carries the no-cache attribute
	// (rebuild everything) and still exports the freshly built cache.
	if len(fake.solve) != 1 {
		t.Fatalf("Solve called %d times, want 1", len(fake.solve))
	}
	if _, ok := fake.solve[0].FrontendAttrs["no-cache"]; !ok {
		t.Error("solve should carry the no-cache attribute")
	}
	if len(fake.solve[0].CacheExports) != 1 {
		t.Errorf("solve CacheExports = %+v, want one", fake.solve[0].CacheExports)
	}
}
