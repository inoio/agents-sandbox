package sandbox

import (
	"context"
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

// Info holds basic information about a sandbox VM.
type Info struct {
	Name   string
	Status string
}

// ImageInfo is an alias for image.Info from the image module.
type ImageInfo = image.Info

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
	handles, err := msb.Get().ListSandboxes(ctx)
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
			Name:   name,
			Status: string(h.Status()),
		})
	}
	return result, nil
}

// ListImages re-exports the image module's ListImages.
var ListImages = image.ListImages //nolint:gochecknoglobals // re-export from image module
