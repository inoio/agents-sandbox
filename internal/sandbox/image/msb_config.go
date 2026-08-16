package image

import (
	"context"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
)

// readImageInfoFromMSB returns the image env map and the baked opencode version
// by inspecting the image in the msb cache. A nil config yields empty values.
func readImageInfoFromMSB(ctx context.Context, c msb.Client, ref string) (map[string]string, string, error) {
	cfg, err := c.ImageInspect(ctx, ref)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		return nil, "", nil
	}
	return parseImageEnv(cfg.Env), parseImageVersion(cfg.Labels), nil
}
