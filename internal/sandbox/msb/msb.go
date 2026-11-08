package msb

import (
	"context"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// Get is the factory clients can use to get an Client.
// Tests override Get to inject mocks.
var Get = func() Client {
	return &realMsbClient{}
}

var skipRuntimeInstall bool

// IsRealClient reports whether production SDK calls are active.
func IsRealClient() bool {
	_, ok := Get().(*realMsbClient)
	return ok
}

// SkipRuntimeInstall prevents later VM lifecycle calls from invoking the SDK's
// replacement-capable installer after runtime preparation has made a decision.
func SkipRuntimeInstall() { skipRuntimeInstall = true }

// resolveRuntime and ensureRuntime name the private home explicitly, so
// resolution does not depend on whether ApplyHome already set MSB_HOME.
var (
	resolveRuntime = msbSdk.ResolveRuntime
	ensureRuntime  = msbSdk.EnsureRuntime
	installRuntime = msbSdk.InstallRuntime
)

// runtimeConfig names agents-sandbox's private runtime home explicitly. MSB_HOME
// still governs the FFI cache and all non-setup SDK operations.
func runtimeConfig() msbSdk.RuntimeConfig {
	return msbSdk.RuntimeConfig{Home: configpaths.MSBHome()}
}

// ValidateInstalled checks the SDK-managed runtime without downloading it.
func ValidateInstalled(_ context.Context) error {
	_, err := resolveRuntime(runtimeConfig())
	return err
}

// PinnedRuntimeVersion is the microsandbox runtime version the linked SDK
// requires; the launcher compares the installed runtime against it.
func PinnedRuntimeVersion() string { return msbSdk.SDKVersion() }

// EnsureRuntime installs the SDK-pinned runtime into the private home if it is
// absent, leaving an existing complete pair in place.
func EnsureRuntime(ctx context.Context) error {
	_, err := ensureRuntime(ctx, runtimeConfig(), msbSdk.InstallOptions{})
	return err
}

// InstallRuntime force-installs the SDK-pinned runtime into the private home,
// replacing a complete but stale pair.
func InstallRuntime(ctx context.Context) error {
	_, err := installRuntime(ctx, runtimeConfig(), msbSdk.InstallOptions{Force: true})
	return err
}
