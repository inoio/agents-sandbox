package image

import (
	"context"
	"strings"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// inspectExistingImage returns the Docker image ID (used as the digest-based
// cache identity) for an existing project image. Env and the opencode version
// are read separately from the msb image cache, not from Docker.
func inspectExistingImage(ctx context.Context, rTag string, ui termio.UI) string {
	inspect, inspectErr := docker.Get().ImageInspect(ctx, rTag)
	if inspectErr != nil {
		ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
		return ""
	}
	return inspect.ID
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
