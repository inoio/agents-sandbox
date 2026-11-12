package docker

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/moby/moby/client"
)

// Client is the exported interface for Docker API operations.
type Client interface {
	DialHijack(
		ctx context.Context,
		url, protocol string,
		meta map[string][]string,
	) (net.Conn, error)
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
	ImageList(
		ctx context.Context,
		opts client.ImageListOptions,
	) (client.ImageListResult, error)
	ImageSave(
		ctx context.Context,
		imageIDs []string,
		saveOpts ...client.ImageSaveOption,
	) (client.ImageSaveResult, error)
	ImagePull(
		ctx context.Context,
		ref string,
		opts client.ImagePullOptions,
	) (client.ImagePullResponse, error)
	ImagePrune(
		ctx context.Context,
		opts client.ImagePruneOptions,
	) (client.ImagePruneResult, error)
	Ping(ctx context.Context, opts client.PingOptions) (client.PingResult, error)
}

var Get = func() Client {
	return &realDockerClient{}
}

// RealClient returns a Client backed by the real moby client, bypassing the
// fail-fast Get installed under tests. Integration tests opt back into the real
// docker daemon with it.
//
//deadcode:ignore reason="integration tests opt back into the real docker daemon"
func RealClient() Client { return &realDockerClient{} }

var (
	mobyClient     *client.Client
	mobyClientOnce sync.Once
	errMobyClient  error
)

// ensureMobyClient lazily creates the shared moby client. It is a package var so
// a unit test can force the construction failure branch the real methods guard
// on, without a docker daemon.
var ensureMobyClient = func() error {
	mobyClientOnce.Do(func() {
		mobyClient, errMobyClient = client.New(clientOptions()...)
	})
	return errMobyClient
}

type realDockerClient struct{}

func (realDockerClient) DialHijack(
	ctx context.Context,
	url, protocol string,
	meta map[string][]string,
) (net.Conn, error) {
	if err := ensureMobyClient(); err != nil {
		return nil, err
	}
	return mobyClient.DialHijack(ctx, url, protocol, meta)
}

// ImageBuild sends the docker CLI credentials of every registry with the build,
// as docker build does for the classic builder.
func (realDockerClient) ImageBuild(
	ctx context.Context,
	buildContext io.Reader,
	options client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageBuildResult{}, err
	}
	options.AuthConfigs = buildRegistryAuths()
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

func (realDockerClient) ImageList(
	ctx context.Context,
	opts client.ImageListOptions,
) (client.ImageListResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImageListResult{}, err
	}
	return mobyClient.ImageList(ctx, opts)
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

// ImagePull sends the docker CLI credentials for the registry ref names, as
// docker pull does.
func (realDockerClient) ImagePull(
	ctx context.Context,
	ref string,
	opts client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	if err := ensureMobyClient(); err != nil {
		return nil, err
	}
	opts.RegistryAuth = pullRegistryAuth(ref)
	return mobyClient.ImagePull(ctx, ref, opts)
}

func (realDockerClient) ImagePrune(
	ctx context.Context,
	options client.ImagePruneOptions,
) (client.ImagePruneResult, error) {
	if err := ensureMobyClient(); err != nil {
		return client.ImagePruneResult{}, err
	}
	return mobyClient.ImagePrune(ctx, options)
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
