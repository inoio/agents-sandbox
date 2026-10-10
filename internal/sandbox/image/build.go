package image

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/distribution/reference"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// BuildOptions controls runner-image construction.
type BuildOptions struct {
	// Force rebuilds the image regardless of cache state.
	Force bool
	// Docker bakes the tool's Docker engine block when the image is built.
	Docker bool
	// AgentVersion pins the agent version baked into the image. When empty, the
	// latest release is resolved at build time unless UserProvided allows image
	// reuse to defer resolution until a rebuild is needed.
	AgentVersion string
	// UserProvided indicates that the selected agent was supplied by the
	// existing image provenance. When its version is unknown, image reuse keeps
	// a stable identity and release resolution is deferred until a rebuild is
	// needed.
	UserProvided bool
	// DryRun reports the build plan without booting the builder VM or
	// producing an archive.
	DryRun bool
}

// userProvidedImageIdentity keeps an unknown user-provided version stable
// without treating the identity as an installer release.
const userProvidedImageIdentity = "user-provided"

// ImageInfo describes the built or existing runner image. ArchivePath is set
// only when a build produced an archive this invocation; it is empty on a cache
// hit, where callers can skip loading into the microsandbox cache.
type ImageInfo struct {
	Tag         string
	Digest      string
	Env         map[string]string
	ArchivePath string
}

// referencesImage reports whether the Dockerfile uses the given image
// identifier as the base of its final (last) stage, ignoring any tag or digest
// on the reference. Multi-stage FROM lines are resolved through their declared
// stage aliases, so the image only counts if it ultimately backs the resulting
// image.
func referencesImage(dockerfile []byte, imageRef string) bool {
	lastImage, stageBase := scanFromStages(dockerfile)
	return imageRefMatches(resolveStageBase(lastImage, stageBase), imageRef)
}

// scanFromStages walks a Dockerfile's FROM lines, returning the final stage's
// image token and the declared stage-alias map.
func scanFromStages(dockerfile []byte) (string, map[string]string) {
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
	return lastImage, stageBase
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

const dockerfileMode = 0o644

// writeDockerfileContext writes the synthetic build context — the rendered
// Dockerfile followed by the profile.d merge script — into dir.
func writeDockerfileContext(dir string, dockerfile []byte) error {
	for _, entry := range []struct {
		name    string
		content []byte
	}{
		{"Dockerfile", dockerfile},
		{pathMergeAsset, pathMergeScript()},
	} {
		if err := os.WriteFile(filepath.Join(dir, entry.name), entry.content, dockerfileMode); err != nil {
			return fmt.Errorf("write %s: %w", entry.name, err)
		}
	}
	return nil
}

// buildRunnerImage assembles the synthetic build context, solves it through the
// BuildKit builder, and returns the produced archive with image metadata. The
// archive outlives the temporary context directory, which is removed here.
func buildRunnerImage(
	ctx context.Context,
	a agent.Agent,
	rendered []byte,
	rTag, dockerfileID string,
	noCache, dockerEnabled bool,
	agentVersion string,
	identity imageIdentity,
	ui termio.UI,
) (builder.BuildResult, error) {
	spinner := ui.Spinner("Ensuring runner image")

	contextDir, err := os.MkdirTemp("", "agents-sandbox-build-")
	if err != nil {
		spinner.StopError(err)
		return builder.BuildResult{}, fmt.Errorf("create build context: %w", err)
	}
	defer os.RemoveAll(contextDir)
	if err = writeDockerfileContext(contextDir, rendered); err != nil {
		spinner.StopError(err)
		return builder.BuildResult{}, err
	}

	ui.Verbosef("Building runner image (noCache=%v)", noCache)
	res, err := builder.Get().Build(ctx, builder.BuildRequest{
		ContextDir: contextDir,
		Tag:        rTag,
		BaseRef:    baseImageRef(rendered),
		BuildArgs: buildArgStrings(
			userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), agentVersion, dockerfileID, dockerEnabled, identity),
		),
		NoCache:  noCache,
		Platform: builder.HostPlatform(),
		Progress: func(line string) { ui.Verbose(line) },
	})
	if err != nil {
		spinner.StopError(err)
		return builder.BuildResult{}, err
	}
	spinner.Stop()
	return res, nil
}

// buildArgStrings dereferences a pointer-valued build-arg map, dropping nil
// entries, so it can be handed to the builder.
func buildArgStrings(args map[string]*string) map[string]string {
	out := make(map[string]string, len(args))
	for key, value := range args {
		if value != nil {
			out[key] = *value
		}
	}
	return out
}

// userBuildArgs returns build arguments that align the in-image dev user with
// the host user that owns the bind-mounted /workspace, pin the agent version,
// and carry the identity components recorded as image labels. BASE_IMAGE is
// supplied by the builder from the resolved base reference.
func userBuildArgs(
	uid, gid int,
	spec agent.ImageSpec,
	version, dockerfileID string,
	docker bool,
	identity imageIdentity,
) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	args := map[string]*string{
		"USER_UID":               &u,
		"USER_GID":               &g,
		spec.VersionArg:          &version,
		"DOCKERFILE_ID":          &dockerfileID,
		projectDockerfileHashArg: &identity.ProjectDockerfileHash,
		agentVersionArg:          &identity.AgentVersion,
		dockerArg:                &identity.Docker,
	}
	if docker {
		v := dockerVersion
		args["DOCKER_VERSION"] = &v
	}
	return args
}

// EnsureImageWithClient builds or inspects the runner image. A build returns the
// builder's archive for subsequent loading; a cache hit reads the digest and env
// from the microsandbox image cache. It does not load the image into
// microsandbox; callers load it lazily via EnsureLoaded when a VM needs it.
func EnsureImageWithClient(
	ctx context.Context,
	a agent.Agent,
	projectDockerfile []byte,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	agentVersion := buildOpts.AgentVersion
	var identityVersion string
	deferVersionResolution := buildOpts.UserProvided && agentVersion == ""
	if deferVersionResolution {
		identityVersion = userProvidedImageIdentity
	} else {
		var err error
		agentVersion, err = resolveAgentVersion(ctx, a, agentVersion)
		if err != nil {
			return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
		}
		identityVersion = agentVersion
	}

	rTag := runnerTag(projectSlug, a.Name())
	rendered, err := RenderDockerfile(a, projectDockerfile, buildOpts.Docker)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("render Dockerfile: %w", err)
	}
	dockerEnabled := effectiveDocker(projectDockerfile, buildOpts.Docker)
	identity := computeImageIdentity(projectDockerfile, identityVersion, dockerEnabled)
	dockerfileID := computeDockerfileID(rendered, identityVersion)

	needsBuild, reasons := buildDecision(ctx, rTag, buildOpts.Force, dockerfileID, identity)
	if needsBuild {
		if deferVersionResolution {
			agentVersion, err = resolveAgentVersion(ctx, a, "")
			if err != nil {
				return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
			}
			identityVersion = agentVersion
			identity = computeImageIdentity(projectDockerfile, identityVersion, dockerEnabled)
			dockerfileID = computeDockerfileID(rendered, identityVersion)
		}
		ui.Verbosef("rebuilding runner image %s: %s", rTag, strings.Join(reasons, "; "))
	} else {
		ui.Verbosef("reusing runner image %s: content identity unchanged", rTag)
	}
	ui.Verbosef(
		"Resolved agent version for agent %s to %s, requested %s",
		a.Name(),
		agentVersion,
		buildOpts.AgentVersion,
	)

	if needsBuild && buildOpts.DryRun {
		ui.Verbosef("dry-run: would build runner image %s", rTag)
		return ImageInfo{Tag: rTag, Digest: "", Env: nil, ArchivePath: ""}, nil
	}

	var info ImageInfo
	if needsBuild {
		res, buildErr := buildRunnerImage(
			ctx, a, rendered, rTag, dockerfileID, buildOpts.Force, dockerEnabled, agentVersion, identity, ui,
		)
		if buildErr != nil {
			return ImageInfo{}, buildErr
		}
		info = ImageInfo{Tag: rTag, Digest: res.Digest, Env: parseImageEnv(res.Env), ArchivePath: res.ArchivePath}
	} else {
		inspect, inspectErr := msb.Get().ImageInspect(ctx, rTag)
		if inspectErr != nil {
			return ImageInfo{}, fmt.Errorf("cannot inspect cached image: %w", inspectErr)
		}
		info = ImageInfo{Tag: rTag, Digest: inspect.Digest, Env: parseImageEnv(inspect.Env), ArchivePath: ""}
	}
	ui.Verbosef("image %s (digest %s)", rTag, info.Digest)
	return info, nil
}

// EnsureLoaded loads archivePath into the microsandbox cache under imageRef. An
// archive is always loaded when present, because the runner tag is stable
// across rebuilds and a same-tag cached image may hold stale content. Only when
// no archive is available (e.g. an earlier cache hit) does it fall back to a
// cache presence check, and it errors when the image is missing and there is
// nothing to load.
func EnsureLoaded(ctx context.Context, mclient msb.Client, archivePath, imageRef string, ui termio.UI) error {
	if archivePath == "" {
		if err := mclient.ImageGet(ctx, imageRef); err == nil {
			ui.Verbosef("microsandbox image %s is up to date", imageRef)
			return nil
		}
		return fmt.Errorf(
			"runner image %s is not in the microsandbox cache and no build archive is available to load",
			imageRef,
		)
	}
	ui.Verbosef("loading image %s into microsandbox from build archive", imageRef)

	spin := ui.Spinner("Loading image into microsandbox")
	archive, err := os.Open(archivePath)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("open image archive: %w", err)
	}
	defer func() { _ = archive.Close() }()
	if err := mclient.ImageLoad(ctx, imageRef, archive); err != nil {
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}

// EnsureImage builds/inspects the runner Docker image and returns an ImageInfo
// describing its tag, digest, and ENV map.
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
// (build/inspect) and EnsureLoaded (load into microsandbox), removing the
// builder's archive once it has been loaded. Preflight checks and dry-run
// handling are the caller's concern.
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
	if info.ArchivePath != "" {
		defer os.Remove(info.ArchivePath)
	}
	return EnsureLoaded(ctx, msb.Get(), info.ArchivePath, info.Tag, ui)
}

// baseImageRef returns the normalized image reference that backs the rendered
// Dockerfile's final stage, i.e., the FROM the builder resolves and pulls.
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
	lastImage, stageBase := scanFromStages(rendered)
	return resolveStageBase(lastImage, stageBase)
}
