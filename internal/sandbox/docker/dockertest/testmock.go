//deadcode:ignore reason="test support: referenced only by _test.go files"
package dockertest

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// PullResponse is a client.ImagePullResponse for tests: reads come from Body and
// Wait returns WaitErr, modelling the registry failure moby delivers inside the
// pull stream. JSONMessages is unused by the launcher and yields nothing.
type PullResponse struct {
	Body    io.Reader
	WaitErr error
}

func (p PullResponse) Read(b []byte) (int, error) {
	if p.Body == nil {
		return 0, io.EOF
	}
	return p.Body.Read(b)
}

func (p PullResponse) Close() error { return nil }

func (p PullResponse) Wait(context.Context) error { return p.WaitErr }

func (p PullResponse) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {}
}

func WithDockerMock(t *testing.T, mock docker.Client) {
	t.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames; it cannot change this helper's result"

	orig := docker.Get

	docker.Get = func() docker.Client { return mock }

	t.Cleanup(func() {
		docker.Get = orig
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
	buildErr, inspectErr, saveErr error
}

func newDefaultErrorDockerClient() *MockDockerClient {
	return newErrorDockerClient(mockErrors{nil, nil, nil})
}

func newErrorDockerClient(mockErrs mockErrors) *MockDockerClient {
	defaultErr := errors.New("cannot connect to Docker daemon")
	buildErr, inspectErr, saveErr := defaultErr, defaultErr, defaultErr
	if mockErrs.buildErr != nil {
		buildErr = mockErrs.buildErr
	}
	if mockErrs.inspectErr != nil {
		inspectErr = mockErrs.inspectErr
	}
	if mockErrs.saveErr != nil {
		saveErr = mockErrs.saveErr
	}
	return &MockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, buildErr
		},
		ImageInspectFn: func(_ context.Context, _ string, inspectOpts ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, inspectErr
		},
		ImageListFn: func(_ context.Context, _ client.ImageListOptions) (client.ImageListResult, error) {
			return client.ImageListResult{}, defaultErr
		},
		ImageSaveFn: func(_ context.Context, _ []string, saveOpts ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, saveErr
		},
		ImagePullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
			return nil, defaultErr
		},
		ImagePruneFn: func(_ context.Context, _ client.ImagePruneOptions) (client.ImagePruneResult, error) {
			return client.ImagePruneResult{}, defaultErr
		},
		PingFn: func(ctx context.Context, opts client.PingOptions) (client.PingResult, error) {
			return client.PingResult{}, errors.New("cannot connect to Docker daemon")
		},
	}
}

// MockDockerClient is the zero implementation of docker.Client.
// All methods succeed with nil/empty returns. Use newErrorDockerClient
// to override specific methods with errors.
type MockDockerClient struct {
	ImageBuildFn   func(ctx context.Context, r io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error)
	ImageInspectFn func(ctx context.Context, ref string, inspectOpts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageListFn    func(ctx context.Context, opts client.ImageListOptions) (client.ImageListResult, error)
	ImageSaveFn    func(ctx context.Context, refs []string, saveOpts ...client.ImageSaveOption) (client.ImageSaveResult, error)
	ImagePullFn    func(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
	ImagePruneFn   func(ctx context.Context, opts client.ImagePruneOptions) (client.ImagePruneResult, error)
	PingFn         func(ctx context.Context, opts client.PingOptions) (client.PingResult, error)
}

// Compile time check Client interface conformity of MockDockerClient.
var _ docker.Client = (*MockDockerClient)(nil)

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
	result := client.ImageInspectResult{
		Config: &dockerspec.DockerOCIImageConfig{},
	}
	return result, nil
}

func (m *MockDockerClient) ImageList(
	ctx context.Context,
	opts client.ImageListOptions,
) (client.ImageListResult, error) {
	if m.ImageListFn != nil {
		return m.ImageListFn(ctx, opts)
	}
	return client.ImageListResult{}, nil
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

func (m *MockDockerClient) ImagePull(
	ctx context.Context,
	ref string,
	opts client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	if m.ImagePullFn != nil {
		return m.ImagePullFn(ctx, ref, opts)
	}
	return PullResponse{Body: strings.NewReader("")}, nil
}

func (m *MockDockerClient) ImagePrune(
	ctx context.Context,
	opts client.ImagePruneOptions,
) (client.ImagePruneResult, error) {
	if m.ImagePruneFn != nil {
		return m.ImagePruneFn(ctx, opts)
	}
	return client.ImagePruneResult{}, nil
}

func (m *MockDockerClient) Ping(
	ctx context.Context,
	opts client.PingOptions,
) (client.PingResult, error) {
	if m.PingFn != nil {
		return m.PingFn(ctx, opts)
	}
	return client.PingResult{
		APIVersion: "1.44",
		OSType:     "linux",
	}, nil
}

func InstallFailFastGet() { docker.Get = func() docker.Client { return &failFastDockerClient{} } }

// failFastDockerClient is installed by InstallFailFastGet under tests; any method
// call panics to signal a test reached the real docker client without opting in.
type failFastDockerClient struct{}

func (f *failFastDockerClient) mustMock() {
	panic("docker.Get reached the real Docker client; install dockertest.WithDockerMock(t, ...) in the test")
}

func (f *failFastDockerClient) ImageBuild(
	_ context.Context,
	_ io.Reader,
	_ client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	f.mustMock()
	return client.ImageBuildResult{}, nil
}

func (f *failFastDockerClient) ImageInspect(
	_ context.Context,
	_ string,
	_ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	f.mustMock()
	return client.ImageInspectResult{}, nil
}

func (f *failFastDockerClient) ImageList(
	_ context.Context,
	_ client.ImageListOptions,
) (client.ImageListResult, error) {
	f.mustMock()
	return client.ImageListResult{}, nil
}

func (f *failFastDockerClient) ImageSave(
	_ context.Context,
	_ []string,
	_ ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastDockerClient) ImagePull(
	_ context.Context,
	_ string,
	_ client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastDockerClient) ImagePrune(
	_ context.Context,
	_ client.ImagePruneOptions,
) (client.ImagePruneResult, error) {
	f.mustMock()
	return client.ImagePruneResult{}, nil
}

func (f *failFastDockerClient) Ping(_ context.Context, _ client.PingOptions) (client.PingResult, error) {
	f.mustMock()
	return client.PingResult{}, nil
}
