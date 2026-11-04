// Package vm holds the capabilities that act on a project VM: finding, naming,
// creating, starting, stopping and removing it, setting up a running sandbox
// (config files, startup hooks, dockerd, the agent daemon, the attach target),
// adopting a legacy identity, and listing and inspecting VMs. Deciding what a
// run needs from them is package prepare's job (specs/lifecycle.md).
package vm

import (
	"context"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
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
//
//exhaustruct:ignore
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
// by opt.
func ListSandboxes(ctx context.Context, opt ListOption) ([]Info, error) {
	handles, err := msb.Get().ListSandboxes(ctx, opt.Labels)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var result []Info
	for _, h := range handles {
		name := h.Name()
		cfg, _ := h.Config()
		var labels map[string]string
		if cfg != nil {
			labels = cfg.Labels
		}
		if naming.SandboxIdentity(name, labels).Task {
			continue
		}
		status := h.Status()
		if !statusMatchesFilter(status, opt) {
			continue
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
		// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="at len(result) == limit the truncation keeps the same elements, so > and >= agree"
		uint32(len(result)) > *opt.Limit { //nolint:gosec // G115: len(result) is bounded by the sandbox count
		result = result[:*opt.Limit]
	}
	return result, nil
}

// statusMatchesFilter reports whether a sandbox status passes the running/
// stopped filter. RunningOnly takes precedence when both are set.
func statusMatchesFilter(status msbSdk.SandboxStatus, opt ListOption) bool {
	if opt.RunningOnly {
		return status == msbSdk.SandboxStatusRunning
	}
	if opt.StoppedOnly {
		return status == msbSdk.SandboxStatusStopped
	}
	return true
}
