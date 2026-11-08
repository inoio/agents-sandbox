package msb

// ImageConfig is the launcher-owned projection of a cached image's parsed OCI
// config block. Optional fields stay nil when the runtime does not report
// them, so unknown never reads as zero.
type ImageConfig struct {
	Digest     string
	Env        []string
	Cmd        []string
	Entrypoint []string
	WorkingDir string
	User       string
	Labels     map[string]string
	StopSignal string
}

// ImageLayer is one layer of an image manifest.
type ImageLayer struct {
	DiffID              string
	BlobDigest          string
	MediaType           string
	CompressedSizeBytes *int64
	ErofsSizeBytes      *int64
	Position            int32
}

// ImagePruneReport summarizes the artifacts a runtime image prune removed.
type ImagePruneReport struct {
	SkippedInUse     uint32
	ImageRefsRemoved uint32
	ManifestsRemoved uint32
	LayersRemoved    uint32
	FsmetaRemoved    uint32
	VMDKRemoved      uint32
	BytesReclaimed   *uint64
}
