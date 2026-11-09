//deadcode:ignore reason="test support: referenced only by _test.go files"
package msbtest

import (
	"context"
	"io"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// MockRuntimeInstaller is a test double for RuntimeInstaller. A nil
// EnsureInstalledFn means succeed.
type MockRuntimeInstaller struct {
	EnsureInstalledFn func(ctx context.Context) error
}

// Compile-time check.
var _ msb.RuntimeInstaller = (*MockRuntimeInstaller)(nil)

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
	GetSandboxFn    func(ctx context.Context, name string) (msb.SandboxHandle, error)
	CreateSandboxFn func(ctx context.Context, name string, spec msb.SandboxSpec) (msb.Sandbox, error)
	ListSandboxesFn func(ctx context.Context, labels map[string]string) ([]msb.SandboxHandle, error)
}

// Compile-time check.
var _ msb.SandboxStore = (*MockSandboxStore)(nil)

// GetSandbox implements SandboxStore.
func (m *MockSandboxStore) GetSandbox(ctx context.Context, name string) (msb.SandboxHandle, error) {
	if m.GetSandboxFn != nil {
		return m.GetSandboxFn(ctx, name)
	}
	return nil, nil
}

// CreateSandbox implements SandboxStore.
func (m *MockSandboxStore) CreateSandbox(
	ctx context.Context,
	name string,
	spec msb.SandboxSpec,
) (msb.Sandbox, error) {
	if m.CreateSandboxFn != nil {
		return m.CreateSandboxFn(ctx, name, spec)
	}
	return nil, nil
}

// ListSandboxes implements SandboxStore.
func (m *MockSandboxStore) ListSandboxes(ctx context.Context, labels map[string]string) ([]msb.SandboxHandle, error) {
	if m.ListSandboxesFn != nil {
		return m.ListSandboxesFn(ctx, labels)
	}
	return nil, nil
}

// MockVolumeStore is a test double for VolumeStore. A nil callback means
// return the zero value with no error.
type MockVolumeStore struct {
	GetVolumeFn    func(ctx context.Context, name string) (msb.VolumeHandle, error)
	CreateVolumeFn func(ctx context.Context, name string, spec msb.VolumeSpec) (msb.VolumeHandle, error)
	ListVolumesFn  func(ctx context.Context) ([]msb.VolumeHandle, error)
	RemoveVolumeFn func(ctx context.Context, name string) error
}

// Compile-time check.
var _ msb.VolumeStore = (*MockVolumeStore)(nil)

// GetVolume implements VolumeStore.
func (m *MockVolumeStore) GetVolume(ctx context.Context, name string) (msb.VolumeHandle, error) {
	if m.GetVolumeFn != nil {
		return m.GetVolumeFn(ctx, name)
	}
	return nil, nil
}

// CreateVolume implements VolumeStore.
func (m *MockVolumeStore) CreateVolume(
	ctx context.Context,
	name string,
	spec msb.VolumeSpec,
) (msb.VolumeHandle, error) {
	if m.CreateVolumeFn != nil {
		return m.CreateVolumeFn(ctx, name, spec)
	}
	return nil, nil
}

// ListVolumes implements VolumeStore.
func (m *MockVolumeStore) ListVolumes(ctx context.Context) ([]msb.VolumeHandle, error) {
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
	ImageListFn          func(ctx context.Context) ([]msb.ImageHandle, error)
	ImagePruneFn         func(ctx context.Context) (*msb.ImagePruneReport, error)
	ImageRemoveFn        func(ctx context.Context, ref string, force bool) error
	ImageLoadFn          func(ctx context.Context, ref string, r io.Reader) error
	ImageInspectFn       func(ctx context.Context, ref string) (*msb.ImageConfig, error)
	ImageInspectDetailFn func(ctx context.Context, ref string) (*msb.ImageDetail, error)
}

// Compile-time check.
var _ msb.ImageStore = (*MockImageStore)(nil)

// ImageGet implements ImageStore.
func (m *MockImageStore) ImageGet(ctx context.Context, ref string) error {
	if m.ImageGetFn != nil {
		return m.ImageGetFn(ctx, ref)
	}
	return nil
}

// ImageList implements ImageStore.
func (m *MockImageStore) ImageList(ctx context.Context) ([]msb.ImageHandle, error) {
	if m.ImageListFn != nil {
		return m.ImageListFn(ctx)
	}
	return nil, nil
}

// ImagePrune implements ImageStore.
func (m *MockImageStore) ImagePrune(ctx context.Context) (*msb.ImagePruneReport, error) {
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
func (m *MockImageStore) ImageInspect(ctx context.Context, ref string) (*msb.ImageConfig, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, ref)
	}
	return nil, nil
}

// ImageInspectDetail implements ImageStore.
func (m *MockImageStore) ImageInspectDetail(ctx context.Context, ref string) (*msb.ImageDetail, error) {
	if m.ImageInspectDetailFn != nil {
		return m.ImageInspectDetailFn(ctx, ref)
	}
	return nil, nil
}

// MockStorageStore is a test double for StorageStore. A nil callback means
// return the zero value with no error.
type MockStorageStore struct {
	StorageUsageFn func(ctx context.Context) (*msb.StorageReport, error)
	PruneStorageFn func(ctx context.Context, req msb.PruneRequest) (*msb.PruneReport, error)
}

// Compile-time check.
var _ msb.StorageStore = (*MockStorageStore)(nil)

// StorageUsage implements StorageStore.
func (m *MockStorageStore) StorageUsage(ctx context.Context) (*msb.StorageReport, error) {
	if m.StorageUsageFn != nil {
		return m.StorageUsageFn(ctx)
	}
	return nil, nil
}

// PruneStorage implements StorageStore.
func (m *MockStorageStore) PruneStorage(
	ctx context.Context,
	req msb.PruneRequest,
) (*msb.PruneReport, error) {
	if m.PruneStorageFn != nil {
		return m.PruneStorageFn(ctx, req)
	}
	return nil, nil
}

// MockSandboxExecutor is a test double for SandboxExecutor. A nil callback
// means return the zero value with no error.
type MockSandboxExecutor struct {
	ShellFn func(ctx context.Context, command string, req msb.ExecRequest) (msb.ShellResult, error)
	ExecFn  func(ctx context.Context, command string, args []string, req msb.ExecRequest) (msb.ShellResult, error)
	FSFn    func() msb.SandboxFS
}

// Compile-time check.
var _ msb.SandboxExecutor = (*MockSandboxExecutor)(nil)

// Shell implements SandboxExecutor.
func (m *MockSandboxExecutor) Shell(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	if m.ShellFn != nil {
		return m.ShellFn(ctx, command, req)
	}
	return nil, nil
}

// Exec implements SandboxExecutor.
func (m *MockSandboxExecutor) Exec(
	ctx context.Context,
	command string,
	args []string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	if m.ExecFn != nil {
		return m.ExecFn(ctx, command, args, req)
	}
	return nil, nil
}

// FS implements SandboxExecutor.
func (m *MockSandboxExecutor) FS() msb.SandboxFS {
	if m.FSFn != nil {
		return m.FSFn()
	}
	return nil
}

// MockSandboxStreamer is a test double for SandboxStreamer. A nil callback
// means return the zero value with no error.
type MockSandboxStreamer struct {
	ShellFn       func(ctx context.Context, command string, req msb.ExecRequest) (msb.ShellResult, error)
	ShellStreamFn func(ctx context.Context, command string, req msb.ExecRequest) (msb.StreamHandle, error)
}

// Compile-time check.
var _ msb.SandboxStreamer = (*MockSandboxStreamer)(nil)

// Shell implements SandboxStreamer.
func (m *MockSandboxStreamer) Shell(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	if m.ShellFn != nil {
		return m.ShellFn(ctx, command, req)
	}
	return nil, nil
}

// ShellStream implements SandboxStreamer.
func (m *MockSandboxStreamer) ShellStream(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.StreamHandle, error) {
	if m.ShellStreamFn != nil {
		return m.ShellStreamFn(ctx, command, req)
	}
	return nil, nil
}

// MockSandboxAttacher is a test double for SandboxAttacher. A nil callback
// means return the zero value with no error.
type MockSandboxAttacher struct {
	AttachWithFn func(ctx context.Context, command string, args []string, req msb.AttachRequest) (int, error)
	AttachFn     func(ctx context.Context, command string, args ...string) (int, error)
	DetachFn     func(ctx context.Context) error
}

// Compile-time check.
var _ msb.SandboxAttacher = (*MockSandboxAttacher)(nil)

// AttachWith implements SandboxAttacher.
func (m *MockSandboxAttacher) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	req msb.AttachRequest,
) (int, error) {
	if m.AttachWithFn != nil {
		return m.AttachWithFn(ctx, command, args, req)
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
	ExecDetachedFn func(ctx context.Context, cmd string, args []string, spec msb.JobSpec) (msb.Job, error)
	GetJobFn       func(ctx context.Context, id string) (msb.Job, error)
	ListJobsFn     func(ctx context.Context, query msb.JobQuery) ([]msb.JobInfo, error)
}

// Compile-time check.
var _ msb.SandboxJobs = (*MockSandboxJobs)(nil)

// ExecDetached implements SandboxJobs.
func (m *MockSandboxJobs) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	spec msb.JobSpec,
) (msb.Job, error) {
	if m.ExecDetachedFn != nil {
		return m.ExecDetachedFn(ctx, cmd, args, spec)
	}
	return nil, nil
}

// GetJob implements SandboxJobs.
func (m *MockSandboxJobs) GetJob(ctx context.Context, id string) (msb.Job, error) {
	if m.GetJobFn != nil {
		return m.GetJobFn(ctx, id)
	}
	return nil, nil
}

// ListJobs implements SandboxJobs.
func (m *MockSandboxJobs) ListJobs(ctx context.Context, query msb.JobQuery) ([]msb.JobInfo, error) {
	if m.ListJobsFn != nil {
		return m.ListJobsFn(ctx, query)
	}
	return nil, nil
}

// MockSandboxObserver is a test double for SandboxObserver. A nil callback
// means return the zero value with no error.
type MockSandboxObserver struct {
	MetricsFn func(ctx context.Context) (*msb.Metrics, error)
	LogsFn    func(ctx context.Context, query msb.LogQuery) ([]msb.LogEntry, error)
}

// Compile-time check.
var _ msb.SandboxObserver = (*MockSandboxObserver)(nil)

// Metrics implements SandboxObserver.
func (m *MockSandboxObserver) Metrics(ctx context.Context) (*msb.Metrics, error) {
	if m.MetricsFn != nil {
		return m.MetricsFn(ctx)
	}
	return nil, nil
}

// Logs implements SandboxObserver.
func (m *MockSandboxObserver) Logs(ctx context.Context, query msb.LogQuery) ([]msb.LogEntry, error) {
	if m.LogsFn != nil {
		return m.LogsFn(ctx, query)
	}
	return nil, nil
}
