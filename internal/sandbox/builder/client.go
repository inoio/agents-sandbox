package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/opencontainers/go-digest"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

const (
	builderReadyTimeout = 30 * time.Second
	builderDialBackoff  = 250 * time.Millisecond
)

// buildkitClient is the host-side seam over the BuildKit client, so the build
// pipeline can be tested without a live builder VM.
type buildkitClient interface {
	ListWorkers(ctx context.Context) error
	Solve(
		ctx context.Context,
		def *llb.Definition,
		opt client.SolveOpt,
		status chan *client.SolveStatus,
	) (*client.SolveResponse, error)
	ResolveImageConfig(
		ctx context.Context,
		ref string,
		opt sourceresolver.Opt,
	) (string, digest.Digest, []byte, error)
	Close() error
}

// newBuildkitClient dials a builder and proves readiness with a ListWorkers
// round trip. It is a variable so tests can substitute a fake.
var newBuildkitClient = realNewBuildkitClient

func realNewBuildkitClient(ctx context.Context, addr string) (buildkitClient, error) {
	c, err := client.New(ctx, addr)
	if err != nil {
		return nil, err
	}
	if _, err := c.ListWorkers(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &realBuildkitClient{client: c}, nil
}

type realBuildkitClient struct {
	client *client.Client
}

func (c *realBuildkitClient) ListWorkers(ctx context.Context) error {
	_, err := c.client.ListWorkers(ctx)
	return err
}

func (c *realBuildkitClient) Solve(
	ctx context.Context,
	def *llb.Definition,
	opt client.SolveOpt,
	status chan *client.SolveStatus,
) (*client.SolveResponse, error) {
	return c.client.Solve(ctx, def, opt, status)
}

// ResolveImageConfig resolves base-image metadata through a BuildKit gateway
// session, the BuildKit-native replacement for a pre-build `docker inspect`.
func (c *realBuildkitClient) ResolveImageConfig(
	ctx context.Context,
	ref string,
	opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	var (
		resolvedRef  string
		resolvedDgst digest.Digest
		resolvedCfg  []byte
	)
	_, err := c.client.Build(
		ctx,
		client.SolveOpt{Session: registryAuthSession(nil)},
		"",
		func(ctx context.Context, gw gwclient.Client) (*gwclient.Result, error) {
			var err error
			resolvedRef, resolvedDgst, resolvedCfg, err = gw.ResolveImageConfig(ctx, ref, opt)
			if err != nil {
				return nil, err
			}
			return gwclient.NewResult(), nil
		},
		nil,
	)
	if err != nil {
		return "", "", nil, err
	}
	return resolvedRef, resolvedDgst, resolvedCfg, nil
}

func (c *realBuildkitClient) Close() error {
	return c.client.Close()
}

// defaultReady waits for the builder VM's published port to serve BuildKit.
func (v *VM) defaultReady(ctx context.Context, hostPort int) error {
	return waitForBuildkit(ctx, fmt.Sprintf("tcp://127.0.0.1:%d", hostPort))
}

// waitForBuildkit retries dialing a buildkitd endpoint until it answers or the
// readiness budget elapses, closing each probed connection.
func waitForBuildkit(ctx context.Context, address string) error {
	deadline := time.Now().Add(builderReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probe, err := newBuildkitClient(ctx, address)
		if err == nil {
			_ = probe.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for buildkitd at %s: %w", address, ctx.Err())
		case <-time.After(builderDialBackoff):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no connection attempt succeeded")
	}
	return fmt.Errorf("wait for buildkitd at %s within %s: %w", address, builderReadyTimeout, lastErr)
}

type BuildRequest struct {
	ContextDir string
	Tag        string
	BaseRef    string
	BuildArgs  map[string]string
	NoCache    bool
	Platform   string
	Progress   func(line string)
}

// VertexTiming is one BuildKit vertex's reported duration. A Dockerfile step can
// be slow while cached (BuildKit materializes its layer), and buildkit-internal
// vertices (`[internal] exporting layers`, cache import, …) are where a solve's
// time goes when the Dockerfile steps are cheap — so both are recorded, tagged by
// Step.
type VertexTiming struct {
	Name     string
	Duration time.Duration
	Cached   bool
	Step     bool
}

type BuildResult struct {
	ArchivePath   string
	Digest        string
	Env           []string
	BaseDigest    string
	Timings       BuildTimings
	VertexTimings []VertexTiming
}

// BuildTimings breaks a build into its phases for verbose diagnostics. A zero
// field means that phase did not run (e.g. a backend that does not report it).
type BuildTimings struct {
	// Ensure is backend/registry bring-up: the builder VM (boot or reuse) and
	// the embedded cache registry.
	Ensure time.Duration
	// BaseResolve connects to BuildKit and resolves the base image digest.
	BaseResolve time.Duration
	// Solve is the whole image solve, including the image export to the archive
	// and the registry cache export (both run in the same solve).
	Solve time.Duration
	// SolveWarmup is Solve's start up to the first progress update: BuildKit's
	// cache import and graph setup, before any Dockerfile step runs.
	SolveWarmup time.Duration
	// Export is the tail of Solve after the last Dockerfile step, i.e.
	// finalizing the image archive, plus archive close.
	Export time.Duration
	// Total is the wall-clock time from Build entry to return.
	Total time.Duration
}

type Builder interface {
	Build(ctx context.Context, req BuildRequest) (BuildResult, error)
	PruneCache(ctx context.Context) error
}

type Client struct {
	backend backend
}

func newClient() *Client {
	return &Client{backend: newBackend()}
}

// ensureCacheRegistry acquires the embedded cache registry endpoint. It is a
// seam so tests can supply an endpoint without spawning the registry daemon.
var ensureCacheRegistry = cacheregistry.Ensure

// registryKeepAlive holds the cache registry daemon's idle timer open for the
// duration of a build. It is a seam so tests can observe the lifecycle without a
// live registry.
var registryKeepAlive = func(ctx context.Context, endpoint cacheregistry.Endpoint) func() {
	return endpoint.KeepAlive(ctx)
}

// builderCacheRef is the deterministic registry cache reference for an image
// identity (tag + platform), keyed by the running registry's address.
func builderCacheRef(endpoint cacheregistry.Endpoint, imageTag, platform string) string {
	return endpoint.CacheRef(imageTag, platform)
}

// cacheRegistryAuth acquires the embedded cache registry and returns its
// endpoint, the image's cache reference, and a BuildKit auth session carrying
// the registry's token.
func (c *Client) cacheRegistryAuth(
	ctx context.Context,
	req BuildRequest,
) (cacheregistry.Endpoint, string, []session.Attachable, error) {
	endpoint, err := ensureCacheRegistry(ctx)
	if err != nil {
		return cacheregistry.Endpoint{}, "", nil, fmt.Errorf("ensure cache registry: %w", err)
	}
	cacheRef := builderCacheRef(endpoint, req.Tag, req.Platform)
	return endpoint, cacheRef, registryAuthSession(cacheRegistryCredentials(endpoint)), nil
}

// cacheRegistryCredentials maps the registry's host:port to its static bearer
// token. The key must match the cache reference authority (RegistryHost), which
// is what BuildKit passes to the session auth provider.
func cacheRegistryCredentials(endpoint cacheregistry.Endpoint) map[string]types.AuthConfig {
	//exhaustruct:ignore // only the static bearer token matters for the embedded registry
	authConfig := types.AuthConfig{RegistryToken: endpoint.Token}
	return map[string]types.AuthConfig{endpoint.RegistryHost(): authConfig}
}

func (c *Client) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	buildStart := time.Now()

	ensureStart := time.Now()
	hostPort, err := c.backend.Ensure(ctx)
	if err != nil {
		return BuildResult{}, fmt.Errorf("ensure builder VM: %w", err)
	}

	// Hold the builder VM's idle timer open for the whole build: the client
	// talks to buildkitd over the forwarded port, which msb does not count as
	// exec activity, so a short idle timeout could otherwise reap it mid-build.
	stopKeepAlive := c.backend.KeepAlive(ctx)
	defer stopKeepAlive()

	cacheEndpoint, cacheRef, authSession, err := c.cacheRegistryAuth(ctx, req)
	if err != nil {
		return BuildResult{}, err
	}
	//exhaustruct:ignore // phases are filled in progressively
	timings := BuildTimings{Ensure: time.Since(ensureStart)}

	// Hold the cache registry's idle timer open for the whole build too: the
	// registry only sees HTTP traffic at cache import and export, so a build
	// that runs longer than the shared idle timeout would let the reaper shut
	// the daemon down mid-build and reset the connection at export time.
	stopRegistryKeepAlive := registryKeepAlive(ctx, cacheEndpoint)
	defer stopRegistryKeepAlive()

	prepareStart := time.Now()
	bc, err := newBuildkitClient(ctx, fmt.Sprintf("tcp://127.0.0.1:%d", hostPort))
	if err != nil {
		return BuildResult{}, fmt.Errorf("connect to builder at %d: %w", hostPort, err)
	}
	defer func() { _ = bc.Close() }()

	baseDigest, err := resolveBaseDigest(ctx, bc, req.BaseRef, req.Platform)
	if err != nil {
		return BuildResult{}, fmt.Errorf("resolve base image %s: %w", req.BaseRef, err)
	}
	timings.BaseResolve = time.Since(prepareStart)

	archive, err := os.CreateTemp("", "agents-sandbox-image-*.tar")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create image archive: %w", err)
	}
	archivePath := archive.Name()
	buildSucceeded := false
	defer func() {
		if !buildSucceeded {
			_ = os.Remove(archivePath)
		}
	}()

	spec := c.backend.Export(req.Tag)
	cacheImports, cacheExports, cacheErr := builderCacheExportOptions(ctx, cacheRef)
	if cacheErr != nil {
		return BuildResult{}, cacheErr
	}

	outcome, err := c.solveAndExport(
		ctx,
		bc,
		req,
		buildAttributes(req, baseDigest),
		spec,
		archive,
		cacheImports,
		cacheExports,
		authSession,
		&timings,
	)
	if err != nil {
		return BuildResult{}, err
	}

	result, err := buildResultFromOutcome(outcome, archivePath, baseDigest)
	if err != nil {
		return BuildResult{}, err
	}
	timings.Total = time.Since(buildStart)
	result.Timings = timings
	result.VertexTimings = outcome.vertexTimings

	buildSucceeded = true
	return result, nil
}

// solveAndExport runs the image solve, which also persists the BuildKit cache
// to the registry in the same pass (BuildKit runs the cache export after the
// build graph completes, alongside the image export). Folding both into one
// solve avoids a second, redundant cache import and graph traversal. The image
// archive is closed here (its close time is part of the export tail), so callers
// must not close it again.
func (c *Client) solveAndExport(
	ctx context.Context,
	bc buildkitClient,
	req BuildRequest,
	attributes map[string]string,
	spec exportSpec,
	archive *os.File,
	cacheImports, cacheExports []client.CacheOptionsEntry,
	authSession []session.Attachable,
	timings *BuildTimings,
) (solveOutcome, error) {
	solveStart := time.Now()
	outcome, solveErr := c.solve(ctx, bc, req, attributes, spec, archive, cacheImports, cacheExports, authSession)
	timings.Solve = time.Since(solveStart)
	timings.SolveWarmup = outcome.firstStatus
	timings.Export = timings.Solve - outcome.lastStatus

	closeStart := time.Now()
	closeErr := archive.Close()
	timings.Export += time.Since(closeStart)
	if solveErr != nil {
		return solveOutcome{}, fmt.Errorf("solve image build: %w", solveErr)
	}
	if closeErr != nil {
		return solveOutcome{}, fmt.Errorf("close image archive: %w", closeErr)
	}
	return outcome, nil
}

// buildAttributes assembles the BuildKit frontend attributes: the platform, the
// caller's build args, the resolved base digest, and the no-cache marker.
func buildAttributes(req BuildRequest, baseDigest string) map[string]string {
	attributes := map[string]string{"platform": req.Platform}
	for key, value := range req.BuildArgs {
		attributes["build-arg:"+key] = value
	}
	attributes["build-arg:BASE_IMAGE"] = baseDigest
	if req.NoCache {
		attributes["no-cache"] = ""
	}
	return attributes
}

// buildResultFromOutcome assembles the BuildResult from a solve's exporter
// response, parsing the image config's Env when BuildKit supplied it.
func buildResultFromOutcome(outcome solveOutcome, archivePath, baseDigest string) (BuildResult, error) {
	var metadata struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"config"`
	}
	if configJSON := outcome.response["containerimage.config"]; configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &metadata); err != nil {
			return BuildResult{}, fmt.Errorf("parse image config: %w", err)
		}
	}
	//exhaustruct:ignore // Timings are attached by Build after the outcome is assembled
	return BuildResult{
		ArchivePath: archivePath,
		Digest:      imageDigest(outcome.response),
		Env:         metadata.Config.Env,
		BaseDigest:  baseDigest,
	}, nil
}

// solveOutcome is the result of one BuildKit solve.
type solveOutcome struct {
	response map[string]string
	steps    int
	// firstStatus is the time from solve start to the first status update:
	// BuildKit's cache import and graph setup, before any Dockerfile step runs.
	firstStatus time.Duration
	// lastStatus is the time from solve start to the last status update. The
	// gap to the solve's return is the image archive export after the build.
	lastStatus time.Duration
	// vertexTimings are the per-vertex durations, longest first.
	vertexTimings []VertexTiming
}

// solve runs one BuildKit solve, forwarding progress. It exports the image when
// imageOut is non-nil and the registry cache when cacheExports is non-empty.
func (c *Client) solve(
	ctx context.Context,
	bc buildkitClient,
	req BuildRequest,
	attributes map[string]string,
	spec exportSpec,
	imageOut io.Writer,
	cacheImports, cacheExports []client.CacheOptionsEntry,
	authSession []session.Attachable,
) (solveOutcome, error) {
	opt := client.SolveOpt{
		Frontend:      "dockerfile.v0",
		LocalDirs:     map[string]string{"context": req.ContextDir, "dockerfile": req.ContextDir},
		FrontendAttrs: attributes,
		CacheImports:  cacheImports,
		CacheExports:  cacheExports,
		Session:       authSession,
	}
	if imageOut != nil {
		opt.Exports = []client.ExportEntry{{
			Type:  spec.Type,
			Attrs: spec.Attrs,
			Output: func(map[string]string) (io.WriteCloser, error) {
				return nopWriteCloser{Writer: imageOut}, nil
			},
		}}
	}

	solveStart := time.Now()
	statusCh := make(chan *client.SolveStatus)
	progressDone := make(chan struct{})
	detector := newBuildStepCacheDetector()
	reporter := newBuildProgressReporter(req.Progress)
	var firstStatus, lastStatus time.Duration
	go func() {
		defer close(progressDone)
		for status := range statusCh {
			elapsed := time.Since(solveStart)
			if firstStatus == 0 {
				firstStatus = elapsed
			}
			lastStatus = elapsed
			detector.observe(status)
			reporter.report(status)
		}
	}()

	resp, err := bc.Solve(ctx, nil, opt, statusCh)
	// Solve owns closing statusCh, which ends the progress loop.
	<-progressDone
	if err != nil {
		return solveOutcome{}, err
	}
	return solveOutcome{
		response:      resp.ExporterResponse,
		steps:         detector.steps(),
		firstStatus:   firstStatus,
		lastStatus:    lastStatus,
		vertexTimings: detector.vertexTimings(),
	}, nil
}

// buildStepCacheDetector records whether any Dockerfile step had to execute
// (a cache miss) by watching completed vertices whose names are step-scoped
// rather than buildkit-internal. BuildKit can emit a transient uncached update
// before a cache lookup resolves, so a step counts as a hit once it is ever
// reported cached. It also records each step's reported duration so a solve can
// be attributed to individual steps.
type buildStepCacheDetector struct {
	cacheState map[string]bool // step name -> ever reported cached
	durations  map[string]time.Duration
}

func newBuildStepCacheDetector() *buildStepCacheDetector {
	return &buildStepCacheDetector{
		cacheState: map[string]bool{},
		durations:  map[string]time.Duration{},
	}
}

func (d *buildStepCacheDetector) observe(status *client.SolveStatus) {
	for _, v := range status.Vertexes {
		if v.Completed == nil {
			continue
		}
		if isDockerfileStep(v.Name) {
			if _, seen := d.cacheState[v.Name]; !seen {
				d.cacheState[v.Name] = false
			}
			if v.Cached {
				d.cacheState[v.Name] = true
			}
		}
		if v.Started != nil {
			if elapsed := v.Completed.Sub(*v.Started); elapsed > d.durations[v.Name] {
				d.durations[v.Name] = elapsed
			}
		}
	}
}

// steps returns the number of distinct Dockerfile steps observed.
func (d *buildStepCacheDetector) steps() int {
	return len(d.cacheState)
}

// vertexTimings returns every observed vertex duration, longest first, tagged as a
// Dockerfile step or a buildkit-internal vertex.
func (d *buildStepCacheDetector) vertexTimings() []VertexTiming {
	timings := make([]VertexTiming, 0, len(d.durations))
	for name, duration := range d.durations {
		timings = append(timings, VertexTiming{
			Name:     name,
			Duration: duration,
			Cached:   d.cacheState[name],
			Step:     isDockerfileStep(name),
		})
	}
	sort.Slice(timings, func(i, j int) bool { return timings[i].Duration > timings[j].Duration })
	return timings
}

// isDockerfileStep reports whether a vertex is a real build instruction rather
// than a buildkit-internal vertex or the FROM source. Its name carries a
// "[<platform> <stage> N/M] " progress prefix (e.g. "[1/2] RUN ..."); the FROM
// vertex always reports uncached because the base image is re-resolved each
// build, so it must not count as a step.
func isDockerfileStep(name string) bool {
	if !strings.HasPrefix(name, "[") || strings.Contains(name, "[internal]") {
		return false
	}
	_, command, ok := strings.Cut(name, "] ")
	if !ok {
		return false
	}
	return !strings.HasPrefix(command, "FROM")
}

// builderCacheExportOptions returns the BuildKit registry cache import/export
// entries for the embedded registry's cache reference. mode=max keeps
// intermediate layers so a source change can still warm-hit its unchanged prefix.
func builderCacheExportOptions(_ context.Context, cacheRef string) (
	[]client.CacheOptionsEntry,
	[]client.CacheOptionsEntry,
	error,
) {
	if cacheRef == "" {
		return nil, nil, errors.New("cache ref is empty")
	}
	// The embedded registry serves plain HTTP on a non-loopback gateway address,
	// so BuildKit's registry remotecache must be told the endpoint is insecure;
	// otherwise it defaults to HTTPS and every import/export fails.
	imports := []client.CacheOptionsEntry{
		{Type: "registry", Attrs: map[string]string{"ref": cacheRef, "registry.insecure": buildkitAttrTrue}},
	}
	exportAttrs := map[string]string{
		"ref": cacheRef, "mode": "max", "registry.insecure": buildkitAttrTrue,
	}
	applyCompressionAttrs(exportAttrs)
	exports := []client.CacheOptionsEntry{{Type: "registry", Attrs: exportAttrs}}
	return imports, exports, nil
}

// imageDigest returns the image's config digest, which is what microsandbox's
// ImageConfig.Digest reports on reuse, so both paths compare equal. It falls
// back to the manifest digest when BuildKit omits the config digest.
func imageDigest(exporterResponse map[string]string) string {
	if digest := exporterResponse["containerimage.config.digest"]; digest != "" {
		return digest
	}
	return exporterResponse["containerimage.digest"]
}

// PruneCache reclaims stale BuildKit cache through the active backend.
func (c *Client) PruneCache(ctx context.Context) error {
	return c.backend.PruneCache(ctx)
}
