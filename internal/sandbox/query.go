package sandbox

import (
	"context"
	"fmt"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"
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

type ImageInfo struct {
	Reference string
	Digest    string
}

type sandboxHandle struct {
	name string
}

type volumeHandle struct {
	name string
}

type imageHandle struct {
	reference string
}

func filterSandboxes(handles []sandboxHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, projectVMPrefix) {
			result = append(result, h.name)
		}
	}
	return result
}

func filterVolumes(handles []volumeHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, "opencode-msb-home-") {
			result = append(result, h.name)
		}
	}
	return result
}

func filterImages(handles []imageHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.reference, "opencode-msb/runner-") {
			result = append(result, h.reference)
		}
	}
	return result
}

func ListSandboxes(ctx context.Context) ([]Info, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	var result []Info
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, projectVMPrefix) {
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
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var result []VolumeInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-home-") {
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

func ListImages(ctx context.Context) ([]ImageInfo, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var result []ImageInfo
	for _, h := range handles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner-") {
			continue
		}
		result = append(result, ImageInfo{
			Reference: ref,
			Digest:    h.ManifestDigest(),
		})
	}
	return result, nil
}
