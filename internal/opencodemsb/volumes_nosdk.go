//go:build !cgo

package opencodemsb

import (
	"context"
	"errors"
)

func (vm *VolumeManager) EnsureHome(ctx context.Context, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	return "", errors.New("volume management requires CGO (SDK not available)")
}

func (vm *VolumeManager) ensureFallbackHome(ctx context.Context, name, projectSlug, imageDigest, imageTag string, reset bool) (string, error) {
	return "", errors.New("volume management requires CGO (SDK not available)")
}

func (vm *VolumeManager) prefillVolume(ctx context.Context, volumeName, imageTag string) error {
	return errors.New("volume management requires CGO (SDK not available)")
}

func (vm *VolumeManager) prefillFallback(ctx context.Context, hostPath, imageTag string) error {
	return errors.New("volume management requires CGO (SDK not available)")
}

func (vm *VolumeManager) prefill(ctx context.Context, ref, imageTag string, isBind bool) error {
	return errors.New("volume management requires CGO (SDK not available)")
}
