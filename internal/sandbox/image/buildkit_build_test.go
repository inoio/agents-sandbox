package image

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	buildkitclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/tonistiigi/fsutil"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
)

// TestBuildKitSolveOptFrontendAndExports pins the dockerfile.v0 frontend over
// the rendered Dockerfile and the moby exporter tagging the image, so the
// BuildKit path produces the same tagged image the classic path does.
func TestBuildKitSolveOptFrontendAndExports(t *testing.T) {
	const tag = "agents-sandbox/test:latest"
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		tag,
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	if opt.Frontend != "dockerfile.v0" {
		t.Errorf("Frontend = %q, want dockerfile.v0", opt.Frontend)
	}
	if got := opt.FrontendAttrs["filename"]; got != "Dockerfile" {
		t.Errorf(`FrontendAttrs["filename"] = %q, want "Dockerfile"`, got)
	}
	wantExports := []buildkitclient.ExportEntry{{Type: "moby", Attrs: map[string]string{"name": tag}}}
	if !reflect.DeepEqual(opt.Exports, wantExports) {
		t.Errorf("Exports = %#v, want %#v", opt.Exports, wantExports)
	}
}

// TestBuildKitSolveOptLocalMountsPointAtTheContextDir pins that the daemon
// receives the one context directory under both local source names the
// dockerfile.v0 frontend reads, and that the directory serves the written
// context.
func TestBuildKitSolveOptLocalMountsPointAtTheContextDir(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	contextFS, contextOK := opt.LocalMounts["context"]
	dockerfileFS, dockerfileOK := opt.LocalMounts["dockerfile"]
	if !contextOK || !dockerfileOK {
		t.Fatalf("LocalMounts = %v, want both context and dockerfile entries", opt.LocalMounts)
	}
	if contextFS != dockerfileFS {
		t.Error("LocalMounts context and dockerfile entries differ, want the same context directory")
	}
	names := buildKitFSNames(t, contextFS)
	if !slices.Contains(names, "Dockerfile") {
		t.Errorf("context FS serves %v, want it to include the Dockerfile", names)
	}
}

// TestBuildKitSolveOptNoCacheFollowsParameter pins that a no-cache build is
// expressed through the dockerfile frontend's no-cache attribute (empty when
// set, absent when the build may use the cache), since a SolveOpt carries no
// NoCache field.
func TestBuildKitSolveOptNoCacheFollowsParameter(t *testing.T) {
	for _, noCache := range []bool{true, false} {
		t.Run(strconv.FormatBool(noCache), func(t *testing.T) {
			opt, cleanup, err := buildKitSolveOpt(
				testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
				"tag",
				noCache,
			)
			if err != nil {
				t.Fatalf("buildKitSolveOpt: %v", err)
			}
			defer cleanup()

			value, present := opt.FrontendAttrs["no-cache"]
			if present != noCache {
				t.Errorf("FrontendAttrs[\"no-cache\"] present = %v, want %v", present, noCache)
			}
			if noCache && value != "" {
				t.Errorf(`FrontendAttrs["no-cache"] = %q, want ""`, value)
			}
		})
	}
}

// TestBuildKitSolveOptCacheEntriesEmptyWhenUnset pins the cache-neutral
// default: a build with no cache configuration carries no cache entries.
func TestBuildKitSolveOptCacheEntriesEmptyWhenUnset(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	if len(opt.CacheImports) != 0 || len(opt.CacheExports) != 0 {
		t.Errorf("CacheImports = %v, CacheExports = %v, want both empty", opt.CacheImports, opt.CacheExports)
	}
}

// TestBuildKitSolveOptCacheEntriesPassThroughVerbatim pins that a configured
// cache spec's type and attributes reach the solve options uninterpreted.
func TestBuildKitSolveOptCacheEntriesPassThroughVerbatim(t *testing.T) {
	// The gha entry gains the Actions cache env attrs when they are set; clear
	// them so this test asserts the verbatim pass-through only.
	t.Setenv("ACTIONS_CACHE_URL", "")
	t.Setenv("ACTIONS_RESULTS_URL", "")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "")

	req := testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{})
	req.cacheFrom = []string{"type=gha,scope=runner-image,mode=max"}
	req.cacheTo = []string{"type=registry,ref=example.com/cache:main"}
	opt, cleanup, err := buildKitSolveOpt(req, "tag", false)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	wantImports := []buildkitclient.CacheOptionsEntry{
		{Type: "gha", Attrs: map[string]string{"scope": "runner-image", "mode": "max"}},
	}
	if !reflect.DeepEqual(opt.CacheImports, wantImports) {
		t.Errorf("CacheImports = %#v, want %#v", opt.CacheImports, wantImports)
	}
	wantExports := []buildkitclient.CacheOptionsEntry{
		{Type: "registry", Attrs: map[string]string{"ref": "example.com/cache:main"}},
	}
	if !reflect.DeepEqual(opt.CacheExports, wantExports) {
		t.Errorf("CacheExports = %#v, want %#v", opt.CacheExports, wantExports)
	}
}

// TestBuildKitCacheEntriesFillGitHubActionsEnv pins that a gha cache entry
// gains the cache-service url and token from the GitHub Actions environment:
// BuildKit reads them from the entry attributes only, and the daemon cannot see
// the step's environment, so the client has to carry them (as buildx does).
func TestBuildKitCacheEntriesFillGitHubActionsEnv(t *testing.T) {
	t.Setenv("ACTIONS_CACHE_URL", "https://cache.actions.example/")
	t.Setenv("ACTIONS_RESULTS_URL", "https://results.actions.example/")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "runtime-token")

	req := testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{})
	req.cacheTo = []string{"type=gha,scope=runner-image,mode=max"}
	opt, cleanup, err := buildKitSolveOpt(req, "tag", false)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	want := []buildkitclient.CacheOptionsEntry{{
		Type: "gha",
		Attrs: map[string]string{
			"scope":  "runner-image",
			"mode":   "max",
			"url":    "https://cache.actions.example/",
			"url_v2": "https://results.actions.example/",
			"token":  "runtime-token",
		},
	}}
	if !reflect.DeepEqual(opt.CacheExports, want) {
		t.Errorf("CacheExports = %#v, want %#v", opt.CacheExports, want)
	}
}

// TestBuildKitCacheEntriesExplicitAttrsBeatGitHubActionsEnv pins that an
// attribute the spec sets explicitly is not overwritten by the environment.
func TestBuildKitCacheEntriesExplicitAttrsBeatGitHubActionsEnv(t *testing.T) {
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "runtime-token")

	req := testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{})
	req.cacheTo = []string{"type=gha,token=explicit-token"}
	opt, cleanup, err := buildKitSolveOpt(req, "tag", false)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	want := []buildkitclient.CacheOptionsEntry{{
		Type:  "gha",
		Attrs: map[string]string{"token": "explicit-token"},
	}}
	if !reflect.DeepEqual(opt.CacheExports, want) {
		t.Errorf("CacheExports = %#v, want %#v", opt.CacheExports, want)
	}
}

// TestBuildKitCacheEntriesLeaveNonGitHubActionsTypesAlone pins that the
// Actions environment is applied only to the gha backend.
func TestBuildKitCacheEntriesLeaveNonGitHubActionsTypesAlone(t *testing.T) {
	t.Setenv("ACTIONS_CACHE_URL", "https://cache.actions.example/")
	t.Setenv("ACTIONS_RESULTS_URL", "https://results.actions.example/")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "runtime-token")

	req := testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{})
	req.cacheFrom = []string{"type=registry,ref=example.com/cache:main"}
	opt, cleanup, err := buildKitSolveOpt(req, "tag", false)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}
	defer cleanup()

	want := []buildkitclient.CacheOptionsEntry{
		{Type: "registry", Attrs: map[string]string{"ref": "example.com/cache:main"}},
	}
	if !reflect.DeepEqual(opt.CacheImports, want) {
		t.Errorf("CacheImports = %#v, want %#v", opt.CacheImports, want)
	}
}

// TestBuildKitSolveOptCleanupRemovesTheContextDir pins the returned cleanup's
// contract: once called, the temporary directory the local mounts served no
// longer exists.
func TestBuildKitSolveOptCleanupRemovesTheContextDir(t *testing.T) {
	opt, cleanup, err := buildKitSolveOpt(
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag",
		false,
	)
	if err != nil {
		t.Fatalf("buildKitSolveOpt: %v", err)
	}

	contextFS, ok := opt.LocalMounts["context"]
	if !ok {
		t.Fatal("LocalMounts has no context entry")
	}
	if len(buildKitFSNames(t, contextFS)) == 0 {
		t.Fatal("context FS served no entries before cleanup")
	}
	cleanup()
	if got := len(buildKitFSNames(t, contextFS)); got != 0 {
		t.Errorf("context FS still serves %d entries after cleanup, want the directory removed", got)
	}
}

// TestBuildKitWriteContextWritesDockerfileAndContextFiles pins that the
// on-disk BuildKit context carries the rendered Dockerfile and every
// buildContextFiles() entry at the same mode the classic context tar gives it,
// so the two builders receive byte-identical sources. A restrictive umask runs
// for the duration of the test, so the modes must survive it — proving the
// write path chmods explicitly instead of relying on the creating umask.
// The test is not parallel because umask is process-global.
func TestBuildKitWriteContextWritesDockerfileAndContextFiles(t *testing.T) {
	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	dir := t.TempDir()
	dockerfile := []byte("FROM scratch\nRUN echo hi\n")
	if err := writeBuildKitContext(dir, dockerfile); err != nil {
		t.Fatalf("writeBuildKitContext: %v", err)
	}

	dockerfilePath := filepath.Join(dir, "Dockerfile")
	gotDockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	if !bytes.Equal(gotDockerfile, dockerfile) {
		t.Errorf("Dockerfile = %q, want the rendered %q", gotDockerfile, dockerfile)
	}
	assertMode(t, dockerfilePath, dockerfileMode)
	for _, file := range buildContextFiles() {
		path := filepath.Join(dir, file.name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read context file %s: %v", file.name, err)
		}
		if !bytes.Equal(content, file.content) {
			t.Errorf("context file %s carries %d bytes, want %d", file.name, len(content), len(file.content))
		}
		assertMode(t, path, os.FileMode(file.mode))
	}
}

// assertMode fails the test unless path's permission bits equal want.
func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

// TestBuildKitClientDialers pins the two hijack endpoints the BuildKit client
// reaches the daemon's embedded builder through: the control connection dials
// /grpc with the h2c protocol and no metadata, and the session dialer forwards
// the requested protocol and its metadata to /session. A wrong endpoint here is
// what fails against an engine that does not serve the embedded builder.
func TestBuildKitClientDialers(t *testing.T) {
	dialedConn, dialedPeer := net.Pipe()
	t.Cleanup(func() {
		_ = dialedConn.Close()
		_ = dialedPeer.Close()
	})
	var gotURL, gotProto string
	var gotMeta map[string][]string
	m := &dockertest.MockDockerClient{
		DialHijackFn: func(_ context.Context, url, protocol string, meta map[string][]string) (net.Conn, error) {
			gotURL, gotProto, gotMeta = url, protocol, meta
			return dialedConn, nil
		},
	}

	if _, err := buildKitControlDialer(m)(context.Background(), "ignored"); err != nil {
		t.Fatalf("control dialer: %v", err)
	}
	if gotURL != "/grpc" || gotProto != "h2c" || gotMeta != nil {
		t.Errorf("control dialer hijacked (%q, %q, %v), want (/grpc, h2c, nil)", gotURL, gotProto, gotMeta)
	}

	meta := map[string][]string{"session-id": {"abc"}}
	if _, err := buildKitSessionDialer(m)(context.Background(), "h2c", meta); err != nil {
		t.Fatalf("session dialer: %v", err)
	}
	if gotURL != "/session" || gotProto != "h2c" {
		t.Errorf("session dialer hijacked (%q, %q), want (/session, h2c)", gotURL, gotProto)
	}
	if !reflect.DeepEqual(gotMeta, meta) {
		t.Errorf("session dialer meta = %v, want %v", gotMeta, meta)
	}
}

// TestBuildImageBuildKitReportsSolveFailure pins the no-silent-downgrade
// invariant on the BuildKit path: a Solve error surfaces as a build failure and
// is never swallowed into a classic retry.
func TestBuildImageBuildKitReportsSolveFailure(t *testing.T) {
	stubBuildKitClient(t, &fakeBuildKitSolver{solveErr: errors.New("solve boom")})

	err := buildImageBuildKit(
		context.Background(), &dockertest.MockDockerClient{}, agentOpencode(t),
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag", false, func(string) {},
	)
	if err == nil {
		t.Fatal("buildImageBuildKit error = nil, want a build failure")
	}
	if !strings.Contains(err.Error(), "docker image build failed") {
		t.Errorf("error = %v, want a buildFailureError wrap", err)
	}
	if !strings.Contains(err.Error(), "solve boom") {
		t.Errorf("error = %v, want it to carry the solve error", err)
	}
}

// TestBuildImageBuildKitStreamsStatus pins that each SolveStatus the client
// emits reaches the line callback, so a BuildKit build reports its progress the
// way the classic build does.
func TestBuildImageBuildKitStreamsStatus(t *testing.T) {
	stubBuildKitClient(t, &fakeBuildKitSolver{solve: func(
		_ context.Context, _ *llb.Definition, _ buildkitclient.SolveOpt, ch chan *buildkitclient.SolveStatus,
	) {
		ch <- &buildkitclient.SolveStatus{
			Vertexes: []*buildkitclient.Vertex{{Name: "load build definition from Dockerfile"}},
			Logs:     []*buildkitclient.VertexLog{{Data: []byte("step output\n")}},
		}
	}})

	var lines []string
	if err := buildImageBuildKit(
		context.Background(), &dockertest.MockDockerClient{}, agentOpencode(t),
		testBuildRequest([]byte("FROM scratch\n"), "", "", "", imageIdentity{}),
		"tag", false, func(s string) { lines = append(lines, s) },
	); err != nil {
		t.Fatalf("buildImageBuildKit: %v", err)
	}
	for _, want := range []string{"load build definition from Dockerfile", "step output"} {
		if !slices.Contains(lines, want) {
			t.Errorf("streamed lines = %v, want them to include %q", lines, want)
		}
	}
}

// TestStreamSolveStatusReportsCacheHits pins the BuildKit cache-hit signal: a
// cached vertex emits a CACHED line immediately after its name (BuildKit writes
// no log for a cached step), while a non-cached vertex emits none. This is what
// lets a consumer scanning the streamed output see the hit on a BuildKit engine,
// where the classic builder's "Using cache" line does not exist.
func TestStreamSolveStatusReportsCacheHits(t *testing.T) {
	var lines []string
	streamSolveStatus(&buildkitclient.SolveStatus{
		Vertexes: []*buildkitclient.Vertex{
			{Name: "install node", Cached: true},
			{Name: "install agent"},
		},
	}, func(s string) { lines = append(lines, s) })

	index := slices.Index(lines, "CACHED")
	if index < 0 {
		t.Fatalf("streamed lines = %v, want a CACHED line for the cached vertex", lines)
	}
	if index == 0 || lines[index-1] != "install node" {
		t.Errorf("streamed lines = %v, want CACHED immediately after the cached vertex name", lines)
	}
	cachedLines := 0
	for _, l := range lines {
		if l == "CACHED" {
			cachedLines++
		}
	}
	if cachedLines != 1 {
		t.Errorf("streamed lines = %v, want exactly one CACHED line (only the cached vertex)", lines)
	}
}

// TestBuildImageBuildKitCarriesBuildArgs pins that the classic build args reach
// the BuildKit build as frontend `build-arg:` attributes, so the two builders
// produce the same image for the same render (Review Focus #1). An arg the
// render omits (here DOCKER_VERSION with docker disabled) must not become an
// attribute.
func TestBuildImageBuildKitCarriesBuildArgs(t *testing.T) {
	var captured map[string]string
	stubBuildKitClient(t, &fakeBuildKitSolver{solve: func(
		_ context.Context, _ *llb.Definition, opt buildkitclient.SolveOpt, _ chan *buildkitclient.SolveStatus,
	) {
		captured = opt.FrontendAttrs
	}})

	req := testBuildRequest(
		[]byte("FROM scratch\n"), "1.2.3", "debian:trixie-slim@sha256:abc", "dockerfile-id",
		imageIdentity{ProjectDockerfileHash: "hash", AgentVersion: "1.2.3", Docker: "false", User: "1000:1000"},
	)
	if err := buildImageBuildKit(
		context.Background(), &dockertest.MockDockerClient{}, agentOpencode(t),
		req, "tag", false, func(string) {},
	); err != nil {
		t.Fatalf("buildImageBuildKit: %v", err)
	}

	for name, want := range map[string]string{
		"USER_UID":         strconv.Itoa(os.Getuid()),
		"USER_GID":         strconv.Itoa(os.Getgid()),
		"OPENCODE_VERSION": "1.2.3",
		"BASE_IMAGE":       "debian:trixie-slim@sha256:abc",
		"DOCKERFILE_ID":    "dockerfile-id",
	} {
		if got := captured["build-arg:"+name]; got != want {
			t.Errorf("FrontendAttrs[build-arg:%s] = %q, want %q", name, got, want)
		}
	}
	if _, present := captured["build-arg:DOCKER_VERSION"]; present {
		t.Error("DOCKER_VERSION must stay absent when the request disables docker")
	}
}

// stubBuildKitClient replaces the BuildKit client seam with solver and the
// session seam with an empty attachable list for the test, restoring both when
// it ends, so the unit tests never reach the host's docker config or a real
// builder.
func stubBuildKitClient(t *testing.T, solver buildKitSolver) {
	t.Helper()
	origClient, origSession := newBuildKitClient, newBuildKitSession
	newBuildKitClient = func(context.Context, docker.Client) (buildKitSolver, error) { return solver, nil }
	newBuildKitSession = func() []session.Attachable { return nil }
	t.Cleanup(func() {
		newBuildKitClient = origClient
		newBuildKitSession = origSession
	})
}

// fakeBuildKitSolver is a buildKitSolver whose Solve runs the test-supplied
// callback (writing statuses or capturing the solve options) and then returns
// solveErr. Like the real client, it closes statusChan on every return path, so
// buildImageBuildKit's pump drains and the build does not deadlock. Close
// records that the build released the client.
type fakeBuildKitSolver struct {
	solve    func(context.Context, *llb.Definition, buildkitclient.SolveOpt, chan *buildkitclient.SolveStatus)
	solveErr error
	closed   bool
}

func (f *fakeBuildKitSolver) Solve(
	ctx context.Context,
	def *llb.Definition,
	opt buildkitclient.SolveOpt,
	statusChan chan *buildkitclient.SolveStatus,
) (*buildkitclient.SolveResponse, error) {
	defer close(statusChan)
	if f.solve != nil {
		f.solve(ctx, def, opt, statusChan)
	}
	if f.solveErr != nil {
		return nil, f.solveErr
	}
	return &buildkitclient.SolveResponse{}, nil
}

func (f *fakeBuildKitSolver) Close() error {
	f.closed = true
	return nil
}

// buildKitFSNames walks an fsutil.FS and returns the entry names it serves; a
// removed root serves nothing, which is how the cleanup test observes deletion.
func buildKitFSNames(t *testing.T, f fsutil.FS) []string {
	t.Helper()
	var names []string
	err := f.Walk(context.Background(), ".", func(_ string, entry fs.DirEntry, _ error) error {
		if entry != nil {
			names = append(names, entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fsutil.FS: %v", err)
	}
	return names
}
