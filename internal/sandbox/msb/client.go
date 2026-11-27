package msb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
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
// newLocalContext is the SDK's local-context constructor as a seam, so a test
// can assert the destination options without the native library.
var newLocalContext = msbSdk.NewLocalContext

// localContext resolves agents-sandbox's microsandbox destination, naming the
// private home explicitly rather than relying on the process-global MSB_HOME.
// The SDK resolves its configured context per operation, so this resolves per
// call too; it is a seam so a test can substitute a destination.
var localContext = func() (*msbSdk.LocalContext, error) {
	return newLocalContext(msbSdk.LocalOptions{Home: configpaths.MSBHome(), ConfigPath: ""})
}

func sandboxes() (*msbSdk.Sandboxes, error) {
	c, err := localContext()
	if err != nil {
		return nil, err
	}
	return c.Sandboxes(), nil
}

func volumes() (*msbSdk.Volumes, error) {
	c, err := localContext()
	if err != nil {
		return nil, err
	}
	return c.Volumes(), nil
}

// The sandbox and volume seams route through the explicit destination. They stay
// package-level function values so unit tests inject fakes without the native
// library.
var (
	getSandbox = func(ctx context.Context, name string) (*msbSdk.Sandbox, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.Get(ctx, name)
	}
	createSandbox = func(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (*msbSdk.Sandbox, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.Create(ctx, name, opts...)
	}
	listSandboxesWith = func(ctx context.Context, opts ...msbSdk.SandboxListOption) (*msbSdk.SandboxPage, error) {
		s, err := sandboxes()
		if err != nil {
			return nil, err
		}
		return s.List(ctx, opts...)
	}
	getVolume = func(ctx context.Context, name string) (*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.Get(ctx, name)
	}
	createVolume = func(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.Create(ctx, name, opts...)
	}
	listVolumes = func(ctx context.Context) ([]*msbSdk.Volume, error) {
		v, err := volumes()
		if err != nil {
			return nil, err
		}
		return v.List(ctx)
	}
	removeVolume = func(ctx context.Context, name string) error {
		v, err := volumes()
		if err != nil {
			return err
		}
		return v.Remove(ctx, name)
	}
	imageGet         = msbSdk.Image.Get
	imageList        = msbSdk.Image.List
	imageRemove      = msbSdk.Image.Remove
	imageLoad        = msbSdk.Image.Load
	realImageInspect = msbSdk.Image.Inspect
	imagePrune       = msbSdk.Image.Prune
	storageUsage     = msbSdk.StorageUsage
	storagePrune     = msbSdk.PruneStorage
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
	return sandboxHandleFrom(ctx, h)
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
			handle, err := sandboxHandleFrom(ctx, h)
			if err != nil {
				// A list is a point-in-time view: an entry removed or replaced
				// between the page fetch and this observation is nothing to
				// enumerate, not a failure. Any other error is real.
				if IsNotFound(err) || IsReplaced(err) {
					continue
				}
				return nil, err
			}
			result = append(result, handle)
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
	return volumeHandleFrom(ctx, v)
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, spec VolumeSpec) (VolumeHandle, error) {
	if _, err := createVolume(ctx, name, volumeOptions(spec)...); err != nil {
		return nil, err
	}
	handle, err := getVolume(ctx, name)
	if err != nil {
		return nil, orphanedVolumeError(ctx, name, err)
	}
	volume, err := volumeHandleFrom(ctx, handle)
	if err != nil {
		return nil, orphanedVolumeError(ctx, name, err)
	}
	return volume, nil
}

// orphanedVolumeError removes a volume the caller never received a handle to and
// returns the resolving error. The removal is best-effort and detached from the
// caller's cancellation, so a cancelled or deadline-exceeded ctx cannot skip it;
// a removal failure is joined with the resolving error so the orphan is not
// silently lost.
func orphanedVolumeError(ctx context.Context, name string, cause error) error {
	if removeErr := removeVolume(context.WithoutCancel(ctx), name); removeErr != nil {
		return errors.Join(cause, fmt.Errorf("remove orphaned volume %q: %w", name, removeErr))
	}
	return cause
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := listVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeHandle, 0, len(handles))
	for _, h := range handles {
		handle, err := volumeHandleFrom(ctx, h)
		if err != nil {
			// A volume removed (or replaced, which reports the same not-found
			// kind) between the fetch and this observation is nothing to
			// enumerate. Any other error is real.
			if IsVolumeNotFound(err) {
				continue
			}
			return nil, err
		}
		result = append(result, handle)
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
	return report, nil
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

func (realMsbClient) StorageUsage(ctx context.Context) (*StorageReport, error) {
	report, err := storageUsage(ctx)
	if err != nil {
		return nil, err
	}
	return report, nil
}
