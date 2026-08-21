package image

import (
	"context"
	"fmt"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/sandbox/humanize"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

// Info represents a single runner image.
type Info struct {
	Reference string
	Digest    string
	Size      string
	CreatedAt string
}

// unknownSize renders when the SDK does not report a size.
const unknownSize = "unknown"

// imageSize renders a size column from the SDK handle, or unknownSize when the
// SDK did not report a size or reports a negative (invalid) one.
func imageSize(bytes *int64) string {
	if bytes == nil || *bytes < 0 {
		return unknownSize
	}
	return humanize.FormatBytes(uint64(*bytes))
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
				Size:      imageSize(h.SizeBytes()),
				CreatedAt: FormatImageTime(h.CreatedAt()),
			})
		}
	}
	return result, nil
}
