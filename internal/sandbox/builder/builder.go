package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

const (
	buildkitVersion         = "v0.25.2"
	builderImage            = "moby/buildkit:" + buildkitVersion
	builderGuestPort uint16 = 1234
	cacheVolumeName         = "agents-sandbox-buildkit-cache"
	cacheMountPath          = "/var/lib/buildkit"
	// builderLogPath lives on the cache volume so buildkitd logs survive the
	// builder VM's short idle reaping (and thus a crashed/lost build). Logs are
	// appended across boots and are intended for debugging only.
	builderLogPath      = cacheMountPath + "/buildkitd.log"
	builderHostPortBase = 11234
	// builderDefaultIdleTimeout is short so a crashed launcher cannot leak the
	// shared builder VM; Client.Build keeps it alive via KeepAlive while a build
	// runs. Overridable by a spike knob to test cross-invocation VM/cache
	// persistence.
	builderDefaultIdleTimeout = 5 * time.Second

	// Temporary build-performance spike knobs (see internal/sandbox/image
	// bench_integration_test.go). All default to the current production values.
	builderTLSBypassEnv   = "AGENTS_SANDBOX_BUILDER_TLS_BYPASS"
	builderMemoryEnv      = "AGENTS_SANDBOX_BUILDER_MEMORY_MIB"
	builderCacheModeEnv   = "AGENTS_SANDBOX_BUILDER_CACHE"
	builderCacheSizeEnv   = "AGENTS_SANDBOX_BUILDER_CACHE_SIZE_MIB"
	builderSnapshotterEnv = "AGENTS_SANDBOX_BUILDER_SNAPSHOTTER"
	builderIdleTimeoutEnv = "AGENTS_SANDBOX_BUILDER_IDLE_TIMEOUT"
	builderTraceEnv       = "AGENTS_SANDBOX_BUILDER_TRACE"
	builderCacheRefEnv    = "AGENTS_SANDBOX_BUILDER_CACHE_REF"
	builderCacheInsecure  = "AGENTS_SANDBOX_BUILDER_CACHE_INSECURE"

	cacheModeVolume      = "volume"
	cacheModeBind        = "bind"
	cacheModeTmpfs       = "tmpfs"
	cacheModeTmpfsExport = "tmpfs-export"
	cacheModeRegistry    = "registry"
)

// builderTrace prints a diagnostic line to stderr when the spike trace knob is
// set, so a benchmark run reveals whether the builder sandbox is reused or
// recreated on each build.
func builderTracef(format string, args ...any) {
	if strings.TrimSpace(os.Getenv(builderTraceEnv)) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[builder-trace] "+format+"\n", args...)
}

// builderCacheTmpfsSizeMiB is the tmpfs cache size when no override is set.
const builderCacheTmpfsSizeMiB uint32 = 16384

// builderKeepAliveInterval must be below the builder idle timeout so
// consecutive touches always land before the VM is reaped. It is a variable so
// tests can shorten it.
var builderKeepAliveInterval = 2 * time.Second

// buildkitdCommand builds the in-VM command that starts buildkitd, binding to
// builderGuestPort and appending its output to builderLogPath on the cache
// mount for debugging. The snapshotter is resolved per call so a spike override
// applies to a freshly created VM.
func buildkitdCommand() string {
	return fmt.Sprintf(
		"exec buildkitd --oci-worker-snapshotter %s --addr tcp://0.0.0.0:%d >> %s 2>&1",
		builderSnapshotter(),
		builderGuestPort,
		builderLogPath,
	)
}

var buildctlPruneCommand = fmt.Sprintf(
	"buildctl --addr tcp://127.0.0.1:%d prune --keep-duration 24h --all",
	builderGuestPort,
)

func builderSandboxName() string {
	return "agents-sandbox-buildkit-" + buildkitVersion
}

type VM struct {
	Msb   msb.Client
	Ready func(ctx context.Context, hostPort int) error
}

func builderSandboxOptions(hostPort int) []msbSdk.SandboxOption {
	return []msbSdk.SandboxOption{
		msbSdk.WithImage(builderImage),
		msbSdk.WithMounts(map[string]msbSdk.MountConfig{
			cacheMountPath: builderCacheMount(),
		}),
		//nolint:gosec // G115: builder host ports are well below the uint16 max
		msbSdk.WithPortBindings(msbSdk.PortBinding{
			Bind:      "127.0.0.1",
			HostPort:  uint16(hostPort),
			GuestPort: builderGuestPort,
			Protocol:  msbSdk.PortProtocolTCP,
		}),
		msbSdk.WithNetwork(builderNetworkPolicy()),
		msbSdk.WithCPUs(sysinfo.NumCPUs()),
		msbSdk.WithMaxCPUs(sysinfo.NumCPUs()),
		msbSdk.WithMemory(builderMemoryMiB()),
		msbSdk.WithMaxMemory(builderMaxMemoryMiB()),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(builderIdleTimeout()),
		msbSdk.WithReplace(),
		msbSdk.WithLabels(map[string]string{naming.LabelComponent: "buildkit"}),
	}
}

// builderMemoryMiB is the builder VM's initial memory. CPUs already default to
// every host core; the initial memory starts at options.DefaultMemoryMiB and can
// only balloon up to host RAM, so a spike override raises the starting point
// (clamped to host RAM).
func builderMemoryMiB() uint32 {
	initial := options.DefaultMemoryMiB
	if raw := strings.TrimSpace(os.Getenv(builderMemoryEnv)); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 32); err == nil && parsed > 0 {
			initial = uint32(parsed)
		}
	}
	if maxMiB := builderMaxMemoryMiB(); maxMiB > 0 && initial > maxMiB {
		initial = maxMiB
	}
	return initial
}

// builderMaxMemoryMiB is the builder VM's memory ceiling (host RAM).
func builderMaxMemoryMiB() uint32 {
	//nolint:gosec // G115: TotalMemoryGiB is physical RAM in GiB, cannot overflow uint32
	return options.ParseMemoryGiB(uint32(sysinfo.TotalMemoryGiB()))
}

// builderIdleTimeout returns the builder VM's idle timeout, overridable by a
// spike knob to test whether keeping the VM alive across builds preserves an
// in-VM (tmpfs) cache. Values not below builderKeepAliveInterval are ignored
// because the keep-alive loop could not refresh the timer in time.
func builderIdleTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(builderIdleTimeoutEnv)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > builderKeepAliveInterval {
			return parsed
		}
	}
	return builderDefaultIdleTimeout
}

// builderCacheMode selects the BuildKit cache backing: the production named
// volume, a host bind mount, a tmpfs, or a spike mode (tmpfs-export,
// registry) combining fast tmpfs worker storage with an external cache.
func builderCacheMode() string {
	switch mode := strings.TrimSpace(os.Getenv(builderCacheModeEnv)); mode {
	case cacheModeBind, cacheModeTmpfs, cacheModeTmpfsExport, cacheModeRegistry:
		return mode
	case "", cacheModeVolume:
		return cacheModeVolume
	default:
		return cacheModeVolume
	}
}

// builderCacheMount describes the worker-storage mount for the selected mode.
func builderCacheMount() msbSdk.MountConfig {
	switch builderCacheMode() {
	case cacheModeBind:
		return msbSdk.Mount.Bind(builderCacheBindPath(), msbSdk.MountOptions{})
	case cacheModeTmpfs, cacheModeTmpfsExport, cacheModeRegistry:
		//exhaustruct:ignore // only size matters for a build cache
		return msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: builderCacheSizeMiB()})
	default:
		return msbSdk.Mount.Named(cacheVolumeName, msbSdk.MountOptions{})
	}
}

// builderCacheExportEnabled reports whether the selected mode persists the layer
// cache to an external location (a host dir for tmpfs-export, a registry for
// registry mode) rather than only in the worker storage.
func builderCacheExportEnabled() bool {
	switch builderCacheMode() {
	case cacheModeTmpfsExport, cacheModeRegistry:
		return true
	default:
		return false
	}
}

// builderCacheRegistryRef is the OCI reference of the registry cache image, e.g.
// "registry.example.com/team/agents-sandbox-cache:latest" or, for a local
// registry, "<gateway-host>:5000/agents-sandbox/cache:latest".
func builderCacheRegistryRef() string {
	return strings.TrimSpace(os.Getenv(builderCacheRefEnv))
}

// builderCacheRegistryInsecure allows plain-HTTP registries (e.g. a local
// "registry:2" with no TLS).
func builderCacheRegistryInsecure() bool {
	value := strings.TrimSpace(os.Getenv(builderCacheInsecure))
	return value == "1" || strings.EqualFold(value, "true")
}

// builderCacheBindPath is the host directory backing the bind cache mode.
func builderCacheBindPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), "buildkit-cache")
}

// builderCacheExportPath is the host directory BuildKit's local cache
// import/export uses in tmpfs-export mode. It is resolved on the CLI host (the
// client's file-sync session), not inside the builder VM, so it persists
// independently of the VM while the worker storage stays on tmpfs.
func builderCacheExportPath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), "buildkit-cache-export")
}

// builderSnapshotter selects buildkitd's OCI worker snapshotter.
func builderSnapshotter() string {
	if raw := strings.TrimSpace(os.Getenv(builderSnapshotterEnv)); raw != "" {
		return raw
	}
	return "native"
}

// builderCacheSizeMiB is the tmpfs cache size, overridable by a spike knob.
func builderCacheSizeMiB() uint32 {
	if raw := strings.TrimSpace(os.Getenv(builderCacheSizeEnv)); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 32); err == nil && parsed > 0 {
			return uint32(parsed)
		}
	}
	return builderCacheTmpfsSizeMiB
}

// builderNetworkPolicy returns the builder VM's network policy, optionally
// disabling or narrowing TLS interception for the spike knob.
func builderNetworkPolicy() *msbSdk.NetworkConfig {
	policy := msbSdk.NetworkPolicy.AllowAll()
	if tls := builderTLSConfig(); tls != nil {
		policy.TLS = tls
	}
	return policy
}

// builderTLSConfig translates the spike knob into a TLS config, or nil to keep
// the default. "all" sets a sentinel intercepted port so no real port is
// intercepted: the Go SDK cannot send enabled=false, and a non-empty port list
// is required to avoid the SDK's omitempty reverting to the native default
// ([443]).
func builderTLSConfig() *msbSdk.TLSConfig {
	raw := strings.TrimSpace(os.Getenv(builderTLSBypassEnv))
	if raw == "" {
		return nil
	}
	if raw == "all" {
		//exhaustruct:ignore // only the intercepted-port sentinel matters here
		return &msbSdk.TLSConfig{InterceptedPorts: []uint16{1}}
	}
	var patterns []string
	for pattern := range strings.SplitSeq(raw, ",") {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	//exhaustruct:ignore // Bypass is the only field the spike sets
	return &msbSdk.TLSConfig{Bypass: patterns}
}
