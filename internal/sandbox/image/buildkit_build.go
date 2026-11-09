package image

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/docker/cli/cli/config"
	buildkitclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/tonistiigi/fsutil"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// buildKitSolver is the slice of the BuildKit client the build uses, so a unit
// test can substitute a fake for the real client without a builder. Solve closes
// statusChan on every return path (the real client defers the close), so an
// implementation must do the same or the status pump never drains.
type buildKitSolver interface {
	Solve(
		ctx context.Context,
		def *llb.Definition,
		opt buildkitclient.SolveOpt,
		statusChan chan *buildkitclient.SolveStatus,
	) (*buildkitclient.SolveResponse, error)
	Close() error
}

// newBuildKitClient constructs a BuildKit client that reaches the daemon's
// embedded builder over the Docker API hijack endpoints. It is a package var so
// a unit test can substitute a fake client.
var newBuildKitClient = func(ctx context.Context, dclient docker.Client) (buildKitSolver, error) {
	return buildkitclient.New(
		ctx,
		"",
		buildkitclient.WithContextDialer(buildKitControlDialer(dclient)),
		buildkitclient.WithSessionDialer(buildKitSessionDialer(dclient)),
	)
}

// buildKitContextFS opens a staged context directory as the fsutil filesystem
// the solve's local mounts serve. It is a package var so a unit test can drive
// the open-failure branch, which a directory this build just created never
// takes.
var buildKitContextFS = fsutil.NewFS

// buildKitContextChmod applies the explicit mode to a written build-context
// file, because os.WriteFile creates the file masked by the process umask. It is
// a package var so a unit test can drive the chmod-failure branch, which the
// mode of a file in a directory this build just created never produces.
var buildKitContextChmod = os.Chmod

// newBuildKitSession returns the session attachables a solve carries: the
// registry-auth provider built from the user's docker credential configuration,
// so a private base image resolves on this path as it does on the classic path.
// The build context needs no attachable of its own — Solve registers the
// filesync provider for SolveOpt.LocalMounts automatically.
//
// It is a package var so a unit test can substitute an empty list and never read
// the host's docker config.
var newBuildKitSession = func() []session.Attachable {
	dockerConfig := config.LoadDefaultConfigFile(os.Stderr)
	//nolint:exhaustruct_v5 // only the credential source is controlled here; TLS/expiry keep their defaults
	return []session.Attachable{authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: authprovider.LoadAuthConfig(dockerConfig),
	})}
}

// buildKitControlDialer returns the context dialer the BuildKit client uses for
// its control connection: the daemon's /grpc endpoint over h2c.
func buildKitControlDialer(dclient docker.Client) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return dclient.DialHijack(ctx, "/grpc", "h2c", nil)
	}
}

// buildKitSessionDialer returns the session dialer the BuildKit client uses for
// its session connection: the daemon's /session endpoint, forwarding the
// protocol and metadata the session requests.
func buildKitSessionDialer(dclient docker.Client) func(context.Context, string, map[string][]string) (net.Conn, error) {
	return func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		return dclient.DialHijack(ctx, "/session", proto, meta)
	}
}

// buildImageBuildKit builds the image through the engine's embedded BuildKit
// builder, reached over the Docker API hijack endpoints (/grpc and /session),
// with the dockerfile.v0 frontend and the moby exporter, so the produced image
// is indistinguishable from the classic path's. The classic build args travel
// as frontend build-arg attributes, and the daemon's streamed solve status is
// forwarded to line. A solve failure is returned as-is — the launcher never
// retries with the classic builder.
func buildImageBuildKit(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	req buildRequest,
	tag string,
	noCache bool,
	line func(string),
) error {
	solver, err := newBuildKitClient(ctx, dclient)
	if err != nil {
		return buildFailureError(err)
	}
	defer func() { _ = solver.Close() }()

	opt, cleanup, err := buildKitSolveOpt(req, tag, noCache)
	if err != nil {
		return buildFailureError(err)
	}
	defer cleanup()

	for name, value := range userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), req) {
		opt.FrontendAttrs["build-arg:"+name] = *value
	}
	opt.Session = newBuildKitSession()

	statusCh := make(chan *buildkitclient.SolveStatus)
	var pump sync.WaitGroup
	pump.Go(func() {
		for status := range statusCh {
			streamSolveStatus(status, line)
		}
	})

	// Client.Solve closes statusCh itself (a deferred close on every return
	// path), so the build must not close it again; it only waits for the pump to
	// drain the closed channel before reporting the outcome.
	_, solveErr := solver.Solve(ctx, nil, opt, statusCh)
	pump.Wait()
	if solveErr != nil {
		return buildFailureError(solveErr)
	}
	return nil
}

// streamSolveStatus forwards each solve status's vertex names and log data to
// line, so a BuildKit build reports its progress the way the classic build does.
// A cached vertex emits no log, so its cache hit is surfaced as a CACHED line
// after the vertex name — the BuildKit counterpart of the classic "Using cache".
func streamSolveStatus(status *buildkitclient.SolveStatus, line func(string)) {
	for _, vertex := range status.Vertexes {
		if vertex.Name != "" {
			line(vertex.Name)
			// A cached step produces no log of its own, so the hit is reported
			// from the vertex flag; this mirrors the classic builder's
			// "Using cache" and lets a consumer scanning the streamed output see
			// the hit on a BuildKit engine.
			if vertex.Cached {
				line("CACHED")
			}
		}
	}
	for _, log := range status.Logs {
		if data := strings.TrimRight(string(log.Data), "\n"); data != "" {
			line(data)
		}
	}
}

// buildKitSolveOpt assembles the solve options for one BuildKit build: the
// rendered Dockerfile and its context files are written to a fresh temporary
// directory and served to the daemon under both local source names the
// dockerfile.v0 frontend reads ("context" and "dockerfile"), the moby exporter
// tags the produced image, noCache bypasses the build cache through the
// frontend's no-cache attribute, and the configured cache specs become the
// solve's cache import/export entries. The returned cleanup removes the
// temporary directory.
func buildKitSolveOpt(req buildRequest, tag string, noCache bool) (buildkitclient.SolveOpt, func(), error) {
	dir, err := os.MkdirTemp("", "agents-sandbox-buildkit-*")
	if err != nil {
		return buildkitclient.SolveOpt{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err = writeBuildKitContext(dir, req.plan.rendered); err != nil {
		cleanup()
		return buildkitclient.SolveOpt{}, nil, err
	}
	contextFS, err := buildKitContextFS(dir)
	if err != nil {
		cleanup()
		return buildkitclient.SolveOpt{}, nil, err
	}
	frontendAttrs := map[string]string{"filename": "Dockerfile"}
	if noCache {
		frontendAttrs["no-cache"] = ""
	}
	//nolint:exhaustruct_v5 // the solve carries only the fields this builder controls; the rest stay zero
	return buildkitclient.SolveOpt{
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		LocalMounts: map[string]fsutil.FS{
			"context":    contextFS,
			"dockerfile": contextFS,
		},
		//nolint:exhaustruct_v5 // the moby exporter needs only Type and Attrs
		Exports: []buildkitclient.ExportEntry{
			{Type: "moby", Attrs: map[string]string{"name": tag}},
		},
		CacheImports: buildKitCacheEntries(req.cacheFrom),
		CacheExports: buildKitCacheEntries(req.cacheTo),
	}, cleanup, nil
}

// writeBuildKitContext writes the synthetic build context — the rendered
// Dockerfile followed by the build-context files — into dir as regular files
// with the same modes the classic path gives the tar entries, so the BuildKit
// filesync and the classic tar serve byte-identical sources. The modes are
// applied with an explicit chmod after each write, because WriteFile creates
// the file masked by the process umask.
func writeBuildKitContext(dir string, dockerfile []byte) error {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, dockerfileMode); err != nil {
		return fmt.Errorf("write build context Dockerfile: %w", err)
	}
	if err := buildKitContextChmod(dockerfilePath, dockerfileMode); err != nil {
		return fmt.Errorf("chmod build context Dockerfile: %w", err)
	}
	for _, file := range buildContextFiles() {
		path := filepath.Join(dir, file.name)
		mode := os.FileMode(file.mode) //nolint:gosec // the context modes are constants (0644/0755)
		if err := os.WriteFile(path, file.content, mode); err != nil {
			return fmt.Errorf("write build context %s: %w", file.name, err)
		}
		if err := buildKitContextChmod(path, mode); err != nil {
			return fmt.Errorf("chmod build context %s: %w", file.name, err)
		}
	}
	return nil
}

// buildKitCacheEntries parses the configured cache specs into the cache
// import/export entries a solve carries; an empty input yields no entries.
func buildKitCacheEntries(specs []string) []buildkitclient.CacheOptionsEntry {
	entries := make([]buildkitclient.CacheOptionsEntry, 0, len(specs))
	for _, spec := range specs {
		entries = append(entries, parseBuildKitCacheSpec(spec))
	}
	return entries
}

// parseBuildKitCacheSpec parses one BuildKit cache spec of the form
// "type=gha,scope=x,mode=max" into the entry a SolveOpt carries. Only the
// shape is parsed — the type and attribute values pass through to BuildKit
// uninterpreted, so an invalid backend or attribute is the daemon's error to
// report. A gha spec additionally gains the Actions cache credentials from the
// environment; see fillGitHubActionsCacheAttrs.
func parseBuildKitCacheSpec(spec string) buildkitclient.CacheOptionsEntry {
	attrs := map[string]string{}
	entryType := ""
	for i, part := range strings.Split(spec, ",") {
		key, value, hasValue := strings.Cut(part, "=")
		if !hasValue {
			continue
		}
		if i == 0 && key == "type" {
			entryType = value
			continue
		}
		attrs[key] = value
	}
	if entryType == "gha" {
		fillGitHubActionsCacheAttrs(attrs)
	}
	return buildkitclient.CacheOptionsEntry{Type: entryType, Attrs: attrs}
}

// fillGitHubActionsCacheAttrs copies the GitHub Actions cache-service
// credentials onto a gha cache entry's attributes. BuildKit's gha backend reads
// the cache url and token from the entry attributes only — its documented
// ACTIONS_* defaults are applied by buildx on the client, not by BuildKit — and
// the embedded builder runs in the engine daemon, which never sees the step's
// environment. So the client carries them, the way buildx does. An attribute
// set explicitly in the spec is left untouched.
func fillGitHubActionsCacheAttrs(attrs map[string]string) {
	fromEnv := func(attr, envVar string) {
		if _, set := attrs[attr]; set {
			return
		}
		if value := os.Getenv(envVar); value != "" {
			attrs[attr] = value
		}
	}
	fromEnv("url", "ACTIONS_CACHE_URL")
	fromEnv("url_v2", "ACTIONS_RESULTS_URL")
	fromEnv("token", "ACTIONS_RUNTIME_TOKEN")
}
