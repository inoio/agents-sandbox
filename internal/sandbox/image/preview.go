package image

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ImagePreview is a read-only description of what EnsureImage would do.
type ImagePreview struct {
	Tag                   string
	NeedsBuild            bool
	Reasons               []string
	AgentVersion          string
	ResolveVersionAtBuild bool
}

// previewRunnerImage renders the plan + decision for a supplied Dockerfile and
// decided version. It never builds and never resolves a version over the network.
func previewRunnerImage(
	ctx context.Context, a agent.Agent, projectDockerfile []byte, projectSlug string,
	buildOpts BuildOptions, ui termio.UI,
) (ImagePreview, error) {
	EmitDockerfileDiagnostics(ui, DiagnoseDockerfile(projectDockerfile, buildOpts.Docker))
	agentVersion, identityVersion, _ := initialBuildVersions(buildOpts)
	_, tag, needsBuild, reasons, err := planRunnerImage(
		ctx, a, projectDockerfile, projectSlug, buildOpts.Docker, agentVersion, identityVersion, buildOpts.Force,
	)
	if err != nil {
		return ImagePreview{}, err
	}
	return ImagePreview{
		Tag: tag, NeedsBuild: needsBuild, Reasons: reasons,
		AgentVersion: buildOpts.AgentVersion, ResolveVersionAtBuild: agentVersion == "",
	}, nil
}

// PreviewImage reports what EnsureImage would do without building anything.
func PreviewImage(
	ctx context.Context, a agent.Agent, projectSlug string, buildOpts BuildOptions, ui termio.UI,
) (ImagePreview, error) {
	WarnIgnoredProjectConfig(ui)
	return previewRunnerImage(ctx, a, readProjectDockerfile(), projectSlug, buildOpts, ui)
}

// RunnerDockerfileID returns the content identity of the runner image this
// project would build for the pinned agent version: the rendered Dockerfile
// (agent profile plus on-disk project Dockerfile), the build-context assets, and
// the version folded together. It is the value the build decision compares an
// existing image's dockerfile-id label against. Exposed so callers outside this
// package (e.g. higher-layer preview tests) can seed an image whose label
// matches the plan this package computes.
//
//deadcode:ignore reason="exported for higher-layer preview tests to seed an image whose label matches this package's plan"
func RunnerDockerfileID(a agent.Agent, agentVersion string, docker options.DockerSetting) (string, error) {
	rendered, err := RenderProjectDockerfile(a, agentVersion, docker)
	if err != nil {
		return "", err
	}
	return computeDockerfileID(rendered, agentVersion), nil
}
