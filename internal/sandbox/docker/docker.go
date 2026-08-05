package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
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

// BuildDockerImage builds a Docker image using the provided client and UI.
func BuildDockerImage(
	ctx context.Context,
	dockerfile []byte,
	tag, label string,
	force bool,
	ui stdio.UI,
) error {
	spinner := ui.Spinner(label)
	tarBuf, err := dockerfileTar(dockerfile)
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	moby := Get()
	buildResp, err := moby.ImageBuild(ctx, tarBuf, client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   force,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid()),
	})
	if err != nil {
		spinner.StopError(err)
		return fmt.Errorf("docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body, ui)
	_ = buildResp.Body.Close()
	if buildErr != nil {
		spinner.StopError(buildErr)
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("docker image build failed: %w", buildErr)
	}

	spinner.Stop()
	return nil
}

const dockerfileMode = 0o644

func dockerfileTar(dockerfile []byte) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: dockerfileMode,
		Size: int64(len(dockerfile)),
	}); err != nil {
		return nil, fmt.Errorf("tar write header: %w", err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(dockerfile)); err != nil {
		_ = tw.Close()
		return nil, fmt.Errorf("tar write dockerfile: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("tar close: %w", err)
	}
	return &buf, nil
}

// userBuildArgs returns Docker build arguments that align the in-image dev user
// (see data/Dockerfile's USER_UID/USER_GID) with the host user that owns the
// bind-mounted /workspace, avoiding permission mismatches inside the VM.
func userBuildArgs(uid, gid int) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID": &u,
		"USER_GID": &g,
	}
}

// scanBuildOutput decodes the Docker build API stream and returns errors.
func scanBuildOutput(r io.Reader, ui stdio.UI) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("unexpected Docker build output: %w", err)
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
		if msg.ErrorDetail.Message != "" {
			return fmt.Errorf("%s", msg.ErrorDetail.Message)
		}
		trimmedMsg := strings.Trim(msg.Stream, " \n")
		if trimmedMsg != "" {
			ui.Verbose(trimmedMsg)
		}
	}
}

// CheckDockerAPI pings the Docker daemon and reports reachability via the UI.
// Returns true if the daemon responds, false otherwise with error/info messages.
func CheckDockerAPI(ctx context.Context, ui stdio.UI) bool {
	moby := Get()
	//nolint:exhaustruct // NegotiateAPIVersion/ForceNegotiate not needed for a simple ping check
	_, err := moby.Ping(ctx, client.PingOptions{})
	if err != nil {
		ui.Errorf("Docker API unreachable: %v", err)
		ui.Infof("Ensure Docker Desktop or colima is running, or verify DOCKER_HOST.")
		return false
	}
	return true
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

// dockerBuildMessage represents a single line from the Docker build API
// streaming response, decoding both progress and error output.
// Docker returns either an ErrorDetail with a Message or an Error.
type dockerBuildMessage struct {
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error  string `json:"error"`
	Stream string `json:"stream"`
}
