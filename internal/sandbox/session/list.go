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
	Name      string
	Status    string
	Image     string
	CreatedAt string
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

// ListSandboxes returns a list of sandbox VMs for the current host.
func ListSandboxes(ctx context.Context) ([]Info, error) {
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	var result []Info
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, naming.VmPrefix) {
			continue
		}
		result = append(result, Info{
			Name:      name,
			Status:    string(h.Status()),
			Image:     h.Image(),
			CreatedAt: FormatTime(h.CreatedAt()),
		})
	}
	return result, nil
}
