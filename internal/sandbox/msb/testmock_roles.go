//deadcode:ignore reason="test support: referenced only by _test.go files"
package msb

import (
	"context"
	"io"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// MockRuntimeInstaller is a test double for RuntimeInstaller. A nil
// EnsureInstalledFn means succeed.
type MockRuntimeInstaller struct {
	EnsureInstalledFn func(ctx context.Context) error
}

// Compile-time check.
var _ RuntimeInstaller = (*MockRuntimeInstaller)(nil)

// EnsureInstalled implements RuntimeInstaller.
func (m *MockRuntimeInstaller) EnsureInstalled(ctx context.Context) error {
	if m.EnsureInstalledFn != nil {
		return m.EnsureInstalledFn(ctx)
	}
	return nil
}

// MockSandboxStore is a test double for SandboxStore. A nil callback means
// return the zero value with no error.
type MockSandboxStore struct {
	GetSandboxFn    func(ctx context.Context, name string) (SandboxHandle, error)
	CreateSandboxFn func(ctx context.Context, name string, opts ...msbSdk.SandboxOption) (Sandbox, error)
	ListSandboxesFn func(ctx context.Context, labels map[string]string) ([]SandboxHandle, error)
}

// Compile-time check.
var _ SandboxStore = (*MockSandboxStore)(nil)

// GetSandbox implements SandboxStore.
func (m *MockSandboxStore) GetSandbox(ctx context.Context, name string) (SandboxHandle, error) {
	if m.GetSandboxFn != nil {
		return m.GetSandboxFn(ctx, name)
	}
	return nil, nil
}

// CreateSandbox implements SandboxStore.
func (m *MockSandboxStore) CreateSandbox(
	ctx context.Context,
	name string,
	opts ...msbSdk.SandboxOption,
) (Sandbox, error) {
	if m.CreateSandboxFn != nil {
		return m.CreateSandboxFn(ctx, name, opts...)
	}
	return nil, nil
}

// ListSandboxes implements SandboxStore.
func (m *MockSandboxStore) ListSandboxes(ctx context.Context, labels map[string]string) ([]SandboxHandle, error) {
	if m.ListSandboxesFn != nil {
		return m.ListSandboxesFn(ctx, labels)
	}
	return nil, nil
}

// MockVolumeStore is a test double for VolumeStore. A nil callback means
// return the zero value with no error.
type MockVolumeStore struct {
	GetVolumeFn    func(ctx context.Context, name string) (VolumeHandle, error)
	CreateVolumeFn func(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error)
	ListVolumesFn  func(ctx context.Context) ([]VolumeHandle, error)
	RemoveVolumeFn func(ctx context.Context, name string) error
}

// Compile-time check.
var _ VolumeStore = (*MockVolumeStore)(nil)

// GetVolume implements VolumeStore.
func (m *MockVolumeStore) GetVolume(ctx context.Context, name string) (VolumeHandle, error) {
	if m.GetVolumeFn != nil {
		return m.GetVolumeFn(ctx, name)
	}
	return nil, nil
}

// CreateVolume implements VolumeStore.
func (m *MockVolumeStore) CreateVolume(
	ctx context.Context,
	name string,
	opts ...msbSdk.VolumeOption,
) (VolumeHandle, error) {
	if m.CreateVolumeFn != nil {
		return m.CreateVolumeFn(ctx, name, opts...)
	}
	return nil, nil
}

// ListVolumes implements VolumeStore.
func (m *MockVolumeStore) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	if m.ListVolumesFn != nil {
		return m.ListVolumesFn(ctx)
	}
	return nil, nil
}

// RemoveVolume implements VolumeStore.
func (m *MockVolumeStore) RemoveVolume(ctx context.Context, name string) error {
	if m.RemoveVolumeFn != nil {
		return m.RemoveVolumeFn(ctx, name)
	}
	return nil
}

// MockImageStore is a test double for ImageStore. A nil callback means return
// the zero value with no error.
type MockImageStore struct {
	ImageGetFn           func(ctx context.Context, ref string) error
	ImageListFn          func(ctx context.Context) ([]ImageHandle, error)
	ImagePruneFn         func(ctx context.Context) (*msbSdk.ImagePruneReport, error)
	ImageRemoveFn        func(ctx context.Context, ref string, force bool) error
	ImageLoadFn          func(ctx context.Context, ref string, r io.Reader) error
	ImageInspectFn       func(ctx context.Context, ref string) (*msbSdk.ImageConfig, error)
	ImageInspectDetailFn func(ctx context.Context, ref string) (*ImageDetail, error)
}

// Compile-time check.
var _ ImageStore = (*MockImageStore)(nil)

// ImageGet implements ImageStore.
func (m *MockImageStore) ImageGet(ctx context.Context, ref string) error {
	if m.ImageGetFn != nil {
		return m.ImageGetFn(ctx, ref)
	}
	return nil
}

// ImageList implements ImageStore.
func (m *MockImageStore) ImageList(ctx context.Context) ([]ImageHandle, error) {
	if m.ImageListFn != nil {
		return m.ImageListFn(ctx)
	}
	return nil, nil
}

// ImagePrune implements ImageStore.
func (m *MockImageStore) ImagePrune(ctx context.Context) (*msbSdk.ImagePruneReport, error) {
	if m.ImagePruneFn != nil {
		return m.ImagePruneFn(ctx)
	}
	return nil, nil
}

// ImageRemove implements ImageStore.
func (m *MockImageStore) ImageRemove(ctx context.Context, ref string, force bool) error {
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, ref, force)
	}
	return nil
}

// ImageLoad implements ImageStore.
func (m *MockImageStore) ImageLoad(ctx context.Context, ref string, r io.Reader) error {
	if m.ImageLoadFn != nil {
		return m.ImageLoadFn(ctx, ref, r)
	}
	return nil
}

// ImageInspect implements ImageStore.
func (m *MockImageStore) ImageInspect(ctx context.Context, ref string) (*msbSdk.ImageConfig, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, ref)
	}
	return nil, nil
}

// ImageInspectDetail implements ImageStore.
func (m *MockImageStore) ImageInspectDetail(ctx context.Context, ref string) (*ImageDetail, error) {
	if m.ImageInspectDetailFn != nil {
		return m.ImageInspectDetailFn(ctx, ref)
	}
	return nil, nil
}

// MockStorageStore is a test double for StorageStore. A nil callback means
// return the zero value with no error.
type MockStorageStore struct {
	StorageUsageFn func(ctx context.Context) (*msbSdk.StorageUsageReport, error)
	PruneStorageFn func(ctx context.Context, options msbSdk.StoragePruneOptions) (*msbSdk.StoragePruneReport, error)
}

// Compile-time check.
var _ StorageStore = (*MockStorageStore)(nil)

// StorageUsage implements StorageStore.
func (m *MockStorageStore) StorageUsage(ctx context.Context) (*msbSdk.StorageUsageReport, error) {
	if m.StorageUsageFn != nil {
		return m.StorageUsageFn(ctx)
	}
	return nil, nil
}

// PruneStorage implements StorageStore.
func (m *MockStorageStore) PruneStorage(
	ctx context.Context,
	options msbSdk.StoragePruneOptions,
) (*msbSdk.StoragePruneReport, error) {
	if m.PruneStorageFn != nil {
		return m.PruneStorageFn(ctx, options)
	}
	return nil, nil
}

// MockSandboxExecutor is a test double for SandboxExecutor. A nil callback
// means return the zero value with no error.
type MockSandboxExecutor struct {
	ShellFn func(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	ExecFn  func(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error)
	FSFn    func() SandboxFS
}

// Compile-time check.
var _ SandboxExecutor = (*MockSandboxExecutor)(nil)

// Shell implements SandboxExecutor.
func (m *MockSandboxExecutor) Shell(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	if m.ShellFn != nil {
		return m.ShellFn(ctx, command, opts...)
	}
	return nil, nil
}

// Exec implements SandboxExecutor.
func (m *MockSandboxExecutor) Exec(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	if m.ExecFn != nil {
		return m.ExecFn(ctx, command, args, opts...)
	}
	return nil, nil
}

// FS implements SandboxExecutor.
func (m *MockSandboxExecutor) FS() SandboxFS {
	if m.FSFn != nil {
		return m.FSFn()
	}
	return nil
}

// MockSandboxStreamer is a test double for SandboxStreamer. A nil callback
// means return the zero value with no error.
type MockSandboxStreamer struct {
	ShellFn       func(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	ShellStreamFn func(ctx context.Context, command string, opts ...msbSdk.ExecOption) (StreamHandle, error)
}

// Compile-time check.
var _ SandboxStreamer = (*MockSandboxStreamer)(nil)

// Shell implements SandboxStreamer.
func (m *MockSandboxStreamer) Shell(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	if m.ShellFn != nil {
		return m.ShellFn(ctx, command, opts...)
	}
	return nil, nil
}

// ShellStream implements SandboxStreamer.
func (m *MockSandboxStreamer) ShellStream(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (StreamHandle, error) {
	if m.ShellStreamFn != nil {
		return m.ShellStreamFn(ctx, command, opts...)
	}
	return nil, nil
}

// MockSandboxAttacher is a test double for SandboxAttacher. A nil callback
// means return the zero value with no error.
type MockSandboxAttacher struct {
	AttachWithFn func(ctx context.Context, command string, args []string, opts ...msbSdk.AttachOption) (int, error)
	AttachFn     func(ctx context.Context, command string, args ...string) (int, error)
	DetachFn     func(ctx context.Context) error
}

// Compile-time check.
var _ SandboxAttacher = (*MockSandboxAttacher)(nil)

// AttachWith implements SandboxAttacher.
func (m *MockSandboxAttacher) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.AttachOption,
) (int, error) {
	if m.AttachWithFn != nil {
		return m.AttachWithFn(ctx, command, args, opts...)
	}
	return 0, nil
}

// Attach implements SandboxAttacher.
func (m *MockSandboxAttacher) Attach(ctx context.Context, command string, args ...string) (int, error) {
	if m.AttachFn != nil {
		return m.AttachFn(ctx, command, args...)
	}
	return 0, nil
}

// Detach implements SandboxAttacher.
func (m *MockSandboxAttacher) Detach(ctx context.Context) error {
	if m.DetachFn != nil {
		return m.DetachFn(ctx)
	}
	return nil
}

// MockSandboxJobs is a test double for SandboxJobs. A nil callback means return
// the zero value with no error.
type MockSandboxJobs struct {
	ExecDetachedFn func(ctx context.Context, cmd string, args []string, opts ...msbSdk.JobExecOption) (Job, error)
	GetJobFn       func(ctx context.Context, id string) (Job, error)
	ListJobsFn     func(ctx context.Context, opts ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error)
}

// Compile-time check.
var _ SandboxJobs = (*MockSandboxJobs)(nil)

// ExecDetached implements SandboxJobs.
func (m *MockSandboxJobs) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	opts ...msbSdk.JobExecOption,
) (Job, error) {
	if m.ExecDetachedFn != nil {
		return m.ExecDetachedFn(ctx, cmd, args, opts...)
	}
	return nil, nil
}

// GetJob implements SandboxJobs.
func (m *MockSandboxJobs) GetJob(ctx context.Context, id string) (Job, error) {
	if m.GetJobFn != nil {
		return m.GetJobFn(ctx, id)
	}
	return nil, nil
}

// ListJobs implements SandboxJobs.
func (m *MockSandboxJobs) ListJobs(ctx context.Context, opts ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error) {
	if m.ListJobsFn != nil {
		return m.ListJobsFn(ctx, opts...)
	}
	return nil, nil
}

// MockSandboxObserver is a test double for SandboxObserver. A nil callback
// means return the zero value with no error.
type MockSandboxObserver struct {
	MetricsFn func(ctx context.Context) (*msbSdk.Metrics, error)
	LogsFn    func(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error)
}

// Compile-time check.
var _ SandboxObserver = (*MockSandboxObserver)(nil)

// Metrics implements SandboxObserver.
func (m *MockSandboxObserver) Metrics(ctx context.Context) (*msbSdk.Metrics, error) {
	if m.MetricsFn != nil {
		return m.MetricsFn(ctx)
	}
	return nil, nil
}

// Logs implements SandboxObserver.
func (m *MockSandboxObserver) Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error) {
	if m.LogsFn != nil {
		return m.LogsFn(ctx, opts)
	}
	return nil, nil
}
