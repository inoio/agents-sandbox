package image

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
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
		plan = validBuildPlan(a, project, buildOpts.Docker, agentVersion, identityVersion)
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
	if err := project.reservedAliasError(); err != nil {
		return buildPlan{}, err
	}
	return validBuildPlan(a, project, docker, agentVersion, identityVersion), nil
}

// validBuildPlan is renderBuildPlan for a project Dockerfile without reserved
// stage aliases, which cannot fail (see renderValid).
func validBuildPlan(
	a agent.Agent,
	project parsedProjectDockerfile,
	docker options.DockerSetting,
	agentVersion, identityVersion string,
) buildPlan {
	rendered := project.renderValid(a, agentVersion, docker)
	return buildPlan{
		rendered: rendered,
		identity: withHostUser(computeImageIdentity(
			project.raw, identityVersion, project.effectiveDocker(docker),
		)),
		dockerfileID: computeDockerfileID(rendered, identityVersion),
		agentVersion: agentVersion,
	}
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
	client msb.ImageStore,
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
