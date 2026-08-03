package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	sysio "io"
	"os"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// DockerClient is the exported interface for Docker API operations needed
// by both the prune and build commands. It lets CLI code create and pass a
// Docker client for pruning or building without depending directly on the
// moby client package.
type DockerClient interface {
	ImageBuild(
		ctx context.Context,
		buildContext sysio.Reader,
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
	Close() error
}

type dockerClient DockerClient

// dockerBuildMessage represents a single line from the Docker build API
// streaming response, decoding both progress and error output.
type dockerBuildMessage struct {
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error string `json:"error"`
	Stream      string `json:"stream"`
}

func dockerfileTar(dockerfile []byte) *bytes.Buffer {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: dockerfileMode,
		Size: int64(len(dockerfile)),
	})
	_, _ = tw.Write(dockerfile)
	_ = tw.Close()
	return &buf
}

func buildDockerImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	tag, label string,
	force bool,
	ui stdio.UI,
) error {
	spin := ui.Spinner(label)

	buildResp, err := cli.ImageBuild(ctx, dockerfileTar(dockerfile), client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   force,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid()),
	})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body, ui)
	_ = buildResp.Body.Close()
	if buildErr != nil {
		spin.StopError(buildErr)
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("docker image build failed: %w", buildErr)
	}

	spin.Stop()
	return nil
}

func scanBuildOutput(r sysio.Reader, ui stdio.UI) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == sysio.EOF {
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
