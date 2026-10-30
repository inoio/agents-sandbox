package view

import (
	"time"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	colName    = "NAME"
	colStatus  = "STATUS"
	colImage   = "IMAGE"
	colCreated = "CREATED"
	colKind    = "KIND"
	colSize    = "SIZE"
	colRef     = "REFERENCE"
	colDigest  = "DIGEST"
)

// SandboxPrinter shapes sandbox lists. Column order matches msb:
// NAME IMAGE STATUS CREATED.
type SandboxPrinter struct{}

func (SandboxPrinter) Headers() []string {
	return []string{colName, colImage, colStatus, colCreated}
}

func (SandboxPrinter) Cells(s sandbox.Info) []string {
	return []string{s.Name, s.Image, termio.StyleStatus(s.Status), s.CreatedAt}
}

type jsonSandbox struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Image   string            `json:"image"`
	Created time.Time         `json:"created"`
	Updated time.Time         `json:"updated"`
	Labels  map[string]string `json:"labels"`
}

func (SandboxPrinter) JSON(infos []sandbox.Info) any {
	out := make([]jsonSandbox, 0, len(infos))
	for _, s := range infos {
		out = append(out, jsonSandbox{
			Name:    s.Name,
			Status:  s.Status,
			Image:   s.Image,
			Created: s.CreatedAtRaw,
			Updated: s.UpdatedAtRaw,
			Labels:  s.Labels,
		})
	}
	return out
}

// ImagePrinter shapes runner image lists.
type ImagePrinter struct{}

func (ImagePrinter) Headers() []string {
	return []string{colRef, colDigest, colSize, colCreated}
}

func (ImagePrinter) Cells(i image.Info) []string {
	return []string{i.Reference, truncateDigest(i.Digest), i.Size, i.CreatedAt}
}

type jsonImage struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
	Size      string `json:"size"`
	Created   string `json:"created"`
}

func (ImagePrinter) JSON(infos []image.Info) any {
	out := make([]jsonImage, 0, len(infos))
	for _, i := range infos {
		out = append(out, jsonImage{
			Reference: i.Reference,
			Digest:    i.Digest,
			Size:      i.Size,
			Created:   i.CreatedAt,
		})
	}
	return out
}

// truncateDigest shortens a full manifest digest to the short form msb uses in
// image list output: the "sha256:" prefix followed by the first 12 hex chars.
func truncateDigest(digest string) string {
	const shortLen = len("sha256:") + 12
	if len(digest) <= shortLen {
		return digest
	}
	return digest[:shortLen]
}

// VolumePrinter shapes home volume lists.
type VolumePrinter struct{}

func (VolumePrinter) Headers() []string {
	return []string{colName, colKind, colSize, colCreated}
}

func (VolumePrinter) Cells(v volume.VolumeInfo) []string {
	return []string{v.Name, v.Kind, volumeSize(v.Kind, v.QuotaMiB, v.CapacityBytes), v.CreatedAt}
}

type jsonVolume struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Size      string `json:"size"`
	CreatedAt string `json:"created"`
}

func (VolumePrinter) JSON(infos []volume.VolumeInfo) any {
	out := make([]jsonVolume, 0, len(infos))
	for _, v := range infos {
		out = append(out, jsonVolume{
			Name:      v.Name,
			Kind:      v.Kind,
			Size:      volumeSize(v.Kind, v.QuotaMiB, v.CapacityBytes),
			CreatedAt: v.CreatedAt,
		})
	}
	return out
}

// volumeSize renders the SIZE column to match msb: disk volumes show
// capacity, directory volumes show quota, and "-" when unavailable.
func volumeSize(kind string, quotaMiB *uint32, capacityBytes *uint64) string {
	if kind == "disk" {
		if capacityBytes != nil {
			return humanize.FormatBytes(*capacityBytes)
		}
		return "-"
	}
	if quotaMiB != nil {
		return humanize.FormatBytes(uint64(*quotaMiB) * 1024 * 1024)
	}
	return "-"
}
