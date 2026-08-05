package sandbox

import (
	"context"
	"io"

	"github.com/moby/moby/client"
)

// BuildImageDockerClient is a test-only mock that satisfies DockerClient
// for use by external test packages (e.g. cmd/opencode-msb tests).
type BuildImageDockerClient struct {
	ImageBuildFn   func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error)
	ImageInspectFn func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageSaveFn    func(context.Context, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error)
	ImageRemoveFn  func(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImageTagFn     func(context.Context, client.ImageTagOptions) (client.ImageTagResult, error)
	CloseFn        func() error
}

func (m *BuildImageDockerClient) ImageBuild(
	ctx context.Context,
	buildContext io.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	if m.ImageBuildFn != nil {
		return m.ImageBuildFn(ctx, buildContext, opts)
	}
	return client.ImageBuildResult{}, nil
}

func (m *BuildImageDockerClient) ImageInspect(
	ctx context.Context,
	imageID string,
	inspectOpts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, imageID, inspectOpts...)
	}
	return client.ImageInspectResult{}, nil
}

func (m *BuildImageDockerClient) ImageSave(
	ctx context.Context,
	imageIDs []string,
	opts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	if m.ImageSaveFn != nil {
		return m.ImageSaveFn(ctx, imageIDs, opts...)
	}
	//nolint:nilnil // ImageSaveResult is an interface; nil is the correct zero value
	return nil, nil
}

func (m *BuildImageDockerClient) ImageRemove(
	ctx context.Context,
	imageID string,
	opts client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, imageID, opts)
	}
	return client.ImageRemoveResult{}, nil
}

func (m *BuildImageDockerClient) ImageTag(
	ctx context.Context,
	opts client.ImageTagOptions,
) (client.ImageTagResult, error) {
	if m.ImageTagFn != nil {
		return m.ImageTagFn(ctx, opts)
	}
	return client.ImageTagResult{}, nil
}

func (m *BuildImageDockerClient) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}

// SetNewMsbClient replaces the internal msb client factory used by Prune
// with one that returns the provided mock MsbClient. The original factory
// is returned so callers can restore it after their test. This function
// is test only and should not be called from production code.
//
// Usage from an external test package:
//
//	mock := &sandbox.MockMsbClient{}
//	mock.Sandboxes = []sandbox.SandboxHandle{&sandbox.MockSandboxHandle{Name_: "test-vm"}}
//	orig := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
//	t.Cleanup(func() { sandbox.SetNewMsbClient(orig) })
func SetNewMsbClient(f func() MsbClient) func() MsbClient {
	orig := newMsbClient
	newMsbClient = f
	return orig
}
