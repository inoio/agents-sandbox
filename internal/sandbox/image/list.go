package image

import (
	"context"
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
)

// Info represents a single runner image.
type Info struct {
	Reference string
	Digest    string
}

// ListImages returns the cached runner images.
func ListImages(ctx context.Context) ([]Info, error) {
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var result []Info
	for _, h := range handles {
		ref := h.Reference()
		if strings.HasPrefix(ref, naming.ImagePrefix) {
			result = append(result, Info{
				Reference: ref,
				Digest:    h.ManifestDigest(),
			})
		}
	}
	return result, nil
}
