package image

import (
	"context"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

// imageHasDockerfileID reports whether the existing runner image carries a
// dockerfile-id label matching the current content identity. A match means the
// baked image reflects the current agent, project Dockerfile, and version, so
// the build can be skipped. It returns false on any inspect error or a missing
// config/label so an unknown image is always rebuilt.
func imageHasDockerfileID(ctx context.Context, rTag, dockerfileID string) bool {
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil || inspect.Config == nil {
		return false
	}
	return inspect.Config.Labels[dockerfileIDLabelKey] == dockerfileID
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
