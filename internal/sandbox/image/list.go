package image

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
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

// ListImages returns the cached runner images. Ownership is positional: the
// private runtime home holds only objects agents-sandbox created, so a reference
// is a runner image when its parsed slug embeds the launcher's 14-character
// slug hash (naming.IsRunnerImageRef); a repository reference (a slash) or a
// single-segment reference without that hash — a manually loaded image — is
// excluded, matching the removal guard.
func ListImages(ctx context.Context) ([]Info, error) {
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var result []Info
	for _, h := range handles {
		ref := h.Reference()
		if !naming.IsRunnerImageRef(ref) {
			continue
		}
		result = append(result, Info{
			Reference: ref,
			Digest:    h.ManifestDigest(),
			Size:      imageSize(h.SizeBytes()),
			CreatedAt: FormatImageTime(h.CreatedAt()),
		})
	}
	return result, nil
}
