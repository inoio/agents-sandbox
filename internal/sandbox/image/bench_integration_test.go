//go:build integration

package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// TestBenchBuildBackends is a THROWAWAY SPIKE (not production code, not part of
// the normal test suite) that compares the wall-clock cost of building the same
// Dockerfile across three backends:
//
//   - msb    - production: buildkitd inside a microsandbox (libkrun) VM, using
//     the `native` snapshotter.
//   - docker - test harness: buildkitd in a `docker run moby/buildkit`
//     container, also using the `native` snapshotter.
//   - host   - `docker build` through the host Docker daemon (overlayfs, or vfs
//     on the dev VM).
//
// The motivating questions: (1) the msb builder feels much slower than a host
// build; (2) msb seems to get slower over a run. docker-vs-msb isolates the
// microVM overhead, because both drive the *same* host-side BuildKit client,
// the *same* rendered Dockerfile, and the *same* native snapshotter; only the
// buildkitd host differs. host-vs-docker isolates the snapshotter/daemon
// environment.
//
// Workloads (BENCH_WORKLOADS, default "small,medium,runner"):
//
//   - small  - debian base + one trivial RUN.
//   - medium - debian base + install git/ca-certificates (a real, package-heavy
//     layer that stresses snapshotter extraction).
//   - runner - the real managed opencode runner image (agent install + stages).
//
// Trend: warm/rebuild phases repeat BENCH_RUNS times and every iteration is
// logged, so a monotonic slowdown across the sequence is visible (summary lines
// report first/last/delta per phase). This is the primary signal for "msb gets
// slower over time".
//
// Phases measured per backend/workload:
//
//   - cold    - cache wiped and a fresh buildkitd process, full no-cache build
//     (includes buildkitd boot/pull + all layers).
//   - warm    - same process, cache reused (cache-hit build), repeated.
//   - rebuild - same process, no-cache build (all steps re-run), repeated.
//   - boot    - fresh buildkitd process but persistent cache, so
//     boot ~= boot - median(warm). Builder backends only.
//
// Every measurement is emitted as a parseable line:
//
//	BENCH workload=<w> backend=<b> phase=<p> seconds=<f>
//
// Run it on a KVM host with Docker, an installed msb runtime, and network
// egress (the msb tier cannot run in the agents-sandbox dev VM):
//
//	AGENTS_SANDBOX_INTEGRATION=1 CGO_ENABLED=1 \
//	  go test -tags integration -count=1 -timeout 120m -v \
//	  -run TestBenchBuildBackends ./internal/sandbox/image/
//
// Tunables (env):
//
//	BENCH_RUNS        - repetitions per warm/rebuild phase (default 5)
//	BENCH_BACKENDS    - comma list from {msb,msb-bypass,docker,host,moby} (default msb,docker,host)
//	BENCH_WORKLOADS   - comma list from {small,medium,net,runner} (default small,medium,runner)
//	BENCH_NET_URL     - download target for the "net" workload (default a 50MiB Cloudflare sample)
//	BENCH_TLS_BYPASS  - "all" (disable interception for every host) or a comma list of domain patterns
//
// The "msb-tuned" backend applies any of these builder knobs (unset = production
// default), so one backend A/Bs them:
//
//	BENCH_BUILDER_MEMORY_MIB       - initial builder-VM memory (default 4096; clamped to host RAM)
//	BENCH_BUILDER_CACHE            - cache backing: volume (default) | bind | tmpfs
//	BENCH_BUILDER_CACHE_SIZE_MIB   - tmpfs cache size (default 16384)
//	BENCH_BUILDER_SNAPSHOTTER      - buildkitd OCI snapshotter (default native)
//	BENCH_BUILDER_IDLE_TIMEOUT     - builder-VM idle timeout, e.g. 30s (default 5s); raise to keep the VM (and a tmpfs cache) alive between builds
//	BENCH_TLS_BYPASS              - TLS interception override (see B4)
//
// B4 (egress). The "net" workload isolates network cost to a single
// `curl -o /dev/null` RUN. Comparing msb vs docker/host isolates the msb egress
// path (TLS inspection + smoltcp relay); BENCH_TLS_BYPASS=all on "msb-tuned"
// isolates interception from the relay. docker/host never intercept.
func TestBenchBuildBackends(t *testing.T) {
	if os.Getenv(integrationEnvVar) == "" {
		t.Skipf("benchmark: set %s=1 on a KVM host with msb and network access", integrationEnvVar)
	}
	useRealHostRuntime(t)
	ctx := context.Background()

	workloads := benchWorkloads(t)
	runs := benchIntEnv("BENCH_RUNS", 5)

	for _, backend := range benchBackends(t) {
		for _, workload := range workloads {
			t.Run(backend+"/"+workload.name, func(t *testing.T) {
				switch backend {
				case "host":
					benchHostBackend(t, ctx, workload, runs)
				case "moby":
					benchMobyBackend(t, ctx, workload, runs)
				default:
					benchBuilderBackend(t, ctx, backend, workload, runs)
				}
			})
		}
	}
}

const (
	benchCacheVolumeMsb        = "agents-sandbox-buildkit-cache"
	benchBuilderContainer      = "agents-sandbox-buildkit-harness"
	hostBenchTag               = "agents-sandbox/bench:opencode"
	benchBaseRef               = "debian:trixie-slim"
	benchNetURLDefault         = "https://speed.cloudflare.com/__down?bytes=52428800"
	benchNetImage              = "curlimages/curl:8.11.1"
	builderBackendEnv          = "AGENTS_SANDBOX_BUILDER"
	builderTLSBypassEnvVar     = "AGENTS_SANDBOX_BUILDER_TLS_BYPASS"
	builderMemoryEnvVar        = "AGENTS_SANDBOX_BUILDER_MEMORY_MIB"
	builderCacheEnvVar         = "AGENTS_SANDBOX_BUILDER_CACHE"
	builderCacheSizeEnvVar     = "AGENTS_SANDBOX_BUILDER_CACHE_SIZE_MIB"
	builderSnapEnvVar          = "AGENTS_SANDBOX_BUILDER_SNAPSHOTTER"
	builderIdleEnvVar          = "AGENTS_SANDBOX_BUILDER_IDLE_TIMEOUT"
	builderCacheRefEnvVar      = "AGENTS_SANDBOX_BUILDER_CACHE_REF"
	builderCacheInsecureEnvVar = "AGENTS_SANDBOX_BUILDER_CACHE_INSECURE"
)

// builderTuningEnv maps a benchmark knob onto the builder env var it sets.
var builderTuningEnv = map[string]string{
	"BENCH_TLS_BYPASS":             builderTLSBypassEnvVar,
	"BENCH_BUILDER_MEMORY_MIB":     builderMemoryEnvVar,
	"BENCH_BUILDER_CACHE":          builderCacheEnvVar,
	"BENCH_BUILDER_CACHE_SIZE_MIB": builderCacheSizeEnvVar,
	"BENCH_BUILDER_SNAPSHOTTER":    builderSnapEnvVar,
	"BENCH_BUILDER_IDLE_TIMEOUT":   builderIdleEnvVar,
	"BENCH_BUILDER_CACHE_REF":      builderCacheRefEnvVar,
	"BENCH_BUILDER_CACHE_INSECURE": builderCacheInsecureEnvVar,
}

// smallDockerfile and mediumDockerfile are synthetic, deterministic workloads
// that share the runner image's base so the base pull is constant across them.
// medium installs packages, producing a file-heavy layer that stresses each
// backend's snapshotter/layer extraction.
var (
	smallDockerfile  = []byte("FROM " + benchBaseRef + "\nRUN echo small > /small-marker\n")
	mediumDockerfile = []byte("FROM " + benchBaseRef + "\n" +
		"RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates " +
		"&& rm -rf /var/lib/apt/lists/*\n")
)

// benchWorkload is one Dockerfile to build across every backend.
type benchWorkload struct {
	name    string
	tag     string
	content []byte
	// withPathAsset additionally writes the profile.d path-merge asset into the
	// context (the runner Dockerfile COPYs it).
	withPathAsset bool
	buildArgs     map[string]string
	baseRef       string
}

// contextDir creates a fresh build context for the workload.
func (w benchWorkload) contextDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if w.withPathAsset {
		if err := writeDockerfileContext(dir, w.content); err != nil {
			t.Fatalf("write runner build context: %v", err)
		}
		return dir
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), w.content, 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return dir
}

// benchWorkloads assembles the requested workloads. The runner workload needs
// the real renderer and agent version, resolved once here (outside any timed
// region).
func benchWorkloads(t *testing.T) []benchWorkload {
	t.Helper()
	requested := os.Getenv("BENCH_WORKLOADS")
	if requested == "" {
		requested = "small,medium,runner"
	}
	available := map[string]func() benchWorkload{
		"small": func() benchWorkload {
			return benchWorkload{name: "small", tag: "agents-sandbox/bench:small",
				content: smallDockerfile, baseRef: benchBaseRef}
		},
		"medium": func() benchWorkload {
			return benchWorkload{name: "medium", tag: "agents-sandbox/bench:medium",
				content: mediumDockerfile, baseRef: benchBaseRef}
		},
		"net":    benchNetWorkload,
		"runner": func() benchWorkload { return benchRunnerWorkload(t) },
	}

	var workloads []benchWorkload
	for name := range strings.SplitSeq(requested, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		build, ok := available[name]
		if !ok {
			t.Fatalf("unknown workload %q (want small,medium,net,runner)", name)
		}
		workloads = append(workloads, build())
	}
	return workloads
}

// benchNetWorkload builds a single-download Dockerfile so a no-cache rebuild
// measures only network time. curlimages/curl carries curl and a CA bundle, so
// no package install (and no extra egress) is needed.
func benchNetWorkload() benchWorkload {
	url := os.Getenv("BENCH_NET_URL")
	if url == "" {
		url = benchNetURLDefault
	}
	dockerfile := fmt.Sprintf("FROM %s\nRUN curl -fsSL -o /dev/null %q\n", benchNetImage, url)
	return benchWorkload{name: "net", tag: "agents-sandbox/bench:net",
		content: []byte(dockerfile), baseRef: benchNetImage}
}

// benchRunnerWorkload renders the real managed opencode runner image context.
func benchRunnerWorkload(t *testing.T) benchWorkload {
	t.Helper()
	a := agentOpencode(t)
	version := benchAgentVersion(t, a)
	rendered := renderBytesVersion(t, a, version, nil, false)
	return benchWorkload{
		name:          "runner",
		tag:           hostBenchTag,
		content:       rendered,
		withPathAsset: true,
		baseRef:       baseImageRef(rendered),
		buildArgs: buildArgStrings(userBuildArgs(
			os.Getuid(), os.Getgid(), a.ImageSpec(), version,
			computeDockerfileID(rendered, version), false, computeImageIdentity(nil, version, false),
		)),
	}
}

// benchResult collects the phase timings for one backend/workload.
type benchResult struct {
	workload string
	backend  string
	cold     time.Duration
	warm     []time.Duration
	rebuild  []time.Duration
	boot     time.Duration
	hasBoot  bool
}

// benchBuilderBackend measures a builder-package backend (msb or docker) by
// selecting it through AGENTS_SANDBOX_BUILDER and driving builder.Get().Build.
func benchBuilderBackend(t *testing.T, ctx context.Context, backend string, workload benchWorkload, runs int) {
	t.Helper()
	t.Setenv(builderBackendEnv, builderBackendName(backend))
	applyBuilderTuning(t, backend)

	request := builder.BuildRequest{
		ContextDir: workload.contextDir(t),
		Tag:        workload.tag,
		BaseRef:    workload.baseRef,
		BuildArgs:  workload.buildArgs,
		Platform:   builder.HostPlatform(),
	}

	wipeBuilderCache(t, ctx, backend)
	result := benchResult{workload: workload.name, backend: backend}
	result.cold = mustBuild(t, ctx, backend, workload.name, request, true, "cold")

	for i := range runs {
		result.warm = append(result.warm, mustBuild(
			t, ctx, backend, workload.name, request, false, fmt.Sprintf("warm-%d", i+1)))
	}
	for i := range runs {
		result.rebuild = append(result.rebuild, mustBuild(
			t, ctx, backend, workload.name, request, true, fmt.Sprintf("rebuild-%d", i+1)))
	}

	resetBuilderProcess(t, ctx, backend)
	result.boot = mustBuild(t, ctx, backend, workload.name, request, false, "boot")
	result.hasBoot = true

	reportBench(t, result)
}

// benchHostBackend measures `docker build` through the host daemon. The host
// uses dockerd's own BuildKit cache, independent of the harness builder.
func benchHostBackend(t *testing.T, ctx context.Context, workload benchWorkload, runs int) {
	t.Helper()
	contextDir := workload.contextDir(t)
	result := benchResult{workload: workload.name, backend: "host"}
	result.cold = mustDockerBuild(t, ctx, workload, hostBuildArgs(contextDir, workload, true), "cold")
	for i := range runs {
		result.warm = append(result.warm, mustDockerBuild(
			t, ctx, workload, hostBuildArgs(contextDir, workload, false), fmt.Sprintf("warm-%d", i+1)))
	}
	for i := range runs {
		result.rebuild = append(result.rebuild, mustDockerBuild(
			t, ctx, workload, hostBuildArgs(contextDir, workload, true), fmt.Sprintf("rebuild-%d", i+1)))
	}
	reportBench(t, result)
}

// hostBuildArgs assembles a `docker build` argv for the workload's context.
func hostBuildArgs(contextDir string, workload benchWorkload, noCache bool) []string {
	args := []string{"build", "--progress=plain"}
	if noCache {
		args = append(args, "--no-cache")
	}
	for _, key := range sortedKeys(workload.buildArgs) {
		args = append(args, "--build-arg", key+"="+workload.buildArgs[key])
	}
	args = append(args, "--build-arg", "BASE_IMAGE="+workload.baseRef)
	return append(args, "-t", workload.tag, contextDir)
}

// mustBuild runs one timed builder-package build, logging the sample and
// removing the produced archive. It fails the test on error.
func mustBuild(
	t *testing.T,
	ctx context.Context,
	backend, workload string,
	request builder.BuildRequest,
	noCache bool,
	phase string,
) time.Duration {
	t.Helper()
	request.NoCache = noCache
	start := time.Now()
	result, err := builder.Get().Build(ctx, request)
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("build [%s] noCache=%v: %v", phase, noCache, err)
	}
	if result.ArchivePath != "" {
		_ = os.Remove(result.ArchivePath)
	}
	logBench(t, workload, backend, phase, duration)
	return duration
}

// mustDockerBuild runs one timed `docker build`, logging the sample.
func mustDockerBuild(
	t *testing.T,
	ctx context.Context,
	workload benchWorkload,
	args []string,
	phase string,
) time.Duration {
	t.Helper()
	start := time.Now()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	duration := time.Since(start)
	if err != nil {
		t.Fatalf("docker build [%s]: %v\n%s", phase, err, strings.TrimSpace(string(output)))
	}
	logBench(t, workload.name, "host", phase, duration)
	return duration
}

// wipeBuilderCache removes the builder process and its cache so the next build
// is genuinely cold.
func wipeBuilderCache(t *testing.T, ctx context.Context, backend string) {
	t.Helper()
	removeBuilderSandboxes(t, ctx)
	if err := msbRemoveVolume(ctx, benchCacheVolumeMsb); err != nil {
		t.Logf("remove msb cache volume (continuing, may be absent): %v", err)
	}
	// A bind cache lives on the host, outside the sandbox/volume teardown.
	if os.Getenv(builderCacheEnvVar) == "bind" {
		bindPath := filepath.Join(configpaths.Get().UserStateDir(), "buildkit-cache")
		if err := os.RemoveAll(bindPath); err != nil {
			t.Fatalf("remove bind cache dir %s: %v", bindPath, err)
		}
	}
	// The tmpfs-export cache lives in a host dir, outside the sandbox teardown.
	if os.Getenv(builderCacheEnvVar) == "tmpfs-export" {
		exportPath := filepath.Join(configpaths.Get().UserStateDir(), "buildkit-cache-export")
		if err := os.RemoveAll(exportPath); err != nil {
			t.Fatalf("remove export cache dir %s: %v", exportPath, err)
		}
	}
}

// resetBuilderProcess removes only the builder process, leaving the persistent
// cache volume in place so a rebuild can warm-hit.
func resetBuilderProcess(t *testing.T, ctx context.Context, backend string) {
	t.Helper()
	if backend == "docker" {
		// Remove the container but keep the harness cache volume.
		_, _ = exec.CommandContext(ctx, "docker", "rm", "-f", benchBuilderContainer).CombinedOutput()
		return
	}
	removeBuilderSandboxes(t, ctx)
}

// reportBench logs a summary plus first/last/delta per repeated phase, so a
// slowdown across the sequence is visible.
func reportBench(t *testing.T, result benchResult) {
	t.Helper()
	t.Logf("summary workload=%s backend=%s cold=%.3fs warm_median=%.3fs rebuild_median=%.3fs",
		result.workload, result.backend,
		result.cold.Seconds(), median(result.warm).Seconds(), median(result.rebuild).Seconds())
	logTrend(t, result, "warm", result.warm)
	logTrend(t, result, "rebuild", result.rebuild)
	if result.hasBoot {
		bootOverhead := result.boot - median(result.warm)
		t.Logf("summary workload=%s backend=%s boot_overhead(approx)=%.3fs",
			result.workload, result.backend, bootOverhead.Seconds())
	}
}

// logTrend reports the first, last, and delta of a repeated phase so a
// monotonic drift across the run stands out.
func logTrend(t *testing.T, result benchResult, phase string, samples []time.Duration) {
	t.Helper()
	if len(samples) < 2 {
		return
	}
	first, last := samples[0], samples[len(samples)-1]
	t.Logf("trend workload=%s backend=%s phase=%s first=%.3fs last=%.3fs delta=%+.3fs",
		result.workload, result.backend, phase, first.Seconds(), last.Seconds(), (last - first).Seconds())
}

func logBench(t *testing.T, workload, backend, phase string, duration time.Duration) {
	t.Helper()
	t.Logf("BENCH workload=%s backend=%s phase=%s seconds=%.3f",
		workload, backend, phase, duration.Seconds())
}

// benchAgentVersion resolves the agent version once, outside any timed region,
// so every backend builds the identical workload and network resolution does
// not skew the comparison.
func benchAgentVersion(t *testing.T, a agent.Agent) string {
	t.Helper()
	version, err := resolveAgentVersion(context.Background(), a, "")
	if err != nil {
		t.Fatalf("resolve agent version: %v", err)
	}
	return version
}

// msbRemoveVolume removes the named microsandbox volume, tolerating absence.
func msbRemoveVolume(ctx context.Context, name string) error {
	err := msb.Get().RemoveVolume(ctx, name)
	if err != nil && msb.IsNotFound(err) {
		return nil
	}
	return err
}

func median(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// benchBackends returns the requested backend set, dropping docker/host when no
// Docker CLI is available.
func benchBackends(t *testing.T) []string {
	t.Helper()
	requested := os.Getenv("BENCH_BACKENDS")
	if requested == "" {
		requested = "msb,docker,host"
	}
	_, dockerOnPath := exec.LookPath("docker")
	var backends []string
	for backend := range strings.SplitSeq(requested, ",") {
		backend = strings.TrimSpace(backend)
		if backend == "" {
			continue
		}
		if (backend == "docker" || backend == "host" || backend == "moby") && dockerOnPath != nil {
			t.Logf("skipping backend %q: docker not on PATH", backend)
			continue
		}
		backends = append(backends, backend)
	}
	return backends
}

// builderBackendName maps a benchmark backend name onto the AGENTS_SANDBOX_BUILDER
// value; the msb-tuned variants all run the msb backend with extra env knobs.
func builderBackendName(backend string) string {
	if isMsbTunedBackend(backend) {
		return "msb"
	}
	return backend
}

// isMsbTunedBackend reports whether backend runs msb with BENCH_* knobs applied.
func isMsbTunedBackend(backend string) bool {
	return backend == "msb-tuned" || backend == "msb-bypass"
}

// applyBuilderTuning copies any set BENCH_* builder knobs into their
// AGENTS_SANDBOX_BUILDER_* counterparts for an msb-tuned backend.
func applyBuilderTuning(t *testing.T, backend string) {
	t.Helper()
	if !isMsbTunedBackend(backend) {
		return
	}
	applied := 0
	for benchName, builderName := range builderTuningEnv {
		value := strings.TrimSpace(os.Getenv(benchName))
		if value == "" {
			continue
		}
		// A registry cache is not wiped by wipeBuilderCache (it lives on the
		// host); a unique ref per subtest guarantees the cold phase starts
		// with an empty cache while warm/rebuild/boot still share it.
		if benchName == "BENCH_BUILDER_CACHE_REF" {
			value = value + "-bench-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		t.Setenv(builderName, value)
		applied++
	}
	if backend == "msb-bypass" && os.Getenv("BENCH_TLS_BYPASS") == "" {
		t.Fatalf("backend msb-bypass requires BENCH_TLS_BYPASS")
	}
	if applied == 0 {
		t.Logf("backend %s: no BENCH_* knobs set; behaves like msb", backend)
	}
}

func benchIntEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}
