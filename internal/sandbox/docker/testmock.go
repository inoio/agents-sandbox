package docker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"
)

func WithDockerMock(t *testing.T, mock Client) {
	t.Helper()

	orig := Get

	Get = func() Client { return mock }

	t.Cleanup(func() {
		Get = orig
	})
}

func WithNoopDockerMock(t *testing.T) {
	WithDockerMock(t, newDefaultDockerClient())
}

func WithDefaultErrorDockerMock(t *testing.T) {
	WithDockerMock(t, newDefaultErrorDockerClient())
}

func newDefaultDockerClient() *MockDockerClient {
	return &MockDockerClient{}
}

type mockErrors struct {
	buildErr, inspectErr, saveErr, removeErr error
}

func newDefaultErrorDockerClient() *MockDockerClient {
	return newErrorDockerClient(mockErrors{nil, nil, nil, nil})
}

//nolint:revive // callbacks always return errors; params required by interface signatures
func newErrorDockerClient(mockErrs mockErrors) *MockDockerClient {
	defaultErr := errors.New("cannot connect to Docker daemon")
	var buildErr, inspectErr, saveErr, removeErr = defaultErr, defaultErr, defaultErr, defaultErr
	if mockErrs.buildErr != nil {
		buildErr = mockErrs.buildErr
	}
	if mockErrs.inspectErr != nil {
		inspectErr = mockErrs.inspectErr
	}
	if mockErrs.saveErr != nil {
		saveErr = mockErrs.saveErr
	}
	if mockErrs.removeErr != nil {
		removeErr = mockErrs.removeErr
	}
	return &MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, buildErr
		},
		ImageInspectFn: func(_ context.Context, _ string, inspectOpts ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, inspectErr
		},
		ImageSaveFn: func(_ context.Context, _ []string, saveOpts ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, saveErr
		},
		ImageRemoveFn: func(_ context.Context, _ string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			return client.ImageRemoveResult{}, removeErr
		},
		ImageTagFn: nil,
	}
}

// MockDockerClient is the zero implementation of sandbox.DockerClient.
// All methods succeed with nil/empty returns. Use newErrorDockerClient
// to override specific methods with errors.
type MockDockerClient struct {
	ImageBuildFn   func(ctx context.Context, r io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error)
	ImageInspectFn func(ctx context.Context, ref string, inspectOpts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageSaveFn    func(ctx context.Context, refs []string, saveOpts ...client.ImageSaveOption) (client.ImageSaveResult, error)
	ImageRemoveFn  func(ctx context.Context, ref string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImageTagFn     func(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error)
}

// Compile time check Client interface conformity of MockDockerClient.
var _ Client = (*MockDockerClient)(nil)

func (m *MockDockerClient) ImageBuild(
	ctx context.Context,
	r io.Reader,
	options client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	if m.ImageBuildFn != nil {
		return m.ImageBuildFn(ctx, r, options)
	}
	return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (m *MockDockerClient) ImageInspect(
	ctx context.Context,
	ref string,
	opts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, ref, opts...)
	}
	//nolint:exhaustruct // DockerOCIImageConfig has unexported fields
	result := client.ImageInspectResult{}
	//nolint:exhaustruct // DockerOCIImageConfig has unexported fields
	result.Config = &dockerspec.DockerOCIImageConfig{}
	return result, nil
}

func (m *MockDockerClient) ImageSave(
	ctx context.Context,
	refs []string,
	opts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	if m.ImageSaveFn != nil {
		return m.ImageSaveFn(ctx, refs, opts...)
	}
	return io.NopCloser(nil), nil
}

func (m *MockDockerClient) ImageRemove(
	ctx context.Context,
	ref string,
	opts client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, ref, opts)
	}
	return client.ImageRemoveResult{}, nil
}

func (m *MockDockerClient) ImageTag(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error) {
	if m.ImageTagFn != nil {
		return m.ImageTagFn(ctx, opts)
	}
	return client.ImageTagResult{}, nil
}
