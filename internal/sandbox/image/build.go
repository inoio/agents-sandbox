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

// buildDockerImage builds the given Dockerfile as a tag, wrapping the build
// with a spinner and verbose output.
func buildDockerImage(
	ctx context.Context,
	dockerfile []byte,
	tag, label string,
	force bool,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	if err := buildImage(ctx, dockerfile, tag, force, line); err != nil {
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
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid()),
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
// bind-mounted /workspace, avoiding permission mismatches inside the VM.
func userBuildArgs(uid, gid int) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID": &u,
		"USER_GID": &g,
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

	if err := buildDockerImage(ctx, dockerfile, rTag, "Ensuring runner image", force, ui); err != nil {
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
		if err := buildDockerImage(
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
		if err := buildDockerImage(
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
