package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/output"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	BaseTag        = "opencode-msb/runner:base"
	shortDigestLen = 12
	dockerfileMode = 0o644
)

type dockerClient interface {
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
	Close() error
}

func ReferencesBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, BaseTag) {
			return true
		}
	}
	return false
}

func ImageTag(digest string) string {
	short := strings.TrimPrefix(digest, "sha256:")
	if len(short) > shortDigestLen {
		short = short[:shortDigestLen]
	}
	return "opencode-msb/runner:sha256-" + short
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

type dockerBuildMessage struct {
	Stream      string `json:"stream"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error string `json:"error"`
}

const runnerTag = "opencode-msb/runner:latest"

func EnsureImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	force bool,
	logger *output.Printer,
) (string, string, error) {
	if force || ReferencesBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDockerfile,
			BaseTag,
			"Building base runner image",
			force,
			logger,
		); err != nil {
			return "", "", fmt.Errorf("building base image: %w", err)
		}
	}

	if err := buildDockerImage(ctx, cli, dockerfile, runnerTag, "Building runner image", force, logger); err != nil {
		return "", "", err
	}

	inspect, err := cli.ImageInspect(ctx, runnerTag)
	if err != nil {
		return "", "", fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest := inspect.ID
	imageRef := ImageTag(imageDigest)

	_, cacheErr := msb.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	spin := output.NewSpinner(logger)
	spin.Start("Loading image into microsandbox")
	saveResult, err := cli.ImageSave(ctx, []string{runnerTag})
	if err != nil {
		spin.StopError(err)
		return "", "", fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
	cmd.Stdin = saveResult
	if out, err := cmd.CombinedOutput(); err != nil {
		spin.StopError(err)
		return "", "", fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	spin.Stop()

	return imageRef, imageDigest, nil
}

func buildDockerImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	tag, label string,
	force bool,
	logger *output.Printer,
) error {
	spin := output.NewSpinner(logger)
	spin.Start(label)

	buildResp, err := cli.ImageBuild(ctx, dockerfileTar(dockerfile), client.ImageBuildOptions{
		Tags:    []string{tag},
		Remove:  true,
		NoCache: force,
	})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body, logger)
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

func scanBuildOutput(r io.Reader, logger *output.Printer) error {
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
		if msg.Stream != "" {
			logger.Debugf("%s", strings.TrimSuffix(msg.Stream, "\n"))
		}
	}
}
