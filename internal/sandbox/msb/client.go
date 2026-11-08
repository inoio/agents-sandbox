package msb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// RuntimeInstaller ensures the microsandbox runtime is installed on the host.
type RuntimeInstaller interface {
	EnsureInstalled(ctx context.Context) error
}

// SandboxStore discovers and creates sandboxes.
type SandboxStore interface {
	GetSandbox(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandbox(ctx context.Context, name string, spec SandboxSpec) (Sandbox, error)
	ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)
}

// VolumeStore discovers, creates and removes volumes.
type VolumeStore interface {
	GetVolume(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolume(ctx context.Context, name string, spec VolumeSpec) (VolumeHandle, error)
	ListVolumes(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolume(ctx context.Context, name string) error
}

// ImageStore inspects, loads, removes and prunes images.
type ImageStore interface {
	ImageGet(ctx context.Context, ref string) error
	ImageList(ctx context.Context) ([]ImageHandle, error)
	ImagePrune(ctx context.Context) (*ImagePruneReport, error)
	ImageRemove(ctx context.Context, ref string, force bool) error
	ImageLoad(ctx context.Context, ref string, r io.Reader) error
	ImageInspect(ctx context.Context, ref string) (*ImageConfig, error)
	ImageInspectDetail(ctx context.Context, ref string) (*ImageDetail, error)
}

// StorageStore reports and prunes runtime storage.
type StorageStore interface {
	StorageUsage(ctx context.Context) (*StorageReport, error)
	PruneStorage(ctx context.Context, req PruneRequest) (*PruneReport, error)
}

// Client is the public abstraction over the microsandbox SDK used by the
// sandbox package. It covers discovery and creation of sandboxes and deletion
// of volumes and images, plus runtime setup. Sandboxes are removed through the
// identity-aware handle/live operations (SandboxHandle.Remove, Sandbox.Destroy)
// rather than by bare name, so this interface has no RemoveSandbox. Production
// code uses realMsbClient; tests construct msbtest.MockMsbClient directly.
type Client interface {
	RuntimeInstaller
	SandboxStore
	VolumeStore
	ImageStore
	StorageStore
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
	return EnsureRuntime(ctx)
}

func (realMsbClient) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	h, err := getSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return &realSandboxHandle{handle: h}, nil
}

func (realMsbClient) CreateSandbox(ctx context.Context, name string, spec SandboxSpec) (Sandbox, error) {
	opts, err := sandboxOptions(spec)
	if err != nil {
		return nil, err
	}
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
	return realVolumeHandle{handle: v}, nil
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, spec VolumeSpec) (VolumeHandle, error) {
	if _, err := createVolume(ctx, name, volumeOptions(spec)...); err != nil {
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
	return realVolumeHandle{handle: handle}, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := listVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = realVolumeHandle{handle: h}
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

func (realMsbClient) ImagePrune(ctx context.Context) (*ImagePruneReport, error) {
	report, err := imagePrune(ctx)
	if err != nil {
		return nil, err
	}
	return imagePruneReportFrom(report), nil
}

func (realMsbClient) PruneStorage(
	ctx context.Context,
	req PruneRequest,
) (*PruneReport, error) {
	report, err := storagePrune(ctx, storagePruneOptions(req))
	if err != nil {
		return nil, err
	}
	return pruneReportFrom(report), nil
}

func (realMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	return imageRemove(ctx, ref, force)
}

// imageSpoolFile is the temp file ImageLoad spools an image archive into. It is
// an interface so the sync and close failure branches are unit-testable with a
// fake; production uses the *os.File os.CreateTemp returns.
type imageSpoolFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

var createImageSpoolFile = func() (imageSpoolFile, error) {
	return os.CreateTemp("", "agents-sandbox-image-*.tar")
}

func (realMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) (err error) {
	tmp, err := createImageSpoolFile()
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

func (realMsbClient) ImageInspect(ctx context.Context, ref string) (*ImageConfig, error) {
	detail, err := realImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	return imageConfigFrom(detail.Config), nil
}

func (realMsbClient) ImageInspectDetail(ctx context.Context, ref string) (*ImageDetail, error) {
	detail, err := realImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := &ImageDetail{Layers: imageLayersFrom(detail.Layers)}
	if detail.ImageHandle != nil {
		out.Reference = detail.Reference()
		out.ManifestDigest = detail.ManifestDigest()
		out.Architecture = detail.Architecture()
		out.OS = detail.OS()
		out.SizeBytes = detail.SizeBytes()
		out.CreatedAt = detail.CreatedAt()
	}
	if config := imageConfigFrom(detail.Config); config != nil {
		out.Config = *config
	}
	return out, nil
}

func (realMsbClient) StorageUsage(ctx context.Context) (*StorageReport, error) {
	report, err := storageUsage(ctx)
	if err != nil {
		return nil, err
	}
	return storageReportFrom(report), nil
}
