package image

import (
	"context"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/sandbox/docker"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// inspectExistingImage returns the Docker image ID (used as the digest-based
// cache identity) for an existing project image.
func inspectExistingImage(ctx context.Context, rTag string, ui termio.UI) string {
	inspect, inspectErr := docker.Get().ImageInspect(ctx, rTag)
	if inspectErr != nil {
		ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
		return ""
	}
	return inspect.ID
}

// readImageInfoFromDocker returns the image env map and the baked opencode
// version by inspecting the Docker image. The loaded microsandbox image is a
// passthrough of the Docker image, so reading from Docker is equivalent to
// reading from microsandbox and avoids requiring the image to be loaded first.
func readImageInfoFromDocker(ctx context.Context, rTag string) (map[string]string, string, error) {
	inspect, err := docker.Get().ImageInspect(ctx, rTag)
	if err != nil {
		return nil, "", err
	}
	cfg := inspect.Config
	if cfg == nil {
		return nil, "", nil
	}
	return parseImageEnv(cfg.Env), parseImageVersion(cfg.Labels), nil
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
