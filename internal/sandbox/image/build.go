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

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
	"github.com/inoio/opencode-sandbox/internal/termio"
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
	AgentVersion    string
	Env             map[string]string
}

// referencesImage reports whether the Dockerfile uses the given image
// identifier as the base of its final (last) stage, ignoring any tag or digest
// on the reference. Multi-stage FROM lines are resolved through their declared
// stage aliases, so the image only counts if it ultimately backs the resulting
// image.
func referencesImage(dockerfile []byte, imageRef string) bool {
	stageBase := make(map[string]string)
	var lastImage string
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if !strings.HasPrefix(line, "FROM") {
			continue
		}
		fromImage, stageAlias := parseFrom(line)
		if fromImage == "" {
			continue
		}
		if stageAlias != "" {
			stageBase[stageAlias] = fromImage
		}
		lastImage = fromImage
	}
	return imageRefMatches(resolveStageBase(lastImage, stageBase), imageRef)
}

// resolveStageBase follows a FROM image token through any declared stage
// aliases until it reaches the underlying image reference.
func resolveStageBase(token string, stageBase map[string]string) string {
	for {
		base, ok := stageBase[token]
		if !ok {
			return token
		}
		token = base
	}
}

// imageRefMatches reports whether the given image token refers to imageRef,
// ignoring any tag or digest on the token.
func imageRefMatches(token, imageRef string) bool {
	ref, err := reference.ParseNormalizedNamed(token)
	if err != nil {
		return false
	}
	return reference.FamiliarName(ref) == imageRef
}

// parseFrom parses a Dockerfile FROM line, returning the main image reference
// and the declared stage alias (the "AS <name>" token, if present). Leading
// build flags (e.g. --platform) are ignored.
func parseFrom(line string) (string, string) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "FROM"))
	var fromImage string
	for rest != "" {
		token, tail := nextField(rest)
		rest = tail
		if strings.HasPrefix(token, "--") {
			continue
		}
		if fromImage == "" {
			fromImage = token
			continue
		}
		if strings.EqualFold(token, "AS") {
			if alias, _ := nextField(rest); alias != "" {
				return fromImage, alias
			}
		}
	}
	return fromImage, ""
}

// nextField splits off the first whitespace-delimited field of s, returning the
// field and the remainder of the string.
func nextField(s string) (string, string) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i+1:], " \t")
}

// ensureRunnerImage builds (or reuses) the runner Docker image and returns its
// tag and digest-based cache identity. Env and the opencode version are read
// separately from the msb image cache, not from Docker.
func ensureRunnerImage(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	projectSlug string,
	force bool,
	agentVersion string,
	ui termio.UI,
) (string, string, error) {
	rTag := runnerTag(projectSlug)
	var imageDigest string
	if !force {
		imageDigest = inspectExistingImage(ctx, rTag, ui)
	}
	return buildRunnerImage(ctx, a, rTag, imageDigest, dockerfile, force, agentVersion, ui)
}

// buildDockerImage builds the given Dockerfile as a tag, wrapping the build
// with a spinner and verbose output.
func buildDockerImage(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	tag, label string,
	force bool,
	agentVersion string,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	if err := buildImage(ctx, a, dockerfile, tag, force, agentVersion, line); err != nil {
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
	a agent.Agent,
	dockerfile []byte,
	tag string,
	force bool,
	agentVersion string,
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
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), agentVersion),
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
// bind-mounted /workspace, and pin the agent version baked into the image.
func userBuildArgs(uid, gid int, spec agent.ImageSpec, version string) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	return map[string]*string{
		"USER_UID":      &u,
		"USER_GID":      &g,
		spec.VersionArg: &version,
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
	a agent.Agent,
	rTag string,
	imageDigest string, //nolint:unparam,staticcheck // digest refreshed by the rebuild's inspect below
	dockerfile []byte,
	force bool,
	agentVersion string,
	ui termio.UI,
) (string, string, error) {
	if err := buildDockerImage(ctx, a, dockerfile, rTag, "Ensuring runner image", force, agentVersion, ui); err != nil {
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

// EnsureImageWithClient builds/inspects the runner Docker image. The resulting
// env map and opencode version are read back from the Docker image config. It
// does not load the image into microsandbox; callers load it lazily via
// EnsureLoaded when a VM needs it.
func EnsureImageWithClient(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	agentVersion, err := resolveAgentVersion(ctx, a, buildOpts.OpenCodeVersion)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
	}

	if buildOpts.Force || referencesImage(dockerfile, naming.BaseImagePrefix) ||
		referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		if buildErr := buildDockerImage(
			ctx,
			a,
			DockerfileFromImageSpec(a.ImageSpec()),
			naming.BaseTag,
			"Ensuring base runner image",
			buildOpts.Force,
			agentVersion,
			ui,
		); buildErr != nil {
			return ImageInfo{}, fmt.Errorf("building base image: %w", buildErr)
		}
	}
	if referencesImage(dockerfile, naming.BaseDindImagePrefix) {
		if buildErr := buildDockerImage(
			ctx,
			a,
			embeddedDindDockerfile,
			naming.DindBaseTag,
			"Ensuring Docker-in-Docker base runner image",
			buildOpts.Force,
			agentVersion,
			ui,
		); buildErr != nil {
			return ImageInfo{}, fmt.Errorf("building dind base image: %w", buildErr)
		}
	}

	rTag, imageDigest, err := ensureRunnerImage(ctx, a, dockerfile, projectSlug, buildOpts.Force, agentVersion, ui)
	if err != nil {
		return ImageInfo{}, err
	}

	imageRef := imageTag(projectSlug, imageDigest)

	env, version, err := readImageInfoFromDocker(ctx, rTag, a.ImageSpec().VersionLabel)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect built image: %w", err)
	}
	return ImageInfo{
		Tag:             imageRef,
		Digest:          imageDigest,
		OpenCodeVersion: version,
		AgentVersion:    version,
		Env:             env,
	}, nil
}

// EnsureLoaded loads the runner image into the microsandbox cache if it is not
// already present, so the image can be used to create VMs. It is idempotent:
// when the image is already cached (ImageGet succeeds) it returns immediately.
func EnsureLoaded(ctx context.Context, mclient msb.Client, projectSlug, imageRef string, ui termio.UI) error {
	if err := mclient.ImageGet(ctx, imageRef); err == nil {
		return nil
	}

	// Docker tags the runner image as ":latest"; the digest-derived imageRef is
	// the microsandbox-side alias. Export the Docker image by its runner tag.
	rTag := runnerTag(projectSlug)

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := docker.Get().ImageSave(ctx, []string{rTag})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()
	if err = mclient.ImageLoad(ctx, imageRef, saveResult); err != nil {
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}

// EnsureImage builds/inspects the runner Docker image and returns an ImageInfo
// describing its tag, digest, baked opencode version, and ENV map (read from
// the Docker image config). It does not load the image into microsandbox.
func EnsureImage(
	ctx context.Context,
	a agent.Agent,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	dockerfile := ResolveRunnerDockerfile(a)
	return EnsureImageWithClient(ctx, a, dockerfile, projectSlug, buildOpts, ui)
}

// Build ensures the runner image is built and loaded into the microsandbox
// cache, so it can be used to create VMs. It composes EnsureImageWithClient
// (build/inspect) and EnsureLoaded (load into microsandbox). Preflight checks
// (e.g. docker availability) and dry-run handling are the caller's concern.
func Build(
	ctx context.Context,
	a agent.Agent,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) error {
	info, err := EnsureImageWithClient(ctx, a, ResolveRunnerDockerfile(a), projectSlug, buildOpts, ui)
	if err != nil {
		return err
	}
	return EnsureLoaded(ctx, msb.Get(), projectSlug, info.Tag, ui)
}
