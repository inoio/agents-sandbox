package image

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func referencesImage(dockerfile []byte, tag string) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

// ensureRunnerImage checks the runner Docker image on disk, loads stored env
// metadata as fallback when the image has been pruned, builds it if needed,
// and returns the tag, digest, and image env map.
func ensureRunnerImage(
	ctx context.Context,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui termio.UI,
) (string, string, map[string]string, error) {
	rTag := runnerTag(projectSlug)
	var imageEnv map[string]string
	var imageDigest string

	if force {
		imageEnv = make(map[string]string)
	} else {
		imageEnv, imageDigest = inspectExistingImage(ctx, rTag, ui)
	}

	if imageEnv == nil {
		imageEnv = make(map[string]string)
	}

	return buildRunnerImage(ctx, rTag, imageEnv, imageDigest, dockerfile, force, projectSlug, ui)
}

// buildRunnerImage builds the runner image and returns the rTag, digest, env map.
func buildRunnerImage(
	ctx context.Context,
	rTag string,
	imageEnv map[string]string,
	imageDigest string,
	dockerfile []byte,
	force bool,
	projectSlug string,
	ui termio.UI,
) (string, string, map[string]string, error) {
	if len(imageEnv) == 0 {
		imageEnv = make(map[string]string)
	}

	if err := docker.BuildDockerImage(ctx, dockerfile, rTag, "Ensuring runner image", force, ui); err != nil {
		if len(imageEnv) > 0 {
			ui.Warnf("build succeeded but inspect failed; using stored env metadata")
			return rTag, imageDigest, imageEnv, nil
		}
		return "", "", nil, err
	}
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest = inspect.ID
	imageEnv = parseImageEnv(inspect.Config.Env)
	storeImageEnv(rTag, imageEnv)
	ui.Verbosef("rebuilt image %s with %d env vars", rTag, len(imageEnv))

	digestTag := imageTag(projectSlug, imageDigest)
	if digestTag != rTag {
		if _, err := docker.Get().
			ImageTag(ctx, client.ImageTagOptions{Source: rTag, Target: digestTag}); err != nil {
			ui.Warnf("failed to tag image with digest: %v", err)
		} else {
			ui.Verbosef("tagged image with digest: %s", digestTag)
		}
	}

	return rTag, imageDigest, imageEnv, nil
}

// EnsureImageWithClient builds/inspects the runner Docker image using the
// provided clients. Tests inject mock clients to verify build behavior.
func EnsureImageWithClient(
	ctx context.Context,
	mclient msb.Client,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui termio.UI,
) (string, string, map[string]string, error) {
	if force || referencesImage(dockerfile, naming.BaseTag) || referencesImage(dockerfile, naming.DindBaseTag) {
		if err := docker.BuildDockerImage(
			ctx,
			embeddedDockerfile,
			naming.BaseTag,
			"Ensuring base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building base image: %w", err)
		}
	}

	if referencesImage(dockerfile, naming.DindBaseTag) {
		if err := docker.BuildDockerImage(
			ctx,
			embeddedDindDockerfile,
			naming.DindBaseTag,
			"Ensuring Docker-in-Docker base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building dind base image: %w", err)
		}
	}

	rTag, imageDigest, imageEnv, err := ensureRunnerImage(ctx, dockerfile, projectSlug, force, ui)
	if err != nil {
		return "", "", nil, err
	}

	imageRef := imageTag(projectSlug, imageDigest)

	cacheErr := mclient.ImageGet(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, imageEnv, nil
	}

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := docker.Get().ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return "", "", nil, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	if err := mclient.ImageLoad(ctx, imageRef, saveResult); err != nil {
		spin.StopError(err)
		return "", "", nil, err
	}
	spin.Stop()

	return imageRef, imageDigest, imageEnv, nil
}

// EnsureImage builds/inspects the runner Docker image and returns its tag,
// digest, and the Dockerfile ENV directives baked into the image config as a
// map. The env map is derived from cli.ImageInspect; if the image is no
// longer on disk (e.g. after `docker prune`), it falls back to stored JSON
// metadata written by a previous invocation.
func EnsureImage(
	ctx context.Context,
	projectSlug string,
	force bool,
	ui termio.UI,
) (string, string, map[string]string, error) {
	dockerfile := ResolveDockerfile()
	return EnsureImageWithClient(ctx, msb.Get(), dockerfile, projectSlug, force, ui)
}
