package image

import (
	"context"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// inspectExistingImage attempts to inspect the image on disk. If the image is
// missing it falls back to stored env metadata. Returns (imageEnvs, digest).
func inspectExistingImage(ctx context.Context, rTag string, ui termio.UI) (map[string]string, string) {
	imageEnv := make(map[string]string)
	var imageDigest string

	inspect, inspectErr := docker.Get().ImageInspect(ctx, rTag)
	if inspectErr != nil {
		ui.Verbosef("image inspect failed (might be pruned): %v", inspectErr)
		if cached := loadImageEnv(rTag); cached != nil {
			imageEnv = cached
			ui.Verbosef("using stored image env metadata for %s", rTag)
		}
		return imageEnv, imageDigest
	}
	imageDigest = inspect.ID
	imageEnv = parseImageEnv(inspect.Config.Env)
	storeImageEnv(rTag, imageEnv)
	ui.Verbosef("inspected image %s with %d env vars", rTag, len(imageEnv))
	return imageEnv, imageDigest
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
