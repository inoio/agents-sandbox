//go:build cgo

package opencodemsb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/moby/moby/client"
	m "github.com/superradcompany/microsandbox/sdk/go"
)

type dockerBuildMessage struct {
	Stream      string `json:"stream"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Error string `json:"error"`
}

const runnerTag = "opencode-msb/runner:latest"

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	if ReferencesBase(dockerfile) {
		if err := buildDockerImage(ctx, EmbeddedDockerfile, BaseTag, "Building base runner image"); err != nil {
			return "", "", fmt.Errorf("building base image: %w", err)
		}
	}

	if err := buildDockerImage(ctx, dockerfile, runnerTag, "Building runner image"); err != nil {
		return "", "", err
	}

	cli, err := client.New(client.FromEnv)
	if err != nil {
		return "", "", fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer cli.Close()

	inspect, err := cli.ImageInspect(ctx, runnerTag)
	if err != nil {
		return "", "", fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest = inspect.ID
	imageRef = ImageTag(imageDigest)

	_, cacheErr := m.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	spin := startSpinner("Loading image into microsandbox")
	saveResult, err := cli.ImageSave(ctx, []string{runnerTag})
	if err != nil {
		spin.stopError(err)
		return "", "", fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
	cmd.Stdin = saveResult
	if out, err := cmd.CombinedOutput(); err != nil {
		spin.stopError(err)
		return "", "", fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}
	spin.stop()

	return imageRef, imageDigest, nil
}

func buildDockerImage(ctx context.Context, dockerfile []byte, tag, label string) error {
	spin := startSpinner(label)

	cli, err := client.New(client.FromEnv)
	if err != nil {
		spin.stopError(err)
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer cli.Close()

	buildResp, err := cli.ImageBuild(ctx, dockerfileTar(dockerfile), client.ImageBuildOptions{
		Tags:   []string{tag},
		Remove: true,
	})
	if err != nil {
		spin.stopError(err)
		return fmt.Errorf("Docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body)
	buildResp.Body.Close()
	if buildErr != nil {
		spin.stopError(buildErr)
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("Docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("Docker image build failed: %w", buildErr)
	}

	spin.stop()
	return nil
}

func scanBuildOutput(r io.Reader) error {
	dec := json.NewDecoder(r)
	for {
		var msg dockerBuildMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return nil
		}
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
		if msg.ErrorDetail.Message != "" {
			return fmt.Errorf("%s", msg.ErrorDetail.Message)
		}
	}
}
