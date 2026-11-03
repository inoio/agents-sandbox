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
	"github.com/inoio/agents-sandbox/internal/configpaths"
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
	// NoRebuild reuses an existing image even when its inputs changed; a
	// missing image is still built.
	NoRebuild bool
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
	// ResolveVersion resolves the agent release to bake when AgentVersion is
	// empty. It is injected at the boundary so the image package holds no
	// process-global test seam; nil uses DefaultAgentVersionResolver.
	ResolveVersion AgentVersionResolver
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

// scanFromStages walks a Dockerfile's FROM instructions, returning the final
// stage's image token and the declared stage-alias map. Only real instruction
// starts are read, so a stage header is not found inside a heredoc body or a
// continuation line; keywords are matched case-insensitively, so lowercase and
// mixed-case FROM lines are recognised.
func scanFromStages(dockerfile []byte) (string, map[string]string) {
	lines := dockerfileLines(dockerfile)
	return scanFromStagesFrom(lines, instructionLineIndices(lines))
}

// scanFromStagesFrom is scanFromStages over pre-lexed input, so a caller that
// already holds the line split and instruction indices (parseProjectDockerfile)
// does not re-lex. lines and instructionLines must come from the same Dockerfile.
func scanFromStagesFrom(lines [][]byte, instructionLines []int) (string, map[string]string) {
	stageBase := make(map[string]string)
	var lastImage string
	for _, i := range instructionLines {
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

// buildRequest is the single description of one runner-image build: the rendered
// plan (Dockerfile, content identity, dockerfile-id, and the resolved agent
// release baked as the installer ARG default), the resolved base digest recorded
// as provenance, and the resolved docker setting. Every build arg is derived from
// this one object instead of parallel positional values.
//
// The one resolved options.DockerSetting feeds two deliberate, different docker
// derivations, and they must not be "simplified" into each other: the
// DOCKER_VERSION arg (userBuildArgs) reads the raw docker.Enabled, while the
// IDENTITY_DOCKER label reads the effective value (effectiveDocker, recorded in
// plan.identity.Docker). They diverge whenever the setting is not explicit but the
// project Dockerfile's final-stage marker or managed docker base implies docker:
// then DOCKER_VERSION is absent while IDENTITY_DOCKER is "true". Unifying the two
// would change the build args.
//
// baseDigest and plan.identity.BaseImage are deliberately named apart: the former
// is the resolved `<ref>@<image id>` the build records, the latter is the locally
// cached base the build decision compares against.
type buildRequest struct {
	plan       buildPlan
	baseDigest string
	docker     options.DockerSetting
}

// buildDockerImage builds the described image as a tag, wrapping the build with a
// spinner and verbose output. noCache bypasses Docker's layer cache; it is
// independent of whether a build is needed at all.
func buildDockerImage(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	req buildRequest,
	tag, label string,
	noCache bool,
	ui termio.UI,
) error {
	spinner := ui.Spinner(label)
	line := func(s string) { ui.Verbose(s) }
	ui.Verbosef("Building Docker image (noCache=%v)", noCache)
	if err := buildImage(ctx, dclient, a, req, tag, noCache, line); err != nil {
		spinner.StopError(err)
		return err
	}
	spinner.Stop()
	return nil
}

// buildImage builds a Docker image via dclient.ImageBuild and reports each
// decoded build output line to the given callback. noCache bypasses Docker's layer
// cache; it is independent of whether a build is needed at all.
func buildImage(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	req buildRequest,
	tag string,
	noCache bool,
	line func(string),
) error {
	var tarBuf bytes.Buffer
	// gomutants:disable-next-line BRANCH_IF reason="writeDockerfileTar writes only into this in-memory bytes.Buffer, whose Write is documented to always return a nil error, so the call cannot fail here and the branch body never executes"
	if err := writeDockerfileTar(&tarBuf, req.plan.rendered); err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	buildResp, err := dclient.ImageBuild(ctx, &tarBuf, client.ImageBuildOptions{
		Tags:      []string{tag},
		Remove:    true,
		NoCache:   noCache,
		BuildArgs: userBuildArgs(os.Getuid(), os.Getgid(), a.ImageSpec(), req),
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
// components recorded as image labels. Every value comes from the request. The
// DOCKER_VERSION arg deliberately follows the request's raw docker.Enabled, not
// the identity's effective docker value (see buildRequest): the two differ for a
// non-explicit setting that the project Dockerfile nonetheless enables, and
// switching this to the effective value would change the build args.
func userBuildArgs(uid, gid int, spec agent.ImageSpec, req buildRequest) map[string]*string {
	u := strconv.Itoa(uid)
	g := strconv.Itoa(gid)
	identity := req.plan.identity
	version := req.plan.agentVersion
	args := map[string]*string{
		"USER_UID":               &u,
		"USER_GID":               &g,
		spec.VersionArg:          &version,
		"BASE_IMAGE":             &req.baseDigest,
		"DOCKERFILE_ID":          &req.plan.dockerfileID,
		projectDockerfileHashArg: &identity.ProjectDockerfileHash,
		agentVersionArg:          &identity.AgentVersion,
		dockerArg:                &identity.Docker,
		userArg:                  &identity.User,
	}
	if req.docker.Enabled {
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
// The Docker client is threaded in explicitly, matching EnsureLoaded.
func EnsureImageWithClient(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	projectDockerfile []byte,
	projectSlug string,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	project := parseProjectDockerfile(projectDockerfile)
	EmitDockerfileDiagnostics(ui, project.diagnostics(buildOpts.Docker))

	agentVersion, identityVersion, deferResolve := initialBuildVersions(buildOpts)
	if !deferResolve {
		resolved, err := buildOpts.resolveAgentVersion(ctx, a, agentVersion)
		if err != nil {
			return ImageInfo{}, fmt.Errorf("resolve agent version: %w", err)
		}
		agentVersion, identityVersion = resolved, resolved
	}

	plan, rTag, needsBuild, reasons, err := planRunnerImage(
		ctx, dclient, a, project, projectSlug, agentVersion, identityVersion, buildOpts,
	)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("render Dockerfile: %w", err)
	}

	if needsBuild && deferResolve {
		resolved, resolveErr := buildOpts.resolveAgentVersion(ctx, a, "")
		if resolveErr != nil {
			return ImageInfo{}, fmt.Errorf("resolve agent version: %w", resolveErr)
		}
		agentVersion, identityVersion = resolved, resolved
		// Re-render so the resolved version becomes the agent stage's ARG
		// default (the deferred render above had no version).
		plan, err = renderBuildPlan(a, project, buildOpts.Docker, agentVersion, identityVersion)
		if err != nil {
			return ImageInfo{}, fmt.Errorf("render Dockerfile: %w", err)
		}
	}
	logBuildDecision(ui, rTag, needsBuild, reasons, a.Name(), agentVersion, buildOpts.AgentVersion)

	if needsBuild {
		if buildErr := buildFromPlan(ctx, dclient, a, plan, rTag, buildOpts, ui); buildErr != nil {
			return ImageInfo{}, buildErr
		}
	}

	inspect, err := dclient.ImageInspect(ctx, rTag)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("cannot inspect built image: %w", err)
	}
	imageDigest := inspect.ID
	ui.Verbosef("image %s (digest %s)", rTag, imageDigest)

	var bakedVersion string
	if inspect.Config != nil {
		bakedVersion = agentReleaseFromLabels(inspect.Config.Labels)
	}

	env, err := readImageInfoFromDocker(ctx, dclient, rTag)
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
// one. An unknown version under NoRebuild defers too: a reused image never
// needs the release, and looking it up needs the network.
func initialBuildVersions(buildOpts BuildOptions) (string, string, bool) {
	if buildOpts.UserProvided && buildOpts.AgentVersion == "" {
		return "", userProvidedImageIdentity, true
	}
	if buildOpts.NoRebuild && buildOpts.AgentVersion == "" {
		return "", "", true
	}
	return buildOpts.AgentVersion, buildOpts.AgentVersion, false
}

// buildPlan is the rendered Dockerfile and the content identity it bakes,
// together with the resolved agent release that is the installer ARG's default.
type buildPlan struct {
	rendered     []byte
	identity     imageIdentity
	dockerfileID string
	agentVersion string
}

// renderBuildPlan renders the runner Dockerfile for the pinned agent version and
// derives the image content identity from the same parsed project Dockerfile.
// The version is the agent stage's ARG default, so the rendered content changes
// with the version.
func renderBuildPlan(
	a agent.Agent,
	project parsedProjectDockerfile,
	docker options.DockerSetting,
	agentVersion, identityVersion string,
) (buildPlan, error) {
	rendered, err := project.render(a, agentVersion, docker)
	if err != nil {
		return buildPlan{}, err
	}
	return buildPlan{
		rendered: rendered,
		identity: withHostUser(computeImageIdentity(
			project.raw, identityVersion, project.effectiveDocker(docker),
		)),
		dockerfileID: computeDockerfileID(rendered, identityVersion),
		agentVersion: agentVersion,
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
	dclient docker.Client,
	a agent.Agent,
	project parsedProjectDockerfile,
	projectSlug string,
	agentVersion, identityVersion string,
	buildOpts BuildOptions,
) (buildPlan, string, bool, []string, error) {
	plan, err := renderBuildPlan(a, project, buildOpts.Docker, agentVersion, identityVersion)
	if err != nil {
		return buildPlan{}, "", false, nil, err
	}
	plan.identity.BaseImage = localBaseImage(ctx, dclient, baseImageRef(plan.rendered))
	tag := runnerTag(projectSlug, a.Name())
	needsBuild, reasons := buildDecision(ctx, dclient, tag, buildOpts, plan.dockerfileID, plan.identity)
	return plan, tag, needsBuild, reasons, nil
}

// buildFromPlan builds the image described by plan, resolving the base digest
// for the provenance label first. A forced build (--rebuild) refreshes the base
// from its registry, so it is the way to pick up a newer published base.
func buildFromPlan(
	ctx context.Context,
	dclient docker.Client,
	a agent.Agent,
	plan buildPlan,
	rTag string,
	buildOpts BuildOptions,
	ui termio.UI,
) error {
	baseRef := baseImageRef(plan.rendered)
	baseDigest, err := resolveBaseDigest(ctx, dclient, baseRef, buildOpts.Force, ui)
	if err != nil {
		return fmt.Errorf("resolve base image %s: %w", baseRef, err)
	}
	return buildDockerImage(
		ctx, dclient, a,
		buildRequest{plan: plan, baseDigest: baseDigest, docker: buildOpts.Docker},
		rTag, "Ensuring runner image", buildOpts.Force, ui,
	)
}

// logBuildDecision reports whether the image was rebuilt and which release
// version was resolved, at verbose level.
func logBuildDecision(
	ui termio.Logger,
	rTag string,
	needsBuild bool,
	reasons []string,
	agentName, agentVersion, requestedVersion string,
) {
	switch {
	case needsBuild:
		ui.Verbosef("rebuilding runner image %s: %s", rTag, strings.Join(reasons, "; "))
	case len(reasons) > 0:
		ui.Infof("reusing runner image %s despite changes (--no-rebuild): %s", rTag, strings.Join(reasons, "; "))
	default:
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
func EnsureLoaded(
	ctx context.Context,
	dclient docker.Client,
	mclient msb.Client,
	info ImageInfo,
	ui termio.UI,
) (ImageInfo, error) {
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
	saveResult, err := dclient.ImageSave(ctx, []string{info.DockerTag})
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
// not load the image into microsandbox. The project Dockerfile's bytes are an
// explicit input (see ReadProjectDockerfile); the caller reads them once.
func EnsureImage(
	ctx context.Context,
	dclient docker.Client,
	paths configpaths.ConfigPaths,
	a agent.Agent,
	projectSlug string,
	projectDockerfile []byte,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	WarnIgnoredProjectConfig(paths, ui)
	return EnsureImageWithClient(ctx, dclient, a, projectDockerfile, projectSlug, buildOpts, ui)
}

// Build ensures the runner image is built and loaded into the microsandbox
// cache, so it can be used to create VMs. It composes EnsureImageWithClient
// (build/inspect) and EnsureLoaded (load into microsandbox) and returns the
// resulting ImageInfo. Preflight checks (e.g., docker availability) and dry-run
// handling are the caller's concern. The project Dockerfile's bytes are an
// explicit input (see ReadProjectDockerfile).
func Build(
	ctx context.Context,
	dclient docker.Client,
	client msb.Client,
	paths configpaths.ConfigPaths,
	a agent.Agent,
	projectSlug string,
	projectDockerfile []byte,
	buildOpts BuildOptions,
	ui termio.UI,
) (ImageInfo, error) {
	WarnIgnoredProjectConfig(paths, ui)
	info, err := EnsureImageWithClient(ctx, dclient, a, projectDockerfile, projectSlug, buildOpts, ui)
	if err != nil {
		return ImageInfo{}, err
	}
	return EnsureLoaded(ctx, dclient, client, info, ui)
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
func resolveBaseDigest(
	ctx context.Context,
	dclient docker.Client,
	baseRef string,
	refresh bool,
	ui termio.Logger,
) (string, error) {
	if refresh {
		pullErr := pullBaseImage(ctx, dclient, baseRef)
		if pullErr == nil {
			return inspectPulledBase(ctx, dclient, baseRef)
		}
		local := localBaseImage(ctx, dclient, baseRef)
		if local == "" {
			return "", pullErr
		}
		ui.Warnf("could not refresh base image %s, building from the local copy: %v", baseRef, pullErr)
		return local, nil
	}
	if local := localBaseImage(ctx, dclient, baseRef); local != "" {
		return local, nil
	}
	ui.Verbosef("base image %s not present locally, pulling", baseRef)
	if err := pullBaseImage(ctx, dclient, baseRef); err != nil {
		return "", err
	}
	return inspectPulledBase(ctx, dclient, baseRef)
}

// localBaseImage returns the provenance value of the locally cached base image,
// or "" when it is not present. It never pulls.
func localBaseImage(ctx context.Context, dclient docker.Client, baseRef string) string {
	inspect, err := dclient.ImageInspect(ctx, baseRef)
	if err != nil || inspect.ID == "" {
		return ""
	}
	return baseProvenance(baseRef, inspect.ID)
}

// pullBaseImage pulls baseRef and waits for the pull to finish.
func pullBaseImage(ctx context.Context, dclient docker.Client, baseRef string) error {
	pull, err := dclient.ImagePull(ctx, baseRef, client.ImagePullOptions{})
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
func inspectPulledBase(ctx context.Context, dclient docker.Client, baseRef string) (string, error) {
	inspect, err := dclient.ImageInspect(ctx, baseRef)
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
