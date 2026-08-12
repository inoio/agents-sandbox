package pruning

// StaleType indicates the kind of artifact being pruned.
type StaleType int

const (
	StaleTypeVM StaleType = iota
	StaleTypeVolume
	StaleTypeDockerImage
	StaleTypeMsbImage
)

var typeName = map[StaleType]string{ //nolint:gochecknoglobals // fmt.stringer pattern
	StaleTypeVM:          "vm",
	StaleTypeVolume:      "volume",
	StaleTypeDockerImage: "docker-image",
	StaleTypeMsbImage:    "msb-image",
}

func (ss StaleType) String() string { return typeName[ss] }
