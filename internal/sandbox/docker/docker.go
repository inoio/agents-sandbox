package docker

import (
	"context"
	"io"
	"sync"

	"github.com/moby/moby/client"
)

// Client is the exported interface for Docker API operations.
type Client interface {
	ImageBuild(
		ctx context.Context,
		buildContext io.Reader,
		options client.ImageBuildOptions,
	) (client.ImageBuildResult, error)
	ImageInspect(
		ctx context.Context,
		imageID string,
		inspectOpts ...client.ImageInspectOption,
	) (client.ImageInspectResult, error)
	ImageSave(
		ctx context.Context,
		imageIDs []string,
		saveOpts ...client.ImageSaveOption,
	) (client.ImageSaveResult, error)
	ImageRemove(
		ctx context.Context,
		imageID string,
		opts client.ImageRemoveOptions,
	) (client.ImageRemoveResult, error)
	ImageTag(
		ctx context.Context,
		opts client.ImageTagOptions,
	) (client.ImageTagResult, error)
	Ping(ctx context.Context, opts client.PingOptions) (client.PingResult, error)
}

//nolint:gochecknoglobals // test hook for the otherwise unmockable docker client
var Get = func() Client {
	return &realDockerClient{}
}

//nolint:gochecknoglobals // needed for lazy, thread-safe Docker client init
var (
	mobyClient     *client.Client
	mobyClientOnce sync.Once
	errMobyClient  error
)

func ensureMobyClient() error {
	mobyClientOnce.Do(func() {
		mobyClient, errMobyClient = client.New(client.FromEnv)
	})
	return errMobyClient
}

type realDockerClient struct{}

func (realDockerClient) ImageBuild(
	ctx context.Context,
	buildContext io.Reader,
	options client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageBuildResult{}, err
	}
	return mobyClient.ImageBuild(ctx, buildContext, options)
}

func (realDockerClient) ImageInspect(
	ctx context.Context,
	imageID string,
	inspectOpts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageInspectResult{}, err
	}
	return mobyClient.ImageInspect(ctx, imageID, inspectOpts...)
}

func (realDockerClient) ImageSave(
	ctx context.Context,
	imageIDs []string,
	saveOpts ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	if err := ensureMobyClient(); err != nil {
		return nil, err
	}
	return mobyClient.ImageSave(ctx, imageIDs, saveOpts...)
}

func (realDockerClient) ImageRemove(
	ctx context.Context,
	imageID string,
	options client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageRemoveResult{}, err
	}
	return mobyClient.ImageRemove(ctx, imageID, options)
}

func (realDockerClient) ImageTag(
	ctx context.Context,
	options client.ImageTagOptions,
) (client.ImageTagResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageTagResult{}, err
	}
	return mobyClient.ImageTag(ctx, options)
}

func (realDockerClient) Ping(
	ctx context.Context,
	opts client.PingOptions,
) (client.PingResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.PingResult{}, err
	}
	return mobyClient.Ping(ctx, opts)
}
