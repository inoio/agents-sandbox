package builder

import (
	"context"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
)

const (
	buildkitVersion            = "v0.25.2"
	builderImage               = "moby/buildkit:" + buildkitVersion
	builderGuestPort    uint16 = 1234
	cacheVolumeName            = "agents-sandbox-buildkit-cache"
	cacheMountPath             = "/var/lib/buildkit"
	builderHostPortBase        = 11234
	builderIdleTimeout         = 5 * time.Minute
	buildkitdCommand           = "exec buildkitd --oci-worker-snapshotter native --addr tcp://0.0.0.0:1234"
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
			cacheMountPath: msbSdk.Mount.Named(cacheVolumeName, msbSdk.MountOptions{}),
		}),
		//nolint:gosec // G115: builder host ports are well below the uint16 max
		msbSdk.WithPortBindings(msbSdk.PortBinding{
			Bind:      "127.0.0.1",
			HostPort:  uint16(hostPort),
			GuestPort: builderGuestPort,
			Protocol:  msbSdk.PortProtocolTCP,
		}),
		msbSdk.WithNetwork(msbSdk.NetworkPolicy.AllowAll()),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(builderIdleTimeout),
		msbSdk.WithReplace(),
		msbSdk.WithLabels(map[string]string{naming.LabelComponent: "buildkit"}),
	}
}
