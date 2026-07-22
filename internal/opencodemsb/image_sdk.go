//go:build cgo

package opencodemsb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"

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

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	if ReferencesBase(dockerfile) {
		if _, _, err := EnsureImage(ctx, EmbeddedDockerfile, force); err != nil {
			return "", "", fmt.Errorf("ensure base image: %w", err)
		}
	}

	cli, err := client.New(client.FromEnv)
	if err != nil {
		return "", "", fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer cli.Close()

	buildTag := "opencode-msb/runner:latest"
	buildResp, err := cli.ImageBuild(ctx, dockerfileTar(dockerfile), client.ImageBuildOptions{
		Tags:   []string{buildTag},
		Remove: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("Docker image build failed: %w", err)
	}

	buildErr := scanBuildOutput(buildResp.Body)
	buildResp.Body.Close()
	if buildErr != nil {
		return "", "", fmt.Errorf("Docker image build failed: %w", buildErr)
	}

	inspect, err := cli.ImageInspect(ctx, buildTag)
	if err != nil {
		return "", "", fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest = inspect.ID
	imageRef = ImageTag(imageDigest)

	_, cacheErr := m.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	saveResult, err := cli.ImageSave(ctx, []string{buildTag})
	if err != nil {
		return "", "", fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	cmd := exec.CommandContext(ctx, "msb", "load", "--tag", imageRef)
	cmd.Stdin = saveResult
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("loading image into microsandbox failed: %w: %s", err, out)
	}

	return imageRef, imageDigest, nil
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
