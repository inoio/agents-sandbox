package image

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// BuildOptions controls runner-image construction.
type BuildOptions struct {
	// Force rebuilds the image regardless of cache state.
	Force bool
	// OpenCodeVersion pins the opencode version baked into the image. When
	// empty, the latest release is resolved at build time.
	OpenCodeVersion string
}

// ImageInfo describes the built or existing runner image.
//
//nolint:revive // unavoidable stutter: image.Info already exists (list.go)
type ImageInfo struct {
	Tag             string
	Digest          string
	OpenCodeVersion string
	Env             map[string]string
}

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

// ensureRunnerImage builds (or reuses) the runner Docker image and returns its
// tag and digest-based cache identity. Env and the opencode version are read
// separately from the msb image cache, not from Docker.
func ensureRunnerImage(
	ctx context.Context,
	dockerfile []byte,
	projectSlug string,
	force bool,
	openCodeVersion string,
	ui termio.UI,
) (string, string, error) {
	rTag := runnerTag(projectSlug)
	var imageDigest string
	if !force {
		imageDigest = inspectExistingImage(ctx, rTag, ui)
	}
	return buildRunnerImage(ctx, rTag, imageDigest, dockerfile, force, openCodeVersion, ui)
}

// buildDockerImage builds the given Dockerfile as a tag, wrapping the build
// with a spinner and verbose output.
func buildDockerImage(
	ctx context.Context,
	dockerfile []byte,
	tag, label string,
	force bool,
	openCodeVersion string,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	if err := buildImage(ctx, dockerfile, tag, force, openCodeVersion, line); err != nil {
		spinner.StopError(err)
		return err
	}
	spinner.Stop()
	return nil
}

// buildImage builds a Docker image via docker.Get().ImageBuild and reports each
// decoded build output line to the given callback.
func buildImage(
	ctx context.Context,
	dockerfile []byte,
	tag string,
	force bool,
	openCodeVersion string,
	line func(string),
) error {
	tarBuf, err := dockerfileTar(dockerfile)
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	buildResp, err := docker.Get().ImageBuild(ctx, tarBuf, client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   force,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid(), openCodeVersion),
	})
	if err != nil {
		return fmt.Errorf("docker image build failed: %w", err)
	}
	defer buildResp.Body.Close()

	buildErr := scanBuildOutput(buildResp.Body, line)
	if buildErr != nil {
		if strings.Contains(buildErr.Error(), "pull access denied") {
			return fmt.Errorf("docker image build failed (base image not found or not logged in): %w", buildErr)
		}
		return fmt.Errorf("docker image build failed: %w", buildErr)
	}

	return nil
}

// scanBuildOutput decodes the Docker build API stream and returns errors,
// forwarding each non-empty line of output to the given callback.
func scanBuildOutput(r io.Reader, line func(string)) error {
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
			line(trimmedMsg)
		}
	}
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
		_ = tw.Close()
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
// bind-mounted /workspace, and pin the opencode version baked into the image.
func userBuildArgs(uid, gid int, openCodeVersion string) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID":         &u,
		"USER_GID":         &g,
		"OPENCODE_VERSION": &openCodeVersion,
	}
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

// buildRunnerImage builds the runner image and returns the rTag and digest.
func buildRunnerImage(
	ctx context.Context,
	rTag string,
	imageDigest string, //nolint:unparam,staticcheck // digest refreshed by the rebuild's inspect below
	dockerfile []byte,
	force bool,
	openCodeVersion string,
	ui termio.UI,
) (string, string, error) {
	if err := buildDockerImage(ctx, dockerfile, rTag, "Ensuring runner image", force, openCodeVersion, ui); err != nil {
		return "", "", err
	}
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return "", "", fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest = inspect.ID //nolint:staticcheck // assignment refreshes the digest param above
	ui.Verbosef("rebuilt image %s (digest %s)", rTag, imageDigest)
	return rTag, imageDigest, nil
}

// EnsureImageWithClient builds/inspects the runner Docker image with the given
// clients. The baked opencode version (requested or latest) becomes a build
// arg, and the resulting env map + opencode version are read back from the msb
// image cache. Tests inject mock clients to verify behavior.
func EnsureImageWithClient(
	ctx context.Context,
	mclient msb.Client,
	dockerfile []byte,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	openCodeVersion, err := resolveOpenCodeVersion(ctx, buildOpts.OpenCodeVersion)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("resolve opencode version: %w", err)
	}

	if buildOpts.Force || referencesImage(dockerfile, naming.BaseTag) ||
		referencesImage(dockerfile, naming.DindBaseTag) {
		if buildErr := buildDockerImage(
			ctx,
			embeddedDockerfile,
			naming.BaseTag,
			"Ensuring base runner image",
			buildOpts.Force,
			openCodeVersion,
			ui,
		); buildErr != nil {
			return ImageInfo{}, fmt.Errorf("building base image: %w", buildErr)
		}
	}
	if referencesImage(dockerfile, naming.DindBaseTag) {
		if buildErr := buildDockerImage(
			ctx,
			embeddedDindDockerfile,
			naming.DindBaseTag,
			"Ensuring Docker-in-Docker base runner image",
			buildOpts.Force,
			openCodeVersion,
			ui,
		); buildErr != nil {
			return ImageInfo{}, fmt.Errorf("building dind base image: %w", buildErr)
		}
	}

	rTag, imageDigest, err := ensureRunnerImage(ctx, dockerfile, projectSlug, buildOpts.Force, openCodeVersion, ui)
	if err != nil {
		return ImageInfo{}, err
	}

	imageRef := imageTag(projectSlug, imageDigest)

	if cacheErr := mclient.ImageGet(ctx, imageRef); cacheErr == nil && !buildOpts.Force {
		env, version, readErr := readImageInfoFromMSB(ctx, mclient, imageRef)
		if readErr != nil {
			return ImageInfo{}, fmt.Errorf("inspect cached msb image: %w", readErr)
		}
		return ImageInfo{Tag: imageRef, Digest: imageDigest, OpenCodeVersion: version, Env: env}, nil
	}

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := docker.Get().ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return ImageInfo{}, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()
	if err = mclient.ImageLoad(ctx, imageRef, saveResult); err != nil {
		spin.StopError(err)
		return ImageInfo{}, err
	}
	spin.Stop()

	env, version, err := readImageInfoFromMSB(ctx, mclient, imageRef)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect loaded msb image: %w", err)
	}
	return ImageInfo{Tag: imageRef, Digest: imageDigest, OpenCodeVersion: version, Env: env}, nil
}

// EnsureImage builds/inspects the runner Docker image and returns an ImageInfo
// describing its tag, digest, baked opencode version, and ENV map (read from
// the msb image cache).
func EnsureImage(
	ctx context.Context,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	dockerfile := ResolveDockerfile()
	return EnsureImageWithClient(ctx, msb.Get(), dockerfile, projectSlug, buildOpts, ui)
}
