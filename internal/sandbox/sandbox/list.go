// Package sandbox queries and represents the sandbox VMs managed by the
// launcher, mirroring the list/display helpers in the image and volume
// packages.
package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/opencode-sandbox/internal/sandbox/humanize"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/naming"
)

// Info holds display information about a sandbox VM.
type Info struct {
	Name         string
	Status       string
	Image        string
	CreatedAt    string
	Labels       map[string]string
	CreatedAtRaw time.Time
	UpdatedAtRaw time.Time
}

// ListOption carries optional filters/format controls for ListSandboxes.
type ListOption struct {
	Labels      map[string]string
	Limit       *uint32
	RunningOnly bool
	StoppedOnly bool
}

// FormatTime renders a timestamp as YYYY-MM-DD HH:MM:SS in the time's own
// location, or an empty string for the zero time.
func FormatTime(t time.Time) string {
	return humanize.FormatTimestamp(t)
}

// ListSandboxes returns a list of sandbox VMs for the current host, filtered
// by the given options.
func ListSandboxes(ctx context.Context, opts ...ListOption) ([]Info, error) { //nolint:gocognit
	opt := ListOption{} //nolint:exhaustruct // filter fields are accumulated from opts below
	for _, o := range opts {
		if o.Labels != nil {
			opt.Labels = o.Labels
		}
		if o.Limit != nil {
			opt.Limit = o.Limit
		}
		opt.RunningOnly = opt.RunningOnly || o.RunningOnly
		opt.StoppedOnly = opt.StoppedOnly || o.StoppedOnly
	}

	handles, err := msb.Get().ListSandboxes(ctx, opt.Labels)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var result []Info
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.VmPrefix) {
			continue
		}
		status := h.Status()
		if opt.RunningOnly || opt.StoppedOnly {
			active := msb.IsSandboxActive(status)
			if opt.RunningOnly && !active {
				continue
			}
			if !opt.RunningOnly && opt.StoppedOnly && active {
				continue
			}
		}
		cfg, _ := h.Config()
		var labels map[string]string
		if cfg != nil {
			labels = cfg.Labels
		}
		result = append(result, Info{
			Name:         name,
			Status:       string(status),
			Image:        h.Image(),
			CreatedAt:    FormatTime(h.CreatedAt()),
			Labels:       labels,
			CreatedAtRaw: h.CreatedAt(),
			UpdatedAtRaw: h.UpdatedAt(),
		})
	}
	if opt.Limit != nil &&
		uint32(len(result)) > *opt.Limit { //nolint:gosec // G115: len(result) is bounded by the sandbox count
		result = result[:*opt.Limit]
	}
	return result, nil
}
