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

// baseMovedLocally reports whether the base image recorded in the existing
// runner image's base label ("<ref>@<id>" provenance from build time) no
// longer matches the locally-tagged base image, i.e. the base tag has moved
// since the runner was built. It returns false on any inspect error or a
// missing config/label so an unknown state never forces a rebuild, keeping the
// skip path offline-safe.
func baseMovedLocally(ctx context.Context, rTag, baseRef string) bool {
	runner, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil || runner.Config == nil {
		return false
	}
	recorded, ok := runner.Config.Labels[baseImageLabelKey]
	if !ok {
		return false
	}
	recordedID := lastAtSegment(recorded)
	if recordedID == "" {
		return false
	}
	base, err := docker.Get().ImageInspect(ctx, baseRef)
	if err != nil {
		return false
	}
	return recordedID != base.ID
}

// lastAtSegment returns the part of s after the last "@", or "" when s carries
// no "@" separator.
func lastAtSegment(s string) string {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return ""
	}
	return s[at+1:]
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
