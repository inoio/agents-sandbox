package msb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Client is the public abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery and creation of sandboxes and deletion
// of volumes and images, plus runtime setup. Sandboxes are removed through the
// identity-aware handle/live operations (SandboxHandle.Remove, Sandbox.Destroy)
// rather than by bare name, so this interface has no RemoveSandbox. Production
// code uses realMsbClient; tests construct MockMsbClient directly.
type Client interface {
	EnsureInstalled(ctx context.Context) error
	GetSandbox(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandbox(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error)
	ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)
	GetVolume(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolume(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error)
	ListVolumes(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolume(ctx context.Context, name string) error
	ImageGet(ctx context.Context, ref string) error
	ImageList(ctx context.Context) ([]ImageHandle, error)
	ImagePrune(ctx context.Context) (*msbSdk.ImagePruneReport, error)
	ImageRemove(ctx context.Context, ref string, force bool) error
	ImageLoad(ctx context.Context, ref string, r io.Reader) error
	ImageInspect(ctx context.Context, ref string) (*msbSdk.ImageConfig, error)
	ImageInspectDetail(ctx context.Context, ref string) (*ImageDetail, error)
	StorageUsage(ctx context.Context) (*msbSdk.StorageUsageReport, error)
	PruneStorage(ctx context.Context, options msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error)
}

// Seams over the SDK calls realMsbClient delegates to, so its unit tests inject
// fakes and never load the native library or read the developer's real runtime.
// The real client is exercised by the msb-tagged tests and by the running
// launcher, not here.
var (
	getSandbox        = msbSdk.GetSandbox
	createSandbox     = msbSdk.CreateSandbox
	listSandboxesWith = msbSdk.ListSandboxesWith
	getVolume         = msbSdk.GetVolume
	createVolume      = msbSdk.CreateVolume
	listVolumes       = msbSdk.ListVolumes
	removeVolume      = msbSdk.RemoveVolume
	imageGet          = msbSdk.Image.Get
	imageList         = msbSdk.Image.List
	imageRemove       = msbSdk.Image.Remove
	imageLoad         = msbSdk.Image.Load
	realImageInspect  = msbSdk.Image.Inspect
	imagePrune        = msbSdk.Image.Prune
	storageUsage      = msbSdk.StorageUsage
	storagePrune      = msbSdk.PruneStorage
)

// realMsbClient delegates to the actual microsandbox SDK.
type realMsbClient struct{}

func (realMsbClient) EnsureInstalled(ctx context.Context) error {
	if skipRuntimeInstall {
		return nil
	}
	_, err := ensureRuntime(ctx, runtimeConfig(), msbSdk.InstallOptions{})
	return err
}

func (realMsbClient) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	h, err := getSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realSandboxHandle{handle: h}, nil
}

func (realMsbClient) CreateSandbox(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error) {
	sb, err := createSandbox(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

func (realMsbClient) ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error) {
	var result []SandboxHandle
	var cursor *string
	for {
		var page *msbSdk.SandboxPage
		var err error
		var opts []msbSdk.SandboxListOption
		if len(labels) > 0 {
			opts = append(opts, msbSdk.WithListLabels(labels))
		}
		if cursor != nil {
			opts = append(opts, msbSdk.WithListCursor(*cursor))
		}
		page, err = listSandboxesWith(ctx, opts...)
		if err != nil {
			return nil, err
		}
		for _, h := range page.Sandboxes {
			result = append(result, &realSandboxHandle{handle: h})
		}
		cursor = page.NextCursor
		if cursor == nil {
			break
		}
	}
	return result, nil
}

func (realMsbClient) GetVolume(ctx context.Context, name string) (VolumeHandle, error) {
	v, err := getVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error) {
	if _, err := createVolume(ctx, name, opts...); err != nil {
		return nil, err
	}
	handle, err := getVolume(ctx, name)
	if err != nil {
		// The volume exists now, and the caller never receives a handle to
		// remove it, so a failed lookup would orphan it. Remove it best-effort,
		// detached from the caller's cancellation so a cancelled or
		// deadline-exceeded ctx cannot skip the cleanup.
		if removeErr := removeVolume(context.WithoutCancel(ctx), name); removeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("remove orphaned volume %q: %w", name, removeErr))
		}
		return nil, err
	}
	return handle, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := listVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) RemoveVolume(ctx context.Context, name string) error {
	return removeVolume(ctx, name)
}

func (realMsbClient) ImageGet(ctx context.Context, ref string) error {
	_, err := imageGet(ctx, ref)
	return err
}

func (realMsbClient) ImageList(ctx context.Context) ([]ImageHandle, error) {
	handles, err := imageList(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ImageHandle, len(handles))
	for i, h := range handles {
		result[i] = h
	}
	return result, nil
}

func (realMsbClient) ImagePrune(ctx context.Context) (*msbSdk.ImagePruneReport, error) {
	return imagePrune(ctx)
}

func (realMsbClient) PruneStorage(
	ctx context.Context,
	options msbSdk.StoragePruneOptions,
) (*msbSdk.StoragePruneReport, error) {
	return storagePrune(ctx, options)
}

func (realMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	return imageRemove(ctx, ref, force)
}

func (realMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) (err error) {
	tmp, err := os.CreateTemp("", "agents-sandbox-image-*.tar")
	if err != nil {
		return fmt.Errorf("creating temp file for image load: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer func() {
		if closeErr := tmp.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("closing image archive: %w", closeErr)
		}
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("spooling image archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing image archive: %w", err)
	}

	if _, err := imageLoad(ctx, tmp.Name(), ref); err != nil {
		return fmt.Errorf("loading image into microsandbox: %w", err)
	}
	return nil
}

func (realMsbClient) ImageInspect(ctx context.Context, ref string) (*msbSdk.ImageConfig, error) {
	detail, err := realImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	return detail.Config, nil
}

func (realMsbClient) ImageInspectDetail(ctx context.Context, ref string) (*ImageDetail, error) {
	detail, err := realImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := &ImageDetail{Layers: detail.Layers}
	if detail.ImageHandle != nil {
		out.Reference = detail.Reference()
		out.ManifestDigest = detail.ManifestDigest()
		out.Architecture = detail.Architecture()
		out.OS = detail.OS()
		out.SizeBytes = detail.SizeBytes()
		out.CreatedAt = detail.CreatedAt()
	}
	if detail.Config != nil {
		out.Config = *detail.Config
	}
	return out, nil
}

func (realMsbClient) StorageUsage(ctx context.Context) (*msbSdk.StorageUsageReport, error) {
	return storageUsage(ctx)
}
