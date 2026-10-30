package image

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// LayerInfo is one OCI layer of an inspected image.
type LayerInfo struct {
	Position       int32
	DiffID         string
	BlobDigest     string
	MediaType      string
	CompressedSize string
	ErofsSize      string
}

// ImageDetail is the inspected identity, manifest, and OCI config of a cached
// runner image.
type ImageDetail struct {
	Reference    string
	Digest       string
	Architecture string
	OS           string
	Size         string
	CreatedAt    string
	Env          []string
	Cmd          []string
	Entrypoint   []string
	WorkingDir   string
	User         string
	Labels       map[string]string
	StopSignal   string
	Layers       []LayerInfo
}

// InspectImage returns the full detail of a cached image by reference.
func InspectImage(ctx context.Context, ref string) (ImageDetail, error) {
	detail, err := msb.Get().ImageInspectDetail(ctx, ref)
	if err != nil {
		return ImageDetail{}, fmt.Errorf("inspect image %q: %w", ref, err)
	}
	layers := make([]LayerInfo, 0, len(detail.Layers))
	for _, l := range detail.Layers {
		layers = append(layers, LayerInfo{
			Position:       l.Position,
			DiffID:         l.DiffID,
			BlobDigest:     l.BlobDigest,
			MediaType:      l.MediaType,
			CompressedSize: imageSize(l.CompressedSizeBytes),
			ErofsSize:      imageSize(l.ErofsSizeBytes),
		})
	}
	return ImageDetail{
		Reference:    detail.Reference,
		Digest:       detail.ManifestDigest,
		Architecture: detail.Architecture,
		OS:           detail.OS,
		Size:         imageSize(detail.SizeBytes),
		CreatedAt:    FormatImageTime(detail.CreatedAt),
		Env:          detail.Config.Env,
		Cmd:          detail.Config.Cmd,
		Entrypoint:   detail.Config.Entrypoint,
		WorkingDir:   detail.Config.WorkingDir,
		User:         detail.Config.User,
		Labels:       detail.Config.Labels,
		StopSignal:   detail.Config.StopSignal,
		Layers:       layers,
	}, nil
}
