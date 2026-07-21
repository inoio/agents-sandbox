//go:build cgo

package opencodemsb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	m "github.com/superradcompany/microsandbox/sdk/go"
)

func EnsureImage(ctx context.Context, dockerfile []byte, force bool) (imageRef, imageDigest string, err error) {
	if ReferencesBase(dockerfile) {
		if _, _, err := EnsureImage(ctx, EmbeddedDockerfile, force); err != nil {
			return "", "", fmt.Errorf("ensure base image: %w", err)
		}
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", "", fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	buildTag := "opencode-msb/runner:latest"
	buildResp, err := cli.ImageBuild(ctx, bytes.NewReader(dockerfile), image.BuildOptions{
		Tags:   []string{buildTag},
		Remove: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("docker build: %w", err)
	}
	defer buildResp.Body.Close()
	io.Copy(io.Discard, buildResp.Body)

	inspect, _, err := cli.ImageInspectWithRaw(ctx, buildTag)
	if err != nil {
		return "", "", fmt.Errorf("docker inspect: %w", err)
	}
	imageDigest = inspect.ID
	imageRef = ImageTag(imageDigest)

	_, cacheErr := m.Image.Get(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, nil
	}

	tmpFile, err := os.CreateTemp("", "opencode-msb-image-*.tar")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	rc, err := cli.ImageSave(ctx, []string{buildTag})
	if err != nil {
		return "", "", fmt.Errorf("docker save: %w", err)
	}
	out, err := os.Create(tmpPath)
	if err != nil {
		rc.Close()
		return "", "", fmt.Errorf("open temp file for write: %w", err)
	}
	_, err = io.Copy(out, rc)
	rc.Close()
	out.Close()
	if err != nil {
		return "", "", fmt.Errorf("save image to temp file: %w", err)
	}

	if _, err := m.Image.Load(ctx, tmpPath, imageRef); err != nil {
		return "", "", fmt.Errorf("msb image load: %w", err)
	}

	return imageRef, imageDigest, nil
}
