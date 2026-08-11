package volume

import (
	"context"
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

// VolumeInfo holds information about a single home volume.
//
//nolint:revive // VolumeInfo is the established name from query.go
type VolumeInfo struct {
	Name string
	Path string
	Kind string
}

type volumeHandle struct {
	name string
}

func filterVolumes(handles []volumeHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, naming.HomePrefix) {
			result = append(result, h.name)
		}
	}
	return result
}

// ListVolumes returns a list of home volumes managed by opencode-msb.
func ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var result []VolumeInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.HomePrefix) {
			continue
		}
		result = append(result, VolumeInfo{
			Name: name,
			Path: h.Path(),
			Kind: string(h.Kind()),
		})
	}
	return result, nil
}
