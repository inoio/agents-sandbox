package image

import (
	"context"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// runnerImageState is the inspected identity of an existing runner image.
type runnerImageState struct {
	Present      bool
	DockerfileID string
	Labels       map[string]string
}

// inspectRunnerImage reads the runner image's identity labels. A missing image
// or one without config is reported as absent so the caller rebuilds.
func inspectRunnerImage(ctx context.Context, rTag string) runnerImageState {
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil || inspect.Config == nil {
		return runnerImageState{}
	}
	labels := inspect.Config.Labels
	return runnerImageState{
		Present:      true,
		DockerfileID: labels[dockerfileIDLabelKey],
		Labels:       labels,
	}
}

// buildDecision decides whether the runner image must be rebuilt, and when it
// must, the reasons. A forced build short-circuits so no image inspect is made.
func buildDecision(
	ctx context.Context,
	rTag string,
	force bool,
	dockerfileID string,
	identity imageIdentity,
) (bool, []string) {
	if force {
		return true, []string{"forced by --rebuild"}
	}
	return buildReasons(inspectRunnerImage(ctx, rTag), dockerfileID, identity)
}

// buildReasons explains why the runner image must be rebuilt. A matching
// dockerfile-id means the current inputs are already baked, so no build is
// needed; otherwise it names the changed identity components.
func buildReasons(state runnerImageState, dockerfileID string, identity imageIdentity) (bool, []string) {
	if !state.Present {
		return true, []string{"runner image not present locally"}
	}
	if state.DockerfileID == dockerfileID {
		return false, nil
	}
	reasons := changedIdentityReasons(state.Labels, identity)
	if len(reasons) == 0 {
		reasons = []string{"runner image content changed (tool version or legacy image)"}
	}
	return true, reasons
}

// changedIdentityReasons lists the identity components that differ from the
// image's recorded labels. It returns nil when the image predates the component
// labels, leaving the caller to report a generic content change.
func changedIdentityReasons(labels map[string]string, identity imageIdentity) []string {
	if !hasIdentityLabels(labels) {
		return nil
	}
	var reasons []string
	if labels[projectDockerfileHashLabelKey] != identity.ProjectDockerfileHash {
		reasons = append(reasons, "project Dockerfile changed")
	}
	if labels[agentVersionLabelKey] != identity.AgentVersion {
		reasons = append(reasons, "agent version changed")
	}
	if labels[dockerLabelKey] != identity.Docker {
		reasons = append(reasons, "docker-in-docker changed")
	}
	return reasons
}

// hasIdentityLabels reports whether the image carries any identity component
// labels, distinguishing a legacy image from one with changed components.
func hasIdentityLabels(labels map[string]string) bool {
	for _, key := range []string{projectDockerfileHashLabelKey, agentVersionLabelKey, dockerLabelKey} {
		if _, ok := labels[key]; ok {
			return true
		}
	}
	return false
}

// readImageInfoFromDocker returns the image env map by inspecting the Docker
// image. The loaded microsandbox image is a passthrough of the Docker image, so
// reading from Docker is equivalent to reading from microsandbox and avoids
// requiring the image to be loaded first.
func readImageInfoFromDocker(ctx context.Context, rTag string) (map[string]string, error) {
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return nil, err
	}
	if inspect.Config == nil {
		//nolint:nilnil // a missing config means no env, which is not an error
		return nil, nil
	}
	return parseImageEnv(inspect.Config.Env), nil
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
