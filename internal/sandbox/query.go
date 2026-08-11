package sandbox

import (
	"context"
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
)

type Info struct {
	Name   string
	Status string
}

type VolumeInfo struct {
	Name string
	Path string
	Kind string
}

type ImageInfo = image.Info

type sandboxHandle struct {
	name string
}

type volumeHandle struct {
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

func filterVolumes(handles []volumeHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, naming.HomePrefix) {
			result = append(result, h.name)
		}
	}
	return result
}

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

var ListImages = image.ListImages //nolint:gochecknoglobals // re-export from image module
