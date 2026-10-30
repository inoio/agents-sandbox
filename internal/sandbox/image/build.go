package image

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/docslink"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// BuildOptions controls runner-image construction.
type BuildOptions struct {
	// Force rebuilds the image regardless of cache state.
	Force bool
	// Docker is the resolved docker capability baked into the image when it is
	// built, and whether that value was set explicitly.
	Docker options.DockerSetting
	// AgentVersion pins the agent version baked into the image. When empty, the
	// latest release is resolved at build time unless UserProvided allows image
	// reuse to defer resolution until a rebuild is needed.
	AgentVersion string
	// UserProvided indicates that the selected agent was supplied by the
	// existing image provenance. When its version is unknown, image reuse keeps
	// a stable identity and release resolution is deferred until a rebuild is
	// needed.
	UserProvided bool
}

// userProvidedImageIdentity keeps an unknown user-provided version stable
// without treating the identity as an installer release.
const userProvidedImageIdentity = "user-provided"

// ImageInfo describes the built or existing runner image. DockerTag is the
// namespaced tag in the system Docker daemon; Ref is the reference the image
// carries in the microsandbox cache, resolved by EnsureLoaded. It is the
// content-addressed reference when the built image has an ID, otherwise the
// prefix-free name reference; EnsureLoaded may resolve it to whichever cached
// candidate already holds the content. Identity is the image's structured
// (slug, agent, contentID) identity — what the initial Ref is rendered from —
// so a caller never has to parse the reference it already knows the parts of.
// Digest is the content identity (Docker's image ID); DockerfileID is the
// source signature carried as the dockerfile-id label.
type ImageInfo struct {
	DockerTag    string
	Ref          string
	Identity     naming.ImageIdentity
	Digest       string
	DockerfileID string
	Env          map[string]string
	// AgentVersion is the agent release the image reports via its
	// agent-version label; empty for a missing or non-release label.
	AgentVersion string
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

// scanFromStages walks a Dockerfile's FROM instructions, returning the final
// stage's image token and the declared stage-alias map. Only real instruction
// starts are read, so a stage header is not found inside a heredoc body or a
// continuation line; keywords are matched case-insensitively, so lowercase and
// mixed-case FROM lines are recognised.
func scanFromStages(dockerfile []byte) (string, map[string]string) {
	stageBase := make(map[string]string)
	var lastImage string
	lines := dockerfileLines(dockerfile)
	for _, i := range instructionLineIndices(lines) {
		fromImage, stageAlias := parseFrom(string(lines[i]))
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
// aliases until it reaches the underlying image reference. A project
// Dockerfile controls its own aliases, so it can declare a cycle (a stage named
// after its own base image, or two stages that alias each other); the walk
// stops once a token repeats instead of following the cycle forever.
func resolveStageBase(token string, stageBase map[string]string) string {
	seen := make(map[string]struct{}, len(stageBase))
	for {
		if _, ok := seen[token]; ok {
			return token
		}
		seen[token] = struct{}{}
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
// and the declared stage alias (the "AS <name>" token, if present). The FROM and
// AS keywords are matched case-insensitively and leading build flags (e.g.
// --platform) are ignored. Trailing whitespace is trimmed so a CRLF line's CR
// does not leak into the alias. A line that is not a FROM instruction yields
// empty values.
func parseFrom(line string) (string, string) {
	keyword, rest := instruction(strings.TrimRight(line, " \t\r\n"))
	if keyword != fromInstruction {
		return "", ""
	}
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
// with a spinner and verbose output. noCache bypasses Docker's layer cache; it is
// independent of whether a build is needed at all.
func buildDockerImage(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	tag, label string,
	noCache bool,
	agentVersion string,
	baseImage string,
	dockerfileID string,
	dockerEnabled bool,
	identity imageIdentity,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	ui.Verbosef("Building Docker image (noCache=%v)", noCache)
	if err := buildImage(
		ctx,
		a,
		dockerfile,
		tag,
		noCache,
		agentVersion,
		baseImage,
		dockerfileID,
		dockerEnabled,
		identity,
		line,
	); err != nil {
		spinner.StopError(err)
		return err
	}
	spinner.Stop()
	return nil
}

// buildImage builds a Docker image via docker.Get().ImageBuild and reports each
// decoded build output line to the given callback. noCache bypasses Docker's layer
// cache; it is independent of whether a build is needed at all.
func buildImage(
	ctx context.Context,
	a agent.Agent,
	dockerfile []byte,
	tag string,
	noCache bool,
	agentVersion string,
	baseImage string,
	dockerfileID string,
	dockerEnabled bool,
	identity imageIdentity,
	line func(string),
) error {
	var tarBuf bytes.Buffer
	if err := writeDockerfileTar(&tarBuf, dockerfile); err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	buildResp, err := docker.Get().ImageBuild(ctx, &tarBuf, client.ImageBuildOptions{
		Tags:    []string{tag},
		Remove:  true,
		NoCache: noCache,
		BuildArgs: userBuildArgs(
			os.Getuid(),
			os.Getgid(),
			a.ImageSpec(),
			agentVersion,
			baseImage,
			dockerfileID,
			dockerEnabled,
			identity,
		),
	})
	if err != nil {
		return buildFailureError(err)
	}
	defer buildResp.Body.Close()

	buildErr := scanBuildOutput(buildResp.Body, line)
	if buildErr != nil {
		return buildFailureError(buildErr)
	}

	return nil
}

// buildFailureError classifies an engine build failure and tailors the message
// for the two a user can act on: a base image the engine could not resolve
// (Docker Engine reports "pull access denied"), and an image name with no
// registry that the engine refused to resolve at all — podman does this when it
// has no unqualified-search-registries, which Docker Engine never needs because
// it assumes Docker Hub. Both point at the troubleshooting section naming the
// remedies. Any other failure stays a plain wrap.
func buildFailureError(err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "pull access denied"):
		return fmt.Errorf(
			"docker image build failed (base image not found or not logged in): %w\n  Docs: %s",
			err,
			docslink.SectionURL("troubleshooting", "the-base-image-cannot-be-found"),
		)
	case isUnresolvedImageName(msg):
		return fmt.Errorf(
			"docker image build failed (the engine could not resolve an image name that has no registry): %w\n  Docs: %s",
			err,
			docslink.SectionURL("troubleshooting", "the-base-image-cannot-be-found"),
		)
	default:
		return fmt.Errorf("docker image build failed: %w", err)
	}
}

// isUnresolvedImageName reports whether the engine refused an unqualified image
// name for want of a registry to resolve it against.
func isUnresolvedImageName(msg string) bool {
	return strings.Contains(msg, "short-name") || strings.Contains(msg, "unqualified search registries")
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

// executableFileMode marks a build-context file that the image installs as a
// runnable command. Docker's COPY takes the installed file's permission bits
// from the context entry, so the mode has to be set here.
const executableFileMode = 0o755

// buildContextFile is one non-Dockerfile file in the synthetic build context.
type buildContextFile struct {
	name    string
	content []byte
	mode    int64
}

// buildContextFiles returns the synthetic build context's files in write order.
// It is the single source of truth for the context tar and the image identity,
// so a new asset is both shipped and identity-invalidating.
func buildContextFiles() []buildContextFile {
	return []buildContextFile{
		{pathMergeAsset, pathMergeScript(), dockerfileMode},
		{trustHelperAsset, trustHelperScript(), executableFileMode},
		{trustHelperLicenseAsset, trustHelperLicense(), dockerfileMode},
		{hostOpenerShimAsset, hostOpenerShimScript(), executableFileMode},
	}
}

// writeDockerfileTar writes the synthetic build context — the rendered
// Dockerfile followed by the build-context files — to w as a tar stream.
func writeDockerfileTar(w io.Writer, dockerfile []byte) error {
	tw := tar.NewWriter(w)
	if err := writeTarEntry(tw, "Dockerfile", dockerfile, dockerfileMode); err != nil {
		_ = tw.Close()
		return err
	}
	for _, file := range buildContextFiles() {
		if err := writeTarEntry(tw, file.name, file.content, file.mode); err != nil {
			_ = tw.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}
	return nil
}

// writeTarEntry writes one regular file to the context tar.
func writeTarEntry(tw *tar.Writer, name string, content []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(content)),
	}); err != nil {
		return fmt.Errorf("tar write header: %w", err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("tar write %s: %w", name, err)
	}
	return nil
}

// userBuildArgs returns Docker build arguments that align the in-image dev
// user with the host user that owns the bind-mounted /workspace, pin the agent
// version, record the base image provenance label, and carry the identity
// components recorded as image labels.
func userBuildArgs(
	uid, gid int,
	spec agent.ImageSpec,
	version, baseImage, dockerfileID string,
	docker bool,
	identity imageIdentity,
) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	args := map[string]*string{
		"USER_UID":               &u,
		"USER_GID":               &g,
		spec.VersionArg:          &version,
		"BASE_IMAGE":             &baseImage,
		"DOCKERFILE_ID":          &dockerfileID,
		projectDockerfileHashArg: &identity.ProjectDockerfileHash,
		agentVersionArg:          &identity.AgentVersion,
		dockerArg:                &identity.Docker,
		userArg:                  &identity.User,
	}
	if docker {
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
	EmitDockerfileDiagnostics(ui, DiagnoseDockerfile(projectDockerfile, buildOpts.Docker))

	agentVersion, identityVersion, deferResolve := initialBuildVersions(buildOpts)
	if !deferResolve {
		resolved, err := resolveAgentVersion(ctx, a, agentVersion)
		if err != nil {
			return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
		}
		agentVersion, identityVersion = resolved, resolved
	}

	plan, rTag, needsBuild, reasons, err := planRunnerImage(
		ctx, a, projectDockerfile, projectSlug, buildOpts.Docker, agentVersion, identityVersion, buildOpts.Force,
	)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("render Dockerfile: %w", err)
	}

	if needsBuild && deferResolve {
		resolved, resolveErr := resolveAgentVersion(ctx, a, "")
		if resolveErr != nil {
			return ImageInfo{}, fmt.Errorf("resolve agent version: %w", resolveErr)
		}
		agentVersion, identityVersion = resolved, resolved
		// Re-render so the resolved version becomes the agent stage's ARG
		// default (the deferred render above had no version).
		plan, err = renderBuildPlan(a, projectDockerfile, buildOpts.Docker, agentVersion, identityVersion)
		if err != nil {
			return ImageInfo{}, fmt.Errorf("render Dockerfile: %w", err)
		}
	}
	logBuildDecision(ui, rTag, needsBuild, reasons, a.Name(), agentVersion, buildOpts.AgentVersion)

	if needsBuild {
		if buildErr := buildFromPlan(ctx, a, plan, rTag, buildOpts, agentVersion, ui); buildErr != nil {
			return ImageInfo{}, buildErr
		}
	}

	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest := inspect.ID
	ui.Verbosef("image %s (digest %s)", rTag, imageDigest)

	var bakedVersion string
	if inspect.Config != nil {
		bakedVersion = agentReleaseFromLabels(inspect.Config.Labels)
	}

	env, err := readImageInfoFromDocker(ctx, rTag)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect built image: %w", err)
	}
	identity := naming.ImageIdentity{
		Slug:      projectSlug,
		Agent:     a.Name(),
		ContentID: imageContentID(imageDigest),
	}
	return ImageInfo{
		DockerTag:    rTag,
		Ref:          identity.Ref(),
		Identity:     identity,
		Digest:       imageDigest,
		DockerfileID: plan.dockerfileID,
		Env:          env,
		AgentVersion: bakedVersion,
	}, nil
}

// imageContentID derives the content id that keys the microsandbox reference
// from Docker's image ID: the sha256: prefix stripped and truncated to a short,
// filesystem-safe hex prefix. It is empty when the image ID is, so the caller
// falls back to the name reference rather than emitting a dangling suffix.
func imageContentID(imageID string) string {
	id := strings.ToLower(strings.TrimPrefix(imageID, "sha256:"))
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY,INTEGER_DECREMENT reason="only a 16-character id distinguishes the bounds, and there both > and >= (and 16 and 15) leave the same 16 characters; only 16 and 17 differ"
	if len(id) > 16 {
		id = id[:16]
	}
	return id
}

// imageRefCandidates returns the microsandbox references that may already hold
// info's content, in reuse-preference order: the content ref, the prefix-free
// name ref a 0.6.0 install cached under, and the legacy prefixed Docker-tag ref a
// pre-0.6 install cached under. The name ref comes from the image's structured
// identity, so a reference the launcher rendered is never parsed back; the
// Docker tag is carried as-is. Empty and duplicate refs are dropped.
func imageRefCandidates(info ImageInfo) []string {
	var candidates []string
	for _, ref := range []string{info.Ref, info.Identity.NameRef(), info.DockerTag} {
		if ref == "" || slices.Contains(candidates, ref) {
			continue
		}
		candidates = append(candidates, ref)
	}
	return candidates
}

// initialBuildVersions returns the requested agent version, the version baked
// into the image content identity, and whether release resolution is deferred.
// A user-provided agent with no recorded version defers resolution: the identity
// uses the user-provided marker and no release is resolved until a rebuild needs
// one.
func initialBuildVersions(buildOpts BuildOptions) (string, string, bool) {
	if buildOpts.UserProvided && buildOpts.AgentVersion == "" {
		return "", userProvidedImageIdentity, true
	}
	return buildOpts.AgentVersion, buildOpts.AgentVersion, false
}

// buildPlan is the rendered Dockerfile and the content identity it bakes.
type buildPlan struct {
	rendered     []byte
	identity     imageIdentity
	dockerfileID string
}

// renderBuildPlan renders the runner Dockerfile for the pinned agent version and
// derives the image content identity from the same inputs. The version is the
// agent stage's ARG default, so the rendered content changes with the version.
func renderBuildPlan(
	a agent.Agent,
	projectDockerfile []byte,
	docker options.DockerSetting,
	agentVersion, identityVersion string,
) (buildPlan, error) {
	rendered, err := RenderDockerfile(a, agentVersion, projectDockerfile, docker)
	if err != nil {
		return buildPlan{}, err
	}
	return buildPlan{
		rendered: rendered,
		identity: withHostUser(computeImageIdentity(
			projectDockerfile, identityVersion, effectiveDocker(projectDockerfile, docker),
		)),
		dockerfileID: computeDockerfileID(rendered, identityVersion),
	}, nil
}

// withHostUser returns identity with the current host user's uid/gid, which
// buildImage bakes into the dev user via the USER_UID/USER_GID build args.
func withHostUser(identity imageIdentity) imageIdentity {
	identity.User = hostUserIdentity(os.Getuid(), os.Getgid())
	return identity
}

// planRunnerImage renders the build plan and computes the local build decision,
// including whether the locally cached base image moved past the one the runner
// was built from. It performs no build and no network access.
func planRunnerImage(
	ctx context.Context,
	a agent.Agent,
	projectDockerfile []byte,
	projectSlug string,
	dockerFlag options.DockerSetting,
	agentVersion, identityVersion string,
	force bool,
) (buildPlan, string, bool, []string, error) {
	plan, err := renderBuildPlan(a, projectDockerfile, dockerFlag, agentVersion, identityVersion)
	if err != nil {
		return buildPlan{}, "", false, nil, err
	}
	plan.identity.BaseImage = localBaseImage(ctx, baseImageRef(plan.rendered))
	tag := runnerTag(projectSlug, a.Name())
	needsBuild, reasons := buildDecision(ctx, tag, force, plan.dockerfileID, plan.identity)
	return plan, tag, needsBuild, reasons, nil
}

// buildFromPlan builds the image described by plan, resolving the base digest
// for the provenance label first. A forced build (--rebuild) refreshes the base
// from its registry, so it is the way to pick up a newer published base.
func buildFromPlan(
	ctx context.Context,
	a agent.Agent,
	plan buildPlan,
	rTag string,
	buildOpts BuildOptions,
	agentVersion string,
	ui termio.UI,
) error {
	baseRef := baseImageRef(plan.rendered)
	baseDigest, err := resolveBaseDigest(ctx, baseRef, buildOpts.Force, ui)
	if err != nil {
		return fmt.Errorf("resolve base image %s: %w", baseRef, err)
	}
	return buildDockerImage(
		ctx, a, plan.rendered, rTag, "Ensuring runner image",
		buildOpts.Force, agentVersion, baseDigest, plan.dockerfileID, buildOpts.Docker.Enabled, plan.identity, ui,
	)
}

// logBuildDecision reports whether the image was rebuilt and which release
// version was resolved, at verbose level.
func logBuildDecision(
	ui termio.UI,
	rTag string,
	needsBuild bool,
	reasons []string,
	agentName, agentVersion, requestedVersion string,
) {
	if needsBuild {
		ui.Verbosef("rebuilding runner image %s: %s", rTag, strings.Join(reasons, "; "))
	} else {
		ui.Verbosef("reusing runner image %s: content identity unchanged", rTag)
	}
	ui.Verbosef(
		"Resolved agent version for agent %s to %s, requested %s",
		agentName,
		agentVersion,
		requestedVersion,
	)
}

// EnsureLoaded loads the runner image described by info into the microsandbox
// cache if it is not already the cached content, so the image can be used to
// create VMs, and returns info with Ref set to the reference that now holds that
// content. It is idempotent: it reuses the first candidate reference whose cache
// content matches (see imageRefCandidates for the content, name, and legacy
// prefixed refs), otherwise it loads the Docker image under info.Ref. The
// candidate set is derived from info.Identity, so a reference the launcher
// rendered is never parsed back. The comparison uses the content digest
// microsandbox records for the image, the same value as Docker's image ID; a
// cached image that records no digest is unknown content and is reloaded, never
// trusted via the source-signature dockerfile-id label. Loading directly avoids
// removing a manifest that an existing VM may still reference.
func EnsureLoaded(ctx context.Context, mclient msb.Client, info ImageInfo, ui termio.UI) (ImageInfo, error) {
	anyCandidateCached := false
	for _, candidate := range imageRefCandidates(info) {
		if mclient.ImageGet(ctx, candidate) != nil {
			continue
		}
		anyCandidateCached = true
		if cachedImageMatches(ctx, mclient, candidate, info) {
			info.Ref = candidate
			ui.Verbosef("microsandbox image %s is up to date", candidate)
			return info, nil
		}
	}
	if info.Ref == "" {
		info.Ref = info.DockerTag
	}
	loadReason := "not present in microsandbox cache"
	if anyCandidateCached {
		loadReason = "cached content differs from the Docker image"
	}
	ui.Verbosef("loading image %s into microsandbox: %s", info.Ref, loadReason)

	spin := ui.Spinner("Loading image into microsandbox")
	saveResult, err := docker.Get().ImageSave(ctx, []string{info.DockerTag})
	if err != nil {
		spin.StopError(err)
		return ImageInfo{}, fmt.Errorf("cannot export Docker image: %w", err)
	}
	defer saveResult.Close()
	if err = mclient.ImageLoad(ctx, info.Ref, saveResult); err != nil {
		spin.StopError(err)
		return ImageInfo{}, err
	}
	spin.Stop()
	return info, nil
}

// cachedImageMatches reports whether the microsandbox image under ref holds
// the same content as the Docker image described by info. Docker's image ID and
// the microsandbox config digest are the same content identity, so they are
// compared directly. A nil inspect result, or one that records no digest, is
// unknown content and never counts as a match: the dockerfile-id label is a
// source signature, not a content identity, and can match while the bytes
// differ (a floating base tag, or the dev user's uid/gid), so it must not stand
// in for the digest. The cost is one reload — a load records a digest, so the
// image matches next time.
func cachedImageMatches(ctx context.Context, mclient msb.Client, ref string, info ImageInfo) bool {
	mInspect, err := mclient.ImageInspect(ctx, ref)
	if err != nil {
		return false
	}
	return mInspect != nil && mInspect.Digest != "" && mInspect.Digest == info.Digest
}

// EnsureImage builds/inspects the runner Docker image and returns an ImageInfo
// describing the resulting image — its tag, reference, digest, content identity,
// baked agent release, and ENV map, read from the Docker image config. It does
// not load the image into microsandbox.
func EnsureImage(
	ctx context.Context,
	a agent.Agent,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	WarnIgnoredProjectConfig(ui)
	return EnsureImageWithClient(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
}

// Build ensures the runner image is built and loaded into the microsandbox
// cache, so it can be used to create VMs. It composes EnsureImageWithClient
// (build/inspect) and EnsureLoaded (load into microsandbox) and returns the
// resulting ImageInfo. Preflight checks (e.g., docker availability) and dry-run
// handling are the caller's concern.
func Build(
	ctx context.Context,
	a agent.Agent,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	WarnIgnoredProjectConfig(ui)
	info, err := EnsureImageWithClient(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
	if err != nil {
		return ImageInfo{}, err
	}
	return EnsureLoaded(ctx, msb.Get(), info, ui)
}

// baseImageRef returns the image reference the rendered Dockerfile's final stage
// builds from — the token the engine resolves for that FROM, through any stage
// aliases. It is deliberately returned unchanged: the launcher's base-digest
// lookup and the build must name the same image, and only the engine knows which
// registry a name without one maps to (podman's unqualified-search-registries,
// a registry mirror, or a prefix rewrite). Qualifying it here would send a user's
// own base to Docker Hub even when the engine resolves that name elsewhere. The
// tool-owned distro base is qualified where it is rendered (distroBaseRef).
func baseImageRef(rendered []byte) string {
	return finalStageToken(rendered)
}

// finalStageToken resolves the rendered Dockerfile's final stage FROM through
// any stage aliases to the underlying image reference.
func finalStageToken(rendered []byte) string {
	lastImage, stageBase := scanFromStages(rendered)
	return resolveStageBase(lastImage, stageBase)
}

// resolveBaseDigest returns "<ref>@sha256:<digest>" for the base image, pulling
// it first when not present locally, or always when refresh is set. The FROM
// itself stays a tag so it floats; the digest records what the image was built
// from and is compared with the locally cached base on later runs
// (changedHostReasons). The reference is pulled as given (the rendered FROM
// token, so the engine resolves build and lookup alike), but the returned label
// uses the familiar spelling, so the provenance label reads the way the user
// wrote the base.
//
// A failed refresh falls back to a locally cached base with a warning, so
// --rebuild still works offline. A failed pull is surfaced through
// buildFailureError: the engine delivers a registry-level failure both as the
// call error and as a message inside the stream (moby's Wait), and draining the
// stream without waiting would lose the latter and let the follow-up inspect
// report a misleading "No such image".
func resolveBaseDigest(ctx context.Context, baseRef string, refresh bool, ui termio.UI) (string, error) {
	if refresh {
		pullErr := pullBaseImage(ctx, baseRef)
		if pullErr == nil {
			return inspectPulledBase(ctx, baseRef)
		}
		local := localBaseImage(ctx, baseRef)
		if local == "" {
			return "", pullErr
		}
		ui.Warnf("could not refresh base image %s, building from the local copy: %v", baseRef, pullErr)
		return local, nil
	}
	if local := localBaseImage(ctx, baseRef); local != "" {
		return local, nil
	}
	ui.Verbosef("base image %s not present locally, pulling", baseRef)
	if err := pullBaseImage(ctx, baseRef); err != nil {
		return "", err
	}
	return inspectPulledBase(ctx, baseRef)
}

// localBaseImage returns the provenance value of the locally cached base image,
// or "" when it is not present. It never pulls.
func localBaseImage(ctx context.Context, baseRef string) string {
	inspect, err := docker.Get().ImageInspect(ctx, baseRef)
	if err != nil || inspect.ID == "" {
		return ""
	}
	return baseProvenance(baseRef, inspect.ID)
}

// pullBaseImage pulls baseRef and waits for the pull to finish.
func pullBaseImage(ctx context.Context, baseRef string) error {
	pull, err := docker.Get().ImagePull(ctx, baseRef, client.ImagePullOptions{})
	if err != nil {
		return buildFailureError(fmt.Errorf("pull base image %s: %w", baseRef, err))
	}
	waitErr := pull.Wait(ctx)
	_ = pull.Close()
	if waitErr != nil {
		return buildFailureError(fmt.Errorf("pull base image %s: %w", baseRef, waitErr))
	}
	return nil
}

// inspectPulledBase returns the provenance value of a just-pulled base image.
func inspectPulledBase(ctx context.Context, baseRef string) (string, error) {
	inspect, err := docker.Get().ImageInspect(ctx, baseRef)
	if err != nil {
		return "", fmt.Errorf("inspect pulled base image %s: %w", baseRef, err)
	}
	return baseProvenance(baseRef, inspect.ID), nil
}

// baseProvenance renders the base label value "<familiar ref>@<image id>".
func baseProvenance(baseRef, imageID string) string {
	return familiarImageRef(baseRef) + "@" + imageID
}

// familiarImageRef strips the registry from a normalized image reference so the
// recorded base label keeps the spelling a user knows (debian:trixie-slim rather
// than docker.io/library/debian:trixie-slim).
func familiarImageRef(ref string) string {
	parsed, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.FamiliarString(parsed)
}
