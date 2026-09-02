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
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// BuildOptions controls runner-image construction.
type BuildOptions struct {
	// Force rebuilds the image regardless of cache state.
	Force bool
	// AgentVersion pins the agent version baked into the image. When empty,
	// the latest release is resolved at build time.
	AgentVersion string
	// Dind appends the tool's Docker-in-Docker block when the image is built.
	Dind bool
}

// ImageInfo describes the built or existing runner image.
//
//nolint:revive // unavoidable stutter: image.Info already exists (list.go)
type ImageInfo struct {
	Tag    string
	Digest string
	Env    map[string]string
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

// buildDockerImage builds the given Dockerfile as a tag, wrapping the build
// with a spinner and verbose output.
func buildDockerImage(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	tag, label string,
	force bool,
	agentVersion string,
	baseImage string,
	dind bool,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	ui.Verbosef("Building Docker image (force=%v)", force)
	if err := buildImage(ctx, a, dockerfile, tag, force, agentVersion, baseImage, dind, line); err != nil {
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
	baseImage string,
	dind bool,
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
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), agentVersion, baseImage, dind),
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

// userBuildArgs returns Docker build arguments that align the in-image dev
// user with the host user that owns the bind-mounted /workspace, pin the agent
// version, and record the base image provenance label.
func userBuildArgs(uid, gid int, spec agent.ImageSpec, version, baseImage string, dind bool) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	args := map[string]*string{
		"USER_UID":      &u,
		"USER_GID":      &g,
		spec.VersionArg: &version,
		"BASE_IMAGE":    &baseImage,
	}
	if dind {
		v := dockerVersion
		args["DOCKER_VERSION"] = &v
	}
	return args
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

// EnsureImageWithClient builds/inspects the runner Docker image. The resulting
// env map is read back from the Docker image config. It does not load the image
// into microsandbox; callers load it lazily via EnsureLoaded when a VM needs it.
func EnsureImageWithClient(
	ctx context.Context,
	a agent.Agent,
	projectDockerfile []byte,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	agentVersion, err := resolveAgentVersion(ctx, a, buildOpts.AgentVersion)
	ui.Verbosef(
		"Resolved agent version for agent %s to %s, requested %s",
		a.Name(),
		agentVersion,
		buildOpts.AgentVersion,
	)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
	}

	rTag := runnerTag(projectSlug, a.Name())
	rendered := RenderDockerfile(a, projectDockerfile, buildOpts.Dind)

	// Pre-redesign images lack the agent contract label; force one rebuild.
	force := buildOpts.Force || !imageHasAgentLabel(ctx, rTag, ui)

	baseRef := baseImageRef(rendered)
	baseDigest, err := resolveBaseDigest(ctx, baseRef, ui)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("resolve base image %s: %w", baseRef, err)
	}

	if buildErr := buildDockerImage(
		ctx, a, rendered, rTag, "Ensuring runner image",
		force, agentVersion, baseDigest, buildOpts.Dind, ui,
	); buildErr != nil {
		return ImageInfo{}, buildErr
	}

	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest := inspect.ID
	ui.Verbosef("rebuilt image %s (digest %s)", rTag, imageDigest)

	imageRef := rTag // the agent tag is the msb reference
	env, err := readImageInfoFromDocker(ctx, rTag)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect built image: %w", err)
	}
	return ImageInfo{Tag: imageRef, Digest: imageDigest, Env: env}, nil
}

// EnsureLoaded loads the runner image into the microsandbox cache if it is not
// already present, so the image can be used to create VMs. It is idempotent:
// when the image is already cached (ImageGet succeeds) it returns immediately.
func EnsureLoaded(ctx context.Context, mclient msb.Client, _, imageRef string, ui termio.UI) error {
	if err := mclient.ImageGet(ctx, imageRef); err == nil {
		return nil
	}

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := docker.Get().ImageSave(ctx, []string{imageRef})
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
// describing its tag, digest, and ENV map (read from the Docker image config).
// It does not load the image into microsandbox.
func EnsureImage(
	ctx context.Context,
	a agent.Agent,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	return EnsureImageWithClient(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
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
	info, err := EnsureImageWithClient(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
	if err != nil {
		return err
	}
	return EnsureLoaded(ctx, msb.Get(), projectSlug, info.Tag, ui)
}

// baseImageRef returns the normalized image reference that backs the rendered
// Dockerfile's final stage, i.e. the FROM the tool must inspect/pull for the
// base label.
func baseImageRef(rendered []byte) string {
	token := finalStageToken(rendered)
	if ref, err := reference.ParseNormalizedNamed(token); err == nil {
		return reference.FamiliarString(ref)
	}
	return token
}

// finalStageToken resolves the rendered Dockerfile's final stage FROM through
// any stage aliases to the underlying image reference.
func finalStageToken(rendered []byte) string {
	stageBase := make(map[string]string)
	var lastImage string
	scanner := bufio.NewScanner(bytes.NewReader(rendered))
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
	return resolveStageBase(lastImage, stageBase)
}

// imageHasAgentLabel reports whether the existing runner image carries the
// agent contract label. Pre-redesign images lack it and are force-rebuilt once.
func imageHasAgentLabel(ctx context.Context, rTag string, _ termio.UI) bool {
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return false
	}
	if inspect.Config == nil {
		return false
	}
	return inspect.Config.Labels[agentLabelKey] != ""
}

// resolveBaseDigest returns "<ref>@sha256:<digest>" for the base image, pulling
// it first when not present locally. The FROM itself stays a tag so it floats;
// the digest only records what the image was built from.
func resolveBaseDigest(ctx context.Context, baseRef string, ui termio.UI) (string, error) {
	inspect, err := docker.Get().ImageInspect(ctx, baseRef)
	if err != nil {
		ui.Verbosef("base image %s not present locally, pulling", baseRef)
		pull, pullErr := docker.Get().ImagePull(ctx, baseRef, client.ImagePullOptions{})
		if pullErr != nil {
			return "", fmt.Errorf("pull base image %s: %w", baseRef, pullErr)
		}
		if _, drainErr := io.Copy(io.Discard, pull); drainErr != nil {
			_ = pull.Close()
			return "", fmt.Errorf("pull base image %s: %w", baseRef, drainErr)
		}
		_ = pull.Close()
		inspect, err = docker.Get().ImageInspect(ctx, baseRef)
		if err != nil {
			return "", fmt.Errorf("inspect pulled base image %s: %w", baseRef, err)
		}
	}
	return baseRef + "@" + inspect.ID, nil
}
