package main

import (
	"context"
	"io"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

// FlagSet is a permutation of CLI flag arguments (one variation per test run).
type FlagSet []string

// stopKillFlags contains --force/--dry-run flag variations for the stop and kill
// commands. All combinations are valid and should produce the same behavior.
//
//nolint:gochecknoglobals // fixture data shared across parameterized tests
var stopKillFlags = []FlagSet{
	{"--force", "--dry-run"},
	{"-f", "-n"},
}

// pruneAgeFlags contains --age threshold variations for the prune command.
// All represent different valid age specifications that should produce
// the same command structure (only the value differs at this layer).
//
//nolint:gochecknoglobals // fixture data shared across parameterized tests
var pruneAgeFlags = []FlagSet{
	{"--age", "7d"},
	{"-a", "7d"},
	{"-a", "2w"},
	{"--age", "14d"},
}

// overrideMsbClient saves the original sandbox.NewMsbClient factory, replaces
// it with one that returns the provided mock, and restores the original on test
// cleanup. Callers should pass sandbox.NewMockMsbClient() as the mock argument.
//
// This is a t.Helper so its callsite is the one shown in stack traces.
func overrideMsbClient(t *testing.T, mock sandbox.MsbClient) {
	t.Helper()

	orig := sandbox.NewMsbClient

	sandbox.NewMsbClient = func() sandbox.MsbClient {
		return mock
	}

	t.Cleanup(func() {
		sandbox.NewMsbClient = orig
	})
}

// overrideDockerClient saves the original newDockerClient factory, replaces it
// with one that returns the provided mock DockerClient, and restores the
// original on test cleanup.
//
// This is a t.Helper so its callsite is the one shown in stack traces.
func overrideDockerClient(t *testing.T, mock sandbox.DockerClient) {
	t.Helper()

	orig := newDockerClient

	newDockerClient = func() (sandbox.DockerClient, error) {
		return mock, nil
	}

	t.Cleanup(func() {
		newDockerClient = orig
	})
}

// TestFixtureHelpers compiles-check that all fixture helpers and flags are
// accessible from a test context. No assertions are performed.
func TestFixtureHelpers(t *testing.T) {
	_ = FlagSet(nil)
	_ = stopKillFlags
	_ = pruneAgeFlags
	_ = overrideMsbClient
	_ = overrideDockerClient
	t.Log("fixture helpers compile-ok")
}

// mockDockerClient is the zero implementation of sandbox.DockerClient.
// All methods succeed with nil/empty returns. Use newErrorDockerClient
// to override specific methods with errors.
var _ sandbox.DockerClient = (*mockDockerClient)(nil)

type mockDockerClient struct {
	ImageBuildFn   func(ctx context.Context, r io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error)
	ImageInspectFn func(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageSaveFn    func(ctx context.Context, refs []string, opts ...client.ImageSaveOption) (client.ImageSaveResult, error)
	ImageRemoveFn  func(ctx context.Context, ref string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImageTagFn     func(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error)
	CloseFn        func() error
}

func newDefaultDockerClient() *mockDockerClient {
	return &mockDockerClient{}
}

//nolint:revive // callbacks always return errors; params required by interface signatures
func newErrorDockerClient(buildErr, inspectErr, saveErr, removeErr error) *mockDockerClient {
	return &mockDockerClient{
		ImageBuildFn: func(_ context.Context, _ io.Reader, opts client.ImageBuildOptions) (client.ImageBuildResult, error) {
			return client.ImageBuildResult{}, buildErr
		},
		ImageInspectFn: func(_ context.Context, _ string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, inspectErr
		},
		ImageSaveFn: func(_ context.Context, _ []string, opts ...client.ImageSaveOption) (client.ImageSaveResult, error) {
			return nil, saveErr
		},
		ImageRemoveFn: func(_ context.Context, _ string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			return client.ImageRemoveResult{}, removeErr
		},
	}
}

func (m *mockDockerClient) ImageBuild(
	ctx context.Context,
	r io.Reader,
	opts client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	if m.ImageBuildFn != nil {
		return m.ImageBuildFn(ctx, r, opts)
	}
	return client.ImageBuildResult{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (m *mockDockerClient) ImageInspect(
	ctx context.Context,
	ref string,
	opts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, ref, opts...)
	}
	result := client.ImageInspectResult{}
	result.Config = &dockerspec.DockerOCIImageConfig{}
	return result, nil
}

func (m *mockDockerClient) ImageSave(
	ctx context.Context,
	refs []string,
	opts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	if m.ImageSaveFn != nil {
		return m.ImageSaveFn(ctx, refs, opts...)
	}
	return io.NopCloser(nil), nil
}

func (m *mockDockerClient) ImageRemove(
	ctx context.Context,
	ref string,
	opts client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, ref, opts)
	}
	return client.ImageRemoveResult{}, nil
}

func (m *mockDockerClient) ImageTag(ctx context.Context, opts client.ImageTagOptions) (client.ImageTagResult, error) {
	if m.ImageTagFn != nil {
		return m.ImageTagFn(ctx, opts)
	}
	return client.ImageTagResult{}, nil
}

func (m *mockDockerClient) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}
