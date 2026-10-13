package builder

import (
	"context"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
)

const (
	buildkitVersion         = "v0.25.2"
	builderImage            = "moby/buildkit:" + buildkitVersion
	builderGuestPort uint16 = 1234
	cacheMountPath          = "/var/lib/buildkit"
	// builderLogPath lives on the tmpfs worker storage, so buildkitd logs are
	// recreated on each builder boot and are intended for debugging only.
	builderLogPath      = cacheMountPath + "/buildkitd.log"
	builderHostPortBase = 11234
	// builderIdleTimeout is short so a crashed launcher cannot leak the shared
	// builder VM; Client.Build keeps it alive via KeepAlive while a build runs.
	builderIdleTimeout = 5 * time.Second

	// builderCacheTmpfsSizeMiB is the BuildKit worker-storage size. The layer
	// cache itself is kept in the embedded registry, not on the worker mount.
	builderCacheTmpfsSizeMiB uint32 = 16384
)

// builderKeepAliveInterval must be below builderIdleTimeout so consecutive
// touches always land before the VM is reaped. It is a variable so tests can
// shorten it.
var builderKeepAliveInterval = 2 * time.Second

// buildkitdCommand builds the in-VM command that starts buildkitd, binding to
// builderGuestPort and appending its output to builderLogPath for debugging.
func buildkitdCommand() string {
	return fmt.Sprintf(
		"exec buildkitd --oci-worker-snapshotter native --addr tcp://0.0.0.0:%d >> %s 2>&1",
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
		msbSdk.WithMemory(options.DefaultMemoryMiB),
		msbSdk.WithMaxMemory(builderMaxMemoryMiB()),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(builderIdleTimeout),
		msbSdk.WithReplace(),
		msbSdk.WithLabels(map[string]string{naming.LabelComponent: "buildkit"}),
	}
}

// builderCacheMount is the BuildKit worker storage: always a tmpfs sized to
// builderCacheTmpfsSizeMiB. The persistent layer cache is the registry.
func builderCacheMount() msbSdk.MountConfig {
	//exhaustruct:ignore // only size matters for the tmpfs worker storage
	return msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: builderCacheTmpfsSizeMiB})
}

// builderMaxMemoryMiB is the builder VM's memory ceiling (host RAM).
func builderMaxMemoryMiB() uint32 {
	//nolint:gosec // G115: TotalMemoryGiB is physical RAM in GiB, cannot overflow uint32
	return options.ParseMemoryGiB(uint32(sysinfo.TotalMemoryGiB()))
}

// builderNetworkPolicy returns the builder VM's network policy, pinning the
// guest subnet pool to the dedicated /30 the cache registry binds its gateway on.
func builderNetworkPolicy() *msbSdk.NetworkConfig {
	policy := msbSdk.NetworkPolicy.AllowAll()
	policy.IPv4Pool = cacheregistry.BuilderIPv4Pool
	return policy
}
