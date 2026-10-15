package image

import (
	"context"
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ImageRef is the reference microsandbox uses to run this image.
func (info ImageInfo) ImageRef() string {
	if info.Ref != "" {
		return info.Ref
	}
	return info.Tag
}

// SandboxImageOptions returns the msb options that reference the runner image,
// marking the embedded registry's plain-HTTP pull and forcing a re-resolve after
// a fresh build so a rebuilt same-tag image is not served stale.
func SandboxImageOptions(info ImageInfo) []msbSdk.SandboxOption {
	options := []msbSdk.SandboxOption{msbSdk.WithImage(info.ImageRef())}
	if info.RegistryInsecure {
		options = append(options, msbSdk.WithRegistryInsecure())
	}
	if info.Refresh {
		options = append(options, msbSdk.WithPullPolicy(msbSdk.PullPolicyAlways))
	}
	return options
}

// EnsureAvailable makes the runner image available to microsandbox for a sandbox
// that is about to be created. An archive is loaded (moby); a registry image
// needs no load, but its registry must stay alive across CreateSandbox, so the
// returned stop function must be called after the sandbox is created.
func EnsureAvailable(ctx context.Context, mclient msb.Client, info ImageInfo, ui termio.UI) (func(), error) {
	if info.ArchivePath != "" {
		spin := ui.Spinner("Loading image into microsandbox")
		if err := mclient.ImageLoad(ctx, info.Tag, info.ArchivePath); err != nil {
			spin.StopError(err)
			return func() {}, err
		}
		spin.Stop()
		return func() {}, nil
	}
	if info.Ref != "" {
		return holdRegistry(ctx)
	}
	if err := mclient.ImageGet(ctx, info.Tag); err == nil {
		return func() {}, nil
	}
	return func() {}, fmt.Errorf(
		"runner image %s is not in the microsandbox cache and no build archive is available",
		info.ImageRef(),
	)
}

// holdRegistry and imagePullRef are package-level seams that keep the registry
// dependency out of unit tests: resolving a pull ref would otherwise spawn the
// embedded registry daemon, and holding it would need a live registry.
var (
	holdRegistry = builder.HoldRegistry
	imagePullRef = builder.ImagePullRef
)

// InstallOfflineImageDelivery keeps a unit-test binary hermetic: resolving a
// pull ref or holding the registry would otherwise spawn the embedded registry
// daemon and, under `go test`, re-run the whole suite. Every package whose
// tests reach EnsureImage must call this from TestMain. A test tier that drives
// the real registry (integration) must not call it, and may override the seams
// per test instead.
func InstallOfflineImageDelivery() {
	imagePullRef = func(context.Context, string) (string, bool, error) { return "", false, nil }
	holdRegistry = func(context.Context) (func(), error) { return func() {}, nil }
}

// resolveImageRef returns the reference microsandbox will use for rTag and
// whether it is pulled from the insecure embedded registry. It returns an empty
// ref for archive backends.
func resolveImageRef(ctx context.Context, rTag string) (string, bool, error) {
	pullRef, ok, err := imagePullRef(ctx, rTag)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return pullRef, true, nil
}
