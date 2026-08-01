package sandbox

import (
	"context"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// msbClient abstracts the microsandbox SDK operations used by prune logic.
// It exists to enable unit testing without a real microsandbox runtime.
type msbClient interface {
	ListSandboxes(ctx context.Context) ([]msbSandboxHandle, error)
	ListVolumes(ctx context.Context) ([]msbVolumeHandle, error)
	ListImages(ctx context.Context) ([]msbImageHandle, error)
	RemoveSandbox(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	RemoveImage(ctx context.Context, ref string, force bool) error
}

// msbSandboxHandle is the subset of *msb.SandboxHandle that prune needs.
type msbSandboxHandle interface {
	Name() string
	Status() msb.SandboxStatus
	UpdatedAt() time.Time
	Image() string
}

// msbVolumeHandle is the subset of *msb.VolumeHandle that prune needs.
type msbVolumeHandle interface {
	Name() string
}

// msbImageHandle is the subset of *msb.ImageHandle that prune needs.
type msbImageHandle interface {
	Reference() string
}

// newMsbClient is the factory Prune uses to obtain an msbClient.
// Tests override it to inject mocks without changing the public API.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var newMsbClient = func() msbClient {
	return realMsbClient{}
}

// realMsbClient delegates to the actual microsandbox SDK.
type realMsbClient struct{}

func (realMsbClient) ListSandboxes(ctx context.Context) ([]msbSandboxHandle, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbSandboxHandle, len(handles))
	for i, h := range handles {
		result[i] = msbSandboxWrapper{handle: h}
	}
	return result, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]msbVolumeHandle, error) {
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbVolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) ListImages(ctx context.Context) ([]msbImageHandle, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]msbImageHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) RemoveSandbox(ctx context.Context, name string) error {
	return msb.RemoveSandbox(ctx, name)
}

func (realMsbClient) RemoveVolume(ctx context.Context, name string) error {
	return msb.RemoveVolume(ctx, name)
}

func (realMsbClient) RemoveImage(ctx context.Context, ref string, force bool) error {
	return msb.Image.Remove(ctx, ref, force)
}

// msbSandboxWrapper adapts *msb.SandboxHandle to msbSandboxHandle.
type msbSandboxWrapper struct {
	handle *msb.SandboxHandle
}

func (w msbSandboxWrapper) Name() string {
	return w.handle.Name()
}

func (w msbSandboxWrapper) Status() msb.SandboxStatus {
	return w.handle.Status()
}

func (w msbSandboxWrapper) UpdatedAt() time.Time {
	return w.handle.UpdatedAt()
}

func (w msbSandboxWrapper) Image() string {
	cfg, err := w.handle.Config()
	if err != nil {
		return ""
	}
	return cfg.Image
}
