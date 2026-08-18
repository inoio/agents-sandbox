package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/humanize"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"
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

type sandboxHandle struct {
	name string
}

func filterSandboxes(handles []sandboxHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, naming.VmPrefix) {
			result = append(result, h.name)
		}
	}
	return result
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
