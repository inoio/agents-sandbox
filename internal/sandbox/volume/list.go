package volume

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
)

//nolint:revive // VolumeInfo is the established name from query.go
type VolumeInfo struct {
	Name          string
	Kind          string
	QuotaMiB      *uint32
	CapacityBytes *uint64
	CreatedAt     string
}

// FormatVolumeTime renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or "-" for the zero time. This matches msb volume list output and is
// intentionally distinct from session.FormatTime (which omits seconds).
func FormatVolumeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// ListVolumes returns a list of home volumes managed by opencode-sandbox.
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
			Name:          name,
			Kind:          string(h.Kind()),
			QuotaMiB:      h.QuotaMiB(),
			CapacityBytes: h.CapacityBytes(),
			CreatedAt:     FormatVolumeTime(h.CreatedAt()),
		})
	}
	return result, nil
}
