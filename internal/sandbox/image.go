package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-msb/internal/git"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

const (
	BaseTag        = "opencode-msb/runner-base:latest"
	DindBaseTag    = "opencode-msb/runner-base-dind:latest"
	dockerfileMode = 0o644
)

// ensureImageDockerClient is the factory used by EnsureImage to create a Docker
// client. It can be overridden in tests to inject a mock client.
//
//nolint:gochecknoglobals // factory variable for test injection
var ensureImageDockerClient = func() (DockerClient, error) {
	return client.New(client.FromEnv)
}

// ReferencesBase reports whether the given Dockerfile references the base image.
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

// ReferencesDindBase reports whether the given Dockerfile references the dind base image.
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

// ImageTag returns the stable tag derived from a project slug and image digest.
func ImageTag(projectSlug, imageDigest string) string {
	return "opencode-msb/runner-" + projectSlug + ":" + git.HashID(imageDigest)
}

func userBuildArgs(uid, gid int) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID": &u,
		"USER_GID": &g,
	}
}

func runnerTag(projectSlug string) string {
	return "opencode-msb/runner-" + projectSlug + ":latest"
}

func envDir() string {
	return ".opencode-msb"
}

func envMetaFile(tag string) string {
	return filepath.Join(envDir(), "image-env-"+tag+".json")
}

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

func ensureRunnerImage(
	ctx context.Context,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	rTag := runnerTag(projectSlug)
	var imageEnv map[string]string
	var imageDigest string

	if force {
		imageEnv = make(map[string]string)
	} else {
		imageEnv, imageDigest = inspectExistingImage(ctx, cli, rTag, ui)
	}

	if imageEnv == nil {
		imageEnv = make(map[string]string)
	}

	return buildRunnerImage(ctx, cli, rTag, imageEnv, imageDigest, dockerfile, force, projectSlug, ui)
}

func inspectExistingImage(ctx context.Context, cli dockerClient, rTag string, ui stdio.UI) (map[string]string, string) {
	imageEnv := make(map[string]string)
	var imageDigest string

	_, inspectErr := cli.ImageInspect(ctx, rTag)
	if inspectErr != nil {
		ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
		if cached := loadImageEnv(rTag); cached != nil {
			imageEnv = cached
			ui.Verbosef("using stored image env metadata for %s", rTag)
		}
		return imageEnv, imageDigest
	}
	inspect, err := cli.ImageInspect(ctx, rTag)
	if err == nil {
		imageDigest = inspect.ID
		imageEnv = parseImageEnv(inspect.Config.Env)
		storeImageEnv(rTag, imageEnv)
		ui.Verbosef("inspected image %s with %d env vars", rTag, len(imageEnv))
	}
	return imageEnv, imageDigest
}

func buildRunnerImage(
	ctx context.Context,
	cli dockerClient,
	rTag string,
	imageEnv map[string]string,
	imageDigest string,
	dockerfile []byte,
	force bool,
	projectSlug string,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	if len(imageEnv) == 0 {
		imageEnv = make(map[string]string)
	}

	if err := buildDockerImage(ctx, cli, dockerfile, rTag, "Building runner image", force, ui); err != nil {
		if len(imageEnv) > 0 {
			ui.Warnf("build succeeded but inspect failed; using stored env metadata")
			return rTag, imageDigest, imageEnv, nil
		}
		return "", "", nil, err
	}
	inspect, err := cli.ImageInspect(ctx, rTag)
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest = inspect.ID
	imageEnv = parseImageEnv(inspect.Config.Env)
	storeImageEnv(rTag, imageEnv)
	ui.Verbosef("rebuilt image %s with %d env vars", rTag, len(imageEnv))

	digestTag := ImageTag(projectSlug, imageDigest)
	if digestTag != rTag {
		if _, err := cli.ImageTag(ctx, client.ImageTagOptions{Source: rTag, Target: digestTag}); err != nil {
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
	client msbClient,
	cli dockerClient,
	dockerfile []byte,
	projectSlug string,
	force bool,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	if force || ReferencesBase(dockerfile) || ReferencesDindBase(dockerfile) {
		if err := buildDockerImage(
			ctx,
			cli,
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
		if err := buildDockerImage(
			ctx,
			cli,
			EmbeddedDindDockerfile,
			DindBaseTag,
			"Building Docker-in-Docker base runner image",
			force,
			ui,
		); err != nil {
			return "", "", nil, fmt.Errorf("building dind base image: %w", err)
		}
	}

	rTag, imageDigest, imageEnv, err := ensureRunnerImage(ctx, cli, dockerfile, projectSlug, force, ui)
	if err != nil {
		return "", "", nil, err
	}

	imageRef := ImageTag(projectSlug, imageDigest)

	cacheErr := client.ImageGet(ctx, imageRef)
	if cacheErr == nil && !force {
		return imageRef, imageDigest, imageEnv, nil
	}

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := cli.ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return "", "", nil, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()

	if err := client.ImageLoad(ctx, imageRef, saveResult); err != nil {
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
// metadata written by a previous invokation.
func EnsureImage(
	ctx context.Context,
	projectSlug string,
	force bool,
	ui stdio.UI,
) (string, string, map[string]string, error) {
	dockerfile := resolveDockerfile()
	dockerCli, err := ensureImageDockerClient()
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	return ensureImageWithClient(ctx, newMsbClient(), dockerCli, dockerfile, projectSlug, force, ui)
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
