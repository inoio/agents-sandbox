package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

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

func ReferencesDindBase(dockerfile []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if strings.HasPrefix(line, "FROM") && strings.Contains(line, DindBaseTag) {
			return true
		}
	}
	return false
}

func ImageTag(projectSlug, imageDigest string) string {
	return imagePrefix + projectSlug + ":" + git.HashID(imageDigest)
}

func runnerTag(projectSlug string) string {
	return imagePrefix + projectSlug + ":latest"
}

// envDir returns the project-local metadata directory for image env info.
func envDir() string {
	return ProjectDir
}

// envMetaFile returns the JSON file path for image env metadata, keyed by the
// stable tag so the data survives image rebuilds and docker prune.
func envMetaFile(tag string) string {
	return filepath.Join(envDir(), "image-env-"+tag+".json")
}

// storeImageEnv writes image env vars to a JSON file.
// Survives docker prune and image rebuilds.
func storeImageEnv(tag string, envs map[string]string) {
	if tag == "" || len(envs) == 0 {
		return
	}
	dir := envDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	data, _ := json.Marshal(envs)
	_ = os.WriteFile(envMetaFile(tag), data, 0o600)
}

// loadImageEnv reads a previously stored image env map from the cached JSON
// file keyed by tag. Returns nil if no file exists.
func loadImageEnv(tag string) map[string]string {
	path := envMetaFile(tag)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
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

// inspectExistingImage attempts to inspect the image on disk. If the image is
// missing it falls back to stored env metadata. Returns (imageEnvs, digest).
func inspectExistingImage(ctx context.Context, rTag string, ui termio.UI) (map[string]string, string) {
	imageEnv := make(map[string]string)
	var imageDigest string

	inspect, inspectErr := docker.Get().ImageInspect(ctx, rTag)
	if inspectErr != nil {
		ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
		if cached := loadImageEnv(rTag); cached != nil {
			imageEnv = cached
			ui.Verbosef("using stored image env metadata for %s", rTag)
		}
		return imageEnv, imageDigest
	}
	imageDigest = inspect.ID
	imageEnv = parseImageEnv(inspect.Config.Env)
	storeImageEnv(rTag, imageEnv)
	ui.Verbosef("inspected image %s with %d env vars", rTag, len(imageEnv))
	return imageEnv, imageDigest
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

	if err := docker.BuildDockerImage(ctx, dockerfile, rTag, "Building runner image", force, ui); err != nil {
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

	digestTag := ImageTag(projectSlug, imageDigest)
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

// ensureImageWithClient builds/inspects the runner Docker image using the
// provided clients. Tests inject mock clients to verify build behavior.
func ensureImageWithClient(
	ctx context.Context,
	mclient MsbClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui termio.UI,
) (string, string, map[string]string, error) {
	if force || ReferencesBase(dockerfile) || ReferencesDindBase(dockerfile) {
		if err := docker.BuildDockerImage(
			ctx,
			EmbeddedDockerfile,
			BaseTag,
			"Building base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building base image: %w", err)
		}
	}

	if ReferencesDindBase(dockerfile) {
		if err := docker.BuildDockerImage(
			ctx,
			EmbeddedDindDockerfile,
			DindBaseTag,
			"Building Docker-in-Docker base runner image",
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

	imageRef := ImageTag(projectSlug, imageDigest)

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
	dockerfile := resolveDockerfile()
	return ensureImageWithClient(ctx, msb.Get(), dockerfile, projectSlug, force, ui)
}

func parseImageEnv(envs []string) map[string]string {
	out := make(map[string]string, len(envs))
	for _, e := range envs {
		if i := strings.Index(e, "="); i > 0 {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}
