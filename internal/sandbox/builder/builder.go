package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

const (
	buildkitVersion = "v0.25.2"
	// builderImage is the upstream BuildKit image with the rootless variant, which
	// bundles the fuse-overlayfs binary (and buildkit-runc) that the guest's
	// overlayfs cannot replace. It runs as uid 1000 by default; the sandbox sets
	// User=root so buildkitd can own the worker volume.
	builderImage            = "moby/buildkit:" + buildkitVersion + "-rootless"
	builderGuestPort uint16 = 1234
	cacheMountPath          = "/var/lib/buildkit"
	// builderLogPath lives on the worker storage, so buildkitd logs are
	// recreated on each builder boot and are intended for debugging only.
	builderLogPath      = cacheMountPath + "/buildkitd.log"
	builderHostPortBase = 11234
	// builderIdleTimeout matches the cache registry's idle timeout (see
	// cacheregistry.IdleTimeout): the shared builder VM and the registry daemon
	// expire together. Client.Build keeps the VM alive via KeepAlive while a
	// build runs, so the timeout only reaps an idle VM.
	builderIdleTimeout = cacheregistry.IdleTimeout

	// builderCacheDiskSizeMiB is the BuildKit worker-storage capacity (128 GiB).
	// The worker is disk-backed (an owned volume that survives the sandbox's
	// stop/start and is removed with the sandbox), not tmpfs: a RAM-backed worker
	// exhausts the guest's memory on large builds (BuildKit materializes every
	// layer into it), which kills buildkitd mid-solve. The native snapshotter
	// keeps a full copy of every unpacked layer, so a single process building many
	// image variants accumulates well beyond the low tens of GiB; 128 GiB leaves
	// ample headroom. The persistent layer cache is the embedded registry, not the
	// worker.
	builderCacheDiskSizeMiB uint32 = 131072

	// builderCacheKeepStorage bounds the retained BuildKit worker cache when it is
	// pruned (see buildctlPruneCommand). The worker is resumed across idle reaps
	// rather than recreated, so it would otherwise grow without limit; the
	// registry holds the authoritative persistent cache, so pruning the worker by
	// size is safe. Kept below builderCacheDiskSizeMiB so the volume never fills.
	builderCacheKeepStorage = "50GB"

	// builderCacheGCKeepStorage is buildkitd's automatic-GC budget, format
	// "Reserved,Free,Maximum" in MB. The default is "2000,2000,2000" (2 GiB), which
	// on multi-GB images makes the daemon evict cached layers continuously during a
	// build (re-unpack churn + GC IO). Align it with builderCacheKeepStorage (50
	// GiB) so the worker stays warm between rebuilds, still under the 128 GiB
	// volume.
	builderCacheGCKeepStorage = "50000,50000,50000"

	// Snapshotter names for buildkitd's --oci-worker-snapshotter. The guest fs
	// cannot host a kernel overlay mount, so "native" (copy-based) was the
	// original default; "fuse-overlayfs" is now the default because the guest
	// provides FUSE and the userspace overlay halves build-step time on large
	// images (see the builder-performance guide). builderSnapshotterEnv can force
	// native for troubleshooting.
	snapshotterNative        = "native"
	snapshotterFuseOverlayfs = "fuse-overlayfs"
	// builderSnapshotterEnv overrides the worker snapshotter (troubleshooting seam).
	// Empty selects the default (fuse-overlayfs).
	builderSnapshotterEnv = "AGENTS_SANDBOX_BUILDER_SNAPSHOTTER"

	// builderNoProcessSandboxEnv opts build steps out of BuildKit's per-step
	// process sandbox (PID/mount namespaces). Disabling it skips the per-step
	// namespace setup and avoids the guest's cgroup-controller warning, at the
	// cost of build steps sharing buildkitd's PID namespace — acceptable in the
	// throwaway per-user builder VM. Opt-in while its benefit is measured.
	builderNoProcessSandboxEnv = "AGENTS_SANDBOX_BUILDER_NO_PROCESS_SANDBOX"

	// workerSnapshotterMarker records which snapshotter formatted the worker, so a
	// snapshotter change clears the (snapshotter-specific) worker once.
	workerSnapshotterMarker = cacheMountPath + "/.agents-sandbox-snapshotter"

	// builderCompression is the layer compression for both the runner-image push
	// (or OCI archive on the moby path) and the registry cache export. zstd
	// compresses multi-GB images far faster than BuildKit's default gzip, cutting
	// the dominant "[internal] exporting to oci image format" cost (measured
	// ~1m30s on the heavy dev image) and the cache-export push.
	// builderCompressionEnv overrides the algorithm (e.g. "uncompressed", "gzip")
	// for A/B measurement, and builderCompressionLevelEnv sets a zstd/gzip level.
	builderCompression         = "zstd"
	builderCompressionEnv      = "AGENTS_SANDBOX_BUILDER_COMPRESSION"
	builderCompressionLevelEnv = "AGENTS_SANDBOX_BUILDER_COMPRESSION_LEVEL"

	// buildkitAttrTrue is the string value BuildKit expects for boolean frontend
	// and exporter attributes.
	buildkitAttrTrue = "true"

	// buildkitAttrRegistryInsecure marks a registry export/cache endpoint as
	// plain HTTP, which the embedded registry is.
	buildkitAttrRegistryInsecure = "registry.insecure"
)

// builderSnapshotter returns the BuildKit worker snapshotter: fuse-overlayfs
// unless overridden.
func builderSnapshotter() string {
	if override := os.Getenv(builderSnapshotterEnv); override != "" {
		return override
	}
	return snapshotterFuseOverlayfs
}

// builderNoProcessSandbox reports whether build steps run without BuildKit's
// per-step process sandbox, from builderNoProcessSandboxEnv. The default (unset
// or unparsable) keeps the sandbox on.
func builderNoProcessSandbox() bool {
	enabled, err := strconv.ParseBool(os.Getenv(builderNoProcessSandboxEnv))
	return err == nil && enabled
}

// applyCompressionAttrs sets the layer compression (and an optional level) on an
// exporter or cache-option attribute map, from the environment overrides. The
// level applies only to the compressing algorithms (zstd/gzip).
func applyCompressionAttrs(attrs map[string]string) {
	compression := builderCompression
	if override := os.Getenv(builderCompressionEnv); override != "" {
		compression = override
	}
	attrs["compression"] = compression
	if level := strings.TrimSpace(os.Getenv(builderCompressionLevelEnv)); level != "" {
		attrs["compression-level"] = level
	}
}

// builderKeepAliveInterval must be below builderIdleTimeout so consecutive
// touches always land before the VM is reaped. It is a variable so tests can
// shorten it.
var builderKeepAliveInterval = 2 * time.Second

// buildkitdCommand builds the in-VM command that starts buildkitd, binding to
// builderGuestPort and appending its output to builderLogPath for debugging.
func buildkitdCommand() string {
	snapshotter := builderSnapshotter()
	flags := []string{
		"--oci-worker-snapshotter " + snapshotter,
		"--oci-worker-gc-keepstorage " + builderCacheGCKeepStorage,
	}
	if builderNoProcessSandbox() {
		flags = append(flags, "--oci-worker-no-process-sandbox")
	}
	return fmt.Sprintf(
		"%sexec buildkitd %s --addr tcp://0.0.0.0:%d >> %s 2>&1",
		snapshotterPrepareCommand(snapshotter),
		strings.Join(flags, " "),
		builderGuestPort,
		builderLogPath,
	)
}

// snapshotterPrepareCommand returns the shell steps run before buildkitd starts.
// It ensures the snapshotter's userspace binary (fuse-overlayfs is baked into the
// builder image; the install is a fallback for image overrides) and clears a
// worker formatted by a different snapshotter, since BuildKit's snapshot data is
// snapshotter-specific.
func snapshotterPrepareCommand(snapshotter string) string {
	if snapshotter == "" {
		return ""
	}
	install := ""
	if snapshotter == snapshotterFuseOverlayfs {
		install = "command -v fuse-overlayfs >/dev/null 2>&1 || " +
			"apk add --no-cache fuse-overlayfs >/dev/null 2>&1 || true; "
	}
	return install + fmt.Sprintf(
		"if [ \"$(cat %s 2>/dev/null)\" != %s ]; then rm -rf %s/*; echo %s > %s; fi; ",
		workerSnapshotterMarker,
		snapshotter,
		cacheMountPath,
		snapshotter,
		workerSnapshotterMarker,
	)
}

var buildctlPruneCommand = fmt.Sprintf(
	"buildctl --addr tcp://127.0.0.1:%d prune --keep-duration 24h --keep-storage %s --all",
	builderGuestPort,
	builderCacheKeepStorage,
)

// builderSandboxName is the builder VM's msb name. It is namespaced by the state
// directory so processes that use different state dirs (an integration test's
// temp dir vs. the real user state) get distinct builder VMs. Without the
// namespace they share one fixed name, and each process — unable to recognize
// the other's port record — recreates the VM with WithReplace, thrashing the
// running builder and its worker cache.
func builderSandboxName() string {
	return "agents-sandbox-buildkit-" + buildkitVersion + "-" + stateNamespace()
}

// BuilderComponentLabelValue is the builder VM's component-label value, namespaced
// by the state directory like the sandbox name. It is exported so callers (e.g.
// integration tests) listing builders by label match only their own state dir's
// builder, never a concurrently running real binary's.
func BuilderComponentLabelValue() string {
	return "buildkit-" + stateNamespace()
}

// stateNamespace is a short, stable hash of the state directory that keeps
// per-state-dir builder singletons apart.
func stateNamespace() string {
	sum := sha256.Sum256([]byte(configpaths.Get().UserStateDir()))
	return hex.EncodeToString(sum[:])[:8]
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
		// The rootless BuildKit image runs as uid 1000; run as root so buildkitd
		// owns the worker volume and can mount the fuse-overlayfs upper dir.
		msbSdk.WithUser("root"),
		msbSdk.WithCPUs(sysinfo.NumCPUs()),
		msbSdk.WithMaxCPUs(sysinfo.NumCPUs()),
		msbSdk.WithMemory(options.DefaultMemoryMiB),
		msbSdk.WithMaxMemory(builderMaxMemoryMiB()),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(builderIdleTimeout),
		msbSdk.WithReplace(),
		msbSdk.WithLabels(map[string]string{naming.LabelComponent: BuilderComponentLabelValue()}),
	}
}

// builderCacheMount is the BuildKit worker storage: a disk-backed owned volume
// sized to builderCacheDiskSizeMiB (128 GiB). It is deliberately not tmpfs — a
// RAM-backed worker exhausts the builder VM's memory on large builds and kills
// buildkitd. The persistent layer cache is the registry, so an ephemeral worker
// is fine.
func builderCacheMount() msbSdk.MountConfig {
	//exhaustruct:ignore // only kind and size matter for the owned worker volume
	return msbSdk.Mount.Owned(msbSdk.OwnedVolumeOptions{
		Kind:    msbSdk.VolumeKindDisk,
		SizeMiB: builderCacheDiskSizeMiB,
	})
}

// builderMaxMemoryMiB is the builder VM's memory ceiling (host RAM).
func builderMaxMemoryMiB() uint32 {
	//nolint:gosec // G115: TotalMemoryGiB is physical RAM in GiB, cannot overflow uint32
	return options.ParseMemoryGiB(uint32(sysinfo.TotalMemoryGiB()))
}

// builderNetworkPolicy returns the builder VM's network policy: permissive
// egress on microsandbox's default pool, so buildkitd can reach public
// registries and the embedded cache registry through the sandbox host alias.
func builderNetworkPolicy() *msbSdk.NetworkConfig {
	return msbSdk.NetworkPolicy.AllowAll()
}
