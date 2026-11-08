//deadcode:ignore reason="test support: referenced only by _test.go files"
package msbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// MockCreateSandboxCall tracks a CreateSandbox call made on MockMsbClient.
type MockCreateSandboxCall struct {
	Name string
	Spec msb.SandboxSpec
}

// MockRemoveImageCall tracks an ImageRemove call made on MockMsbClient.
type MockRemoveImageCall struct {
	Ref   string
	Force bool
}

// MockDetachedCall tracks an ExecDetached call made on MockSandbox.
type MockDetachedCall struct {
	Cmd  string
	Args []string
	Spec msb.JobSpec
}

// MockJob is a test double for Job. Nil callbacks return the configured fields.
type MockJob struct {
	Info      *msb.JobInfo
	InfoErr   error
	Logs_     []msb.JobLogEntry
	LogsErr   error
	KillErr   error
	CloseErr  error
	InspectFn func(ctx context.Context) (*msb.JobInfo, error)
	LogsFn    func(ctx context.Context, query msb.JobLogQuery) ([]msb.JobLogEntry, error)
	DidKill   bool
	DidClose  bool
}

// Compile-time check.
var _ msb.Job = (*MockJob)(nil)

func (m *MockJob) Inspect(ctx context.Context) (*msb.JobInfo, error) {
	if m.InspectFn != nil {
		return m.InspectFn(ctx)
	}
	return m.Info, m.InfoErr
}

func (m *MockJob) Kill(context.Context) error {
	m.DidKill = true
	return m.KillErr
}

func (m *MockJob) Logs(ctx context.Context, query msb.JobLogQuery) ([]msb.JobLogEntry, error) {
	if m.LogsFn != nil {
		return m.LogsFn(ctx, query)
	}
	return m.Logs_, m.LogsErr
}

func (m *MockJob) Close() error {
	m.DidClose = true
	return m.CloseErr
}

// MockMsbClient is a test double for Client with callback-style fields
// and collection fields for convenient test setup. Nil callbacks mean no-op/succeed.
// Existing callers that set Sandboxes/Volumes/Images/List*Err fields work without change.
type MockMsbClient struct {
	mu sync.Mutex

	// Callback-style fields (granular control). Nil = no-op/succeed.
	EnsureInstalledFn    func(ctx context.Context) error
	GetSandboxFn         func(ctx context.Context, name string) (msb.SandboxHandle, error)
	CreateSandboxFn      func(ctx context.Context, name string, spec msb.SandboxSpec) (msb.Sandbox, error)
	ListSandboxesFn      func(ctx context.Context, labels map[string]string) ([]msb.SandboxHandle, error)
	GetVolumeFn          func(ctx context.Context, name string) (msb.VolumeHandle, error)
	CreateVolumeFn       func(ctx context.Context, name string, spec msb.VolumeSpec) (msb.VolumeHandle, error)
	ListVolumesFn        func(ctx context.Context) ([]msb.VolumeHandle, error)
	RemoveVolumeFn       func(ctx context.Context, name string) error
	ImageGetFn           func(ctx context.Context, ref string) error
	ImageListFn          func(ctx context.Context) ([]msb.ImageHandle, error)
	ImagePruneFn         func(ctx context.Context) (*msb.ImagePruneReport, error)
	ImageRemoveFn        func(ctx context.Context, ref string, force bool) error
	ImageLoadFn          func(ctx context.Context, ref string, r io.Reader) error
	ImageInspectFn       func(ctx context.Context, ref string) (*msb.ImageConfig, error)
	ImageInspectDetailFn func(ctx context.Context, ref string) (*msb.ImageDetail, error)
	StorageUsageFn       func(ctx context.Context) (*msb.StorageReport, error)
	StoragePruneFn       func(ctx context.Context, req msb.PruneRequest) (*msb.PruneReport, error)

	// Pre-populated collections — List* methods return these when the callback is nil.
	// Tests can append to these fields freely.
	Sandboxes []msb.SandboxHandle
	Volumes   []msb.VolumeHandle
	Images    []msb.ImageHandle

	// CreatedSandbox is returned by CreateSandbox instead of the default MockSandbox.
	CreatedSandbox msb.Sandbox

	// CreatedSandboxes holds names passed to CreateSandbox calls.
	CreatedSandboxes []string
	// CreatedSandboxCalls holds full details of CreateSandbox calls.
	CreatedSandboxCalls []MockCreateSandboxCall
	// RemovedVolumes holds names passed to RemoveVolume calls.
	RemovedVolumes []string
	// RemovedImages holds arguments passed to ImageRemove calls.
	RemovedImages []MockRemoveImageCall
	// LoadedImages holds refs passed to ImageLoad calls.
	LoadedImages []string
	// StoragePruneCalls holds the requests passed to PruneStorage calls.
	StoragePruneCalls []msb.PruneRequest

	// Error fields.
	CreateSandboxErr error
	ListSandboxesErr error
	GetVolumeErr     error
	ListVolumesErr   error
	ListImagesErr    error
	storageUsageErr  error
}

// Compile-time check.
var _ msb.Client = (*MockMsbClient)(nil)

// namedHandle is implemented by mock handles that expose a Name.
type namedHandle interface{ Name() string }

// resolveHandle applies the MockMsbClient lookup fallback shared by
// GetSandbox and GetVolume: the injected error, the pre-seeded handle, a scan
// of the collection by name, then a not-found SDK error.
func resolveHandle[T namedHandle](
	name string,
	handles []T,
	err error,
	notFoundKind msbSdk.ErrorKind,
) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	for _, h := range handles {
		if h.Name() == name {
			return h, nil
		}
	}
	var zero T
	return zero, &msbSdk.Error{Kind: notFoundKind, Message: name}
}

// EnsureInstalled implements Client.
func (m *MockMsbClient) EnsureInstalled(ctx context.Context) error {
	if m.EnsureInstalledFn != nil {
		return m.EnsureInstalledFn(ctx)
	}
	return nil
}

// GetSandbox implements Client.
func (m *MockMsbClient) GetSandbox(ctx context.Context, name string) (msb.SandboxHandle, error) {
	if m.GetSandboxFn != nil {
		return m.GetSandboxFn(ctx, name)
	}
	return resolveHandle(name, m.Sandboxes, nil, msbSdk.ErrSandboxNotFound)
}

// CreateSandbox implements Client.
func (m *MockMsbClient) CreateSandbox(ctx context.Context, name string, spec msb.SandboxSpec) (msb.Sandbox, error) {
	m.mu.Lock()
	m.CreatedSandboxes = append(m.CreatedSandboxes, name)
	m.CreatedSandboxCalls = append(m.CreatedSandboxCalls, MockCreateSandboxCall{Name: name, Spec: spec})
	m.mu.Unlock()

	if m.CreateSandboxFn != nil {
		return m.CreateSandboxFn(ctx, name, spec)
	}
	if m.CreateSandboxErr != nil {
		return nil, m.CreateSandboxErr
	}
	if m.CreatedSandbox != nil {
		return m.CreatedSandbox, nil
	}
	return &MockSandbox{Name_: name}, nil
}

// ListSandboxes implements Client.
func (m *MockMsbClient) ListSandboxes(ctx context.Context, labels map[string]string) ([]msb.SandboxHandle, error) {
	if m.ListSandboxesFn != nil {
		return m.ListSandboxesFn(ctx, labels)
	}
	if m.ListSandboxesErr != nil {
		return nil, m.ListSandboxesErr
	}
	return m.Sandboxes, nil
}

// GetVolume implements Client.
func (m *MockMsbClient) GetVolume(ctx context.Context, name string) (msb.VolumeHandle, error) {
	if m.GetVolumeFn != nil {
		return m.GetVolumeFn(ctx, name)
	}
	return resolveHandle(name, m.Volumes, m.GetVolumeErr, msbSdk.ErrVolumeNotFound)
}

// CreateVolume implements Client.
func (m *MockMsbClient) CreateVolume(
	ctx context.Context,
	name string,
	spec msb.VolumeSpec,
) (msb.VolumeHandle, error) {
	if m.CreateVolumeFn != nil {
		return m.CreateVolumeFn(ctx, name, spec)
	}
	return &MockVolumeHandle{Name_: name}, nil
}

// ListVolumes implements Client.
func (m *MockMsbClient) ListVolumes(ctx context.Context) ([]msb.VolumeHandle, error) {
	if m.ListVolumesFn != nil {
		return m.ListVolumesFn(ctx)
	}
	if m.ListVolumesErr != nil {
		return nil, m.ListVolumesErr
	}
	return m.Volumes, nil
}

// RemoveVolume implements Client.
func (m *MockMsbClient) RemoveVolume(ctx context.Context, name string) error {
	m.mu.Lock()
	m.RemovedVolumes = append(m.RemovedVolumes, name)
	m.mu.Unlock()
	if m.RemoveVolumeFn != nil {
		return m.RemoveVolumeFn(ctx, name)
	}
	return nil
}

// ImageGet implements Client.
func (m *MockMsbClient) ImageGet(ctx context.Context, ref string) error {
	if m.ImageGetFn != nil {
		return m.ImageGetFn(ctx, ref)
	}
	return nil
}

// ImageList implements Client.
func (m *MockMsbClient) ImageList(ctx context.Context) ([]msb.ImageHandle, error) {
	if m.ImageListFn != nil {
		return m.ImageListFn(ctx)
	}
	if m.ListImagesErr != nil {
		return nil, m.ListImagesErr
	}
	return m.Images, nil
}

// ImagePrune implements Client.
func (m *MockMsbClient) ImagePrune(ctx context.Context) (*msb.ImagePruneReport, error) {
	if m.ImagePruneFn != nil {
		return m.ImagePruneFn(ctx)
	}
	return &msb.ImagePruneReport{}, nil
}

// PruneStorage implements Client, recording every call's request.
func (m *MockMsbClient) PruneStorage(
	ctx context.Context,
	req msb.PruneRequest,
) (*msb.PruneReport, error) {
	m.mu.Lock()
	m.StoragePruneCalls = append(m.StoragePruneCalls, req)
	// gomutants:disable-next-line STATEMENT_REMOVE reason="a test double's mutex unlock is only observable under concurrency, where a missing unlock deadlocks rather than failing an assertion"
	m.mu.Unlock()
	if m.StoragePruneFn != nil {
		return m.StoragePruneFn(ctx, req)
	}
	return &msb.PruneReport{}, nil
}

// ImageRemove implements Client.
func (m *MockMsbClient) ImageRemove(ctx context.Context, ref string, force bool) error {
	m.mu.Lock()
	m.RemovedImages = append(m.RemovedImages, MockRemoveImageCall{Ref: ref, Force: force})
	m.mu.Unlock()
	if m.ImageRemoveFn != nil {
		return m.ImageRemoveFn(ctx, ref, force)
	}
	return nil
}

// ImageLoad implements Client.
func (m *MockMsbClient) ImageLoad(ctx context.Context, ref string, r io.Reader) error {
	m.mu.Lock()
	m.LoadedImages = append(m.LoadedImages, ref)
	m.mu.Unlock()
	if m.ImageLoadFn != nil {
		return m.ImageLoadFn(ctx, ref, r)
	}
	return nil
}

func (m *MockMsbClient) ImageInspect(ctx context.Context, ref string) (*msb.ImageConfig, error) {
	if m.ImageInspectFn != nil {
		return m.ImageInspectFn(ctx, ref)
	}
	return &msb.ImageConfig{}, nil
}

func (m *MockMsbClient) ImageInspectDetail(ctx context.Context, ref string) (*msb.ImageDetail, error) {
	if m.ImageInspectDetailFn != nil {
		return m.ImageInspectDetailFn(ctx, ref)
	}
	return &msb.ImageDetail{Reference: ref}, nil
}

// StorageUsage implements Client.
func (m *MockMsbClient) StorageUsage(ctx context.Context) (*msb.StorageReport, error) {
	if m.StorageUsageFn != nil {
		return m.StorageUsageFn(ctx)
	}
	if m.storageUsageErr != nil {
		return nil, m.storageUsageErr
	}
	return &msb.StorageReport{}, nil
}

// SetStorageUsageErr sets the error returned by MockMsbClient.StorageUsage.
func (m *MockMsbClient) SetStorageUsageErr(err error) *MockMsbClient {
	m.storageUsageErr = err
	return m
}

// SetGetSandboxErr sets the error returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGetSandboxErr(err error) *MockMsbClient {
	if err != nil {
		m.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return nil, err
		}
	} else {
		m.GetSandboxFn = nil
	}
	return m
}

// SetGotSandbox sets the sandbox handle returned by MockMsbClient.GetSandbox.
func (m *MockMsbClient) SetGotSandbox(h msb.SandboxHandle) *MockMsbClient {
	if h != nil {
		m.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
			return h, nil
		}
	} else {
		m.GetSandboxFn = nil
	}
	return m
}

// SetGetVolumeErr sets the error returned by MockMsbClient.GetVolume.
func (m *MockMsbClient) SetGetVolumeErr(err error) *MockMsbClient {
	if err != nil {
		m.GetVolumeFn = func(_ context.Context, _ string) (msb.VolumeHandle, error) {
			return nil, err
		}
	} else {
		m.GetVolumeFn = nil
	}
	return m
}

// -- Domain mocks --

// MockSandboxHandle is a test double for SandboxHandle.
type MockSandboxHandle struct {
	Name_        string
	ID_          string
	Status_      msb.SandboxStatus
	UpdatedAt_   time.Time
	CreatedAt_   time.Time
	BackendKind_ msb.BackendKind
	Image_       string
	ConnectSb    msb.Sandbox
	// ConnectOrStartFn overrides ConnectOrStart's status-driven behaviour.
	ConnectOrStartFn func(context.Context) (msb.Sandbox, bool, error)
	StartSb          msb.Sandbox
	DidRmv           bool
	DidStop          bool
	DidKill          bool
	ConnectErr       error
	StartErr         error
	StopErr          error
	KillErr          error
	RemoveErr        error
	DestroyFn        func(context.Context) error
	DestroyErr       error
	Destroyed        bool
	Cfg              *msb.SandboxConfig
	ConfigErr        error
	Mounts           map[string]msb.MountConfig
	MountsErr        error
	PortBindings     []msb.PortBinding
	PortsErr         error
	ModifyErr        error
	Plan             *msb.SandboxModificationPlan
	ModifiedOptions  []msb.ModifyOptions
	CompactResult    *msb.DiskCompactionResult
	CompactErr       error
	CompactedOptions []msb.CompactionRequest
	MetricsResult    *msb.Metrics
	MetricsErr       error
	LogsResult       []msb.LogEntry
	LogsErr          error
	LogsOptions      []msb.LogQuery
}

func (m *MockSandboxHandle) Name() string                 { return m.Name_ }
func (m *MockSandboxHandle) ID() string                   { return m.ID_ }
func (m *MockSandboxHandle) Status() msb.SandboxStatus    { return m.Status_ }
func (m *MockSandboxHandle) UpdatedAt() time.Time         { return m.UpdatedAt_ }
func (m *MockSandboxHandle) CreatedAt() time.Time         { return m.CreatedAt_ }
func (m *MockSandboxHandle) BackendKind() msb.BackendKind { return m.BackendKind_ }
func (m *MockSandboxHandle) Image() string {
	if m.Image_ != "" {
		return m.Image_
	}
	if m.Cfg != nil {
		return m.Cfg.Image
	}
	return ""
}

func (m *MockSandboxHandle) Connect(_ context.Context) (msb.Sandbox, error) {
	if m.ConnectErr != nil {
		return nil, m.ConnectErr
	}
	if m.ConnectSb != nil {
		return m.ConnectSb, nil
	}
	return &MockSandbox{Name_: m.Name_}, nil
}

// ConnectOrStart mirrors the runtime by Status_ unless ConnectOrStartFn is set:
// a running or starting sandbox is connected (ConnectSb/ConnectErr), a created,
// stopped or crashed one is booted (StartSb/StartErr), and any other status is
// refused.
func (m *MockSandboxHandle) ConnectOrStart(ctx context.Context) (msb.Sandbox, bool, error) {
	if m.ConnectOrStartFn != nil {
		return m.ConnectOrStartFn(ctx)
	}
	switch m.Status_ {
	case msb.SandboxStatusRunning, msb.SandboxStatusStarting:
		sb, err := m.Connect(ctx)
		return sb, false, err
	case msb.SandboxStatusCreated, msb.SandboxStatusStopped, msb.SandboxStatusCrashed:
		if m.StartErr != nil {
			return nil, false, m.StartErr
		}
		if m.StartSb != nil {
			return m.StartSb, true, nil
		}
		return &MockSandbox{Name_: m.Name_}, true, nil
	case msb.SandboxStatusDraining, msb.SandboxStatusPaused:
		return nil, false, fmt.Errorf("sandbox %q is %q and cannot take a new client", m.Name_, m.Status_)
	}
	return nil, false, fmt.Errorf("sandbox %q has unexpected status %q", m.Name_, m.Status_)
}

func (m *MockSandboxHandle) Stop(_ context.Context, _ msb.LifecycleOptions) error {
	m.DidStop = true
	return m.StopErr
}

func (m *MockSandboxHandle) Kill(_ context.Context, _ msb.LifecycleOptions) error {
	m.DidKill = true
	return m.KillErr
}

func (m *MockSandboxHandle) Remove(_ context.Context) error {
	if m.RemoveErr != nil {
		return m.RemoveErr
	}
	m.DidRmv = true
	return nil
}
func (m *MockSandboxHandle) DidRemove() bool { return m.DidRmv }
func (m *MockSandboxHandle) Destroy(ctx context.Context, _ msb.LifecycleOptions) error {
	if m.DestroyFn != nil {
		return m.DestroyFn(ctx)
	}
	if m.DestroyErr != nil {
		return m.DestroyErr
	}
	m.Destroyed = true
	return nil
}

func (m *MockSandboxHandle) DidDestroy() bool { return m.Destroyed }

func (m *MockSandboxHandle) Config() (*msb.SandboxConfig, error) {
	return m.Cfg, m.ConfigErr
}

// ConfigMounts is seeded directly rather than derived from a persisted config
// JSON, which the mock has no equivalent of: the real handle's wiring of
// ParseConfigMounts over ConfigJSON() needs the FFI runtime to exercise. The
// parser it calls carries the tests for the decode itself.
func (m *MockSandboxHandle) ConfigMounts() (map[string]msb.MountConfig, error) {
	if m.MountsErr != nil {
		return nil, m.MountsErr
	}
	return m.Mounts, nil
}

// ConfigPorts is seeded like ConfigMounts: the real handle's ParseConfigPorts
// over ConfigJSON() needs the FFI runtime, so the mock carries the decoded
// bindings directly and the parser carries the decode tests.
func (m *MockSandboxHandle) ConfigPorts() ([]msb.PortBinding, error) {
	if m.PortsErr != nil {
		return nil, m.PortsErr
	}
	return m.PortBindings, nil
}

func (m *MockSandboxHandle) Modify(
	_ context.Context,
	opts msb.ModifyOptions,
) (*msb.SandboxModificationPlan, error) {
	m.ModifiedOptions = append(m.ModifiedOptions, opts)
	return m.Plan, m.ModifyErr
}

func (m *MockSandboxHandle) Compact(
	_ context.Context,
	req msb.CompactionRequest,
) (*msb.DiskCompactionResult, error) {
	m.CompactedOptions = append(m.CompactedOptions, req)
	return m.CompactResult, m.CompactErr
}

func (m *MockSandboxHandle) Metrics(_ context.Context) (*msb.Metrics, error) {
	return m.MetricsResult, m.MetricsErr
}

func (m *MockSandboxHandle) Logs(_ context.Context, query msb.LogQuery) ([]msb.LogEntry, error) {
	m.LogsOptions = append(m.LogsOptions, query)
	return m.LogsResult, m.LogsErr
}

// MockSandbox is a test double for Sandbox.
type MockSandbox struct {
	Name_      string
	FSValue_   any
	ShellOut   map[string]msb.ShellResult
	ShellErr   error
	ShellCalls *[]string
	ExecOut    map[string]msb.ShellResult
	ExecErr    error
	ExecCalls  *[]string
	AttachCode int
	AttachErr  error
	// AttachFn, when set, runs inside AttachWith with its ctx and supplies the
	// result instead of AttachCode/AttachErr.
	AttachFn   func(ctx context.Context) (int, error)
	AttachUser string
	AttachCwd  string
	// AttachEnv is the environment the last AttachWith passed.
	AttachEnv  map[string]string
	AttachCmd  string
	AttachArgs []string
	DetachErr  error
	// DetachFn overrides DetachErr when set, so a test can observe the
	// context a detach runs under.
	DetachFn   func(ctx context.Context) error
	DestroyErr error
	Destroyed  bool
	CloseErr   error

	// Detached jobs. ExecDetached returns Job_ (or a fresh MockJob) when
	// ExecDetachedFn is nil; ListJobs returns Jobs when ListJobsFn is nil.
	ExecDetachedFn    func(ctx context.Context, cmd string, args []string, spec msb.JobSpec) (msb.Job, error)
	ExecDetachedCalls *[]MockDetachedCall
	Job_              msb.Job
	GetJobFn          func(ctx context.Context, id string) (msb.Job, error)
	ListJobsFn        func(ctx context.Context, query msb.JobQuery) ([]msb.JobInfo, error)
	Jobs              []msb.JobInfo

	// StreamHandle_ is returned by ShellStream when ShellStreamFn is nil. When
	// both are nil, a no-op handle is returned that immediately reports
	// stream-closed on Recv.
	StreamHandle_ msb.StreamHandle
	// ShellStreamFn overrides StreamHandle_ when set. Useful for reconnect
	// tests that must return a different handle per call.
	ShellStreamFn func(command string) (msb.StreamHandle, error)
	// ShellStreamCmds records every command passed to ShellStream.
	ShellStreamCmds *[]string

	// Metrics and Logs back the seam's resource-pressure reads.
	MetricsResult *msb.Metrics
	MetricsErr    error
	LogsResult    []msb.LogEntry
	LogsErr       error
	// LogsOptions records every LogQuery passed to Logs.
	LogsOptions []msb.LogQuery
}

func (m *MockSandbox) FS() msb.SandboxFS {
	if f, ok := m.FSValue_.(msb.SandboxFS); ok {
		return f
	}
	return &TestFS{}
}

func (m *MockSandbox) Shell(_ context.Context, command string, _ msb.ExecRequest) (msb.ShellResult, error) {
	if m.ShellCalls != nil {
		*m.ShellCalls = append(*m.ShellCalls, command)
	}
	if m.ShellErr != nil {
		return nil, m.ShellErr
	}
	if out, ok := m.ShellOut[command]; ok {
		return out, nil
	}
	// Return successful result when no override is configured.
	return &TestResult{success: true}, nil
}

func (m *MockSandbox) Exec(
	_ context.Context,
	command string,
	args []string,
	_ msb.ExecRequest,
) (msb.ShellResult, error) {
	if m.ExecErr != nil {
		return nil, m.ExecErr
	}
	key := command + " " + strings.Join(args, " ")
	if m.ExecCalls != nil {
		*m.ExecCalls = append(*m.ExecCalls, key)
	}
	if out, ok := m.ExecOut[key]; ok {
		return out, nil
	}
	// Return successful result when no override is configured.
	return &TestResult{success: true}, nil
}

func (m *MockSandbox) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	spec msb.JobSpec,
) (msb.Job, error) {
	if m.ExecDetachedCalls != nil {
		*m.ExecDetachedCalls = append(*m.ExecDetachedCalls, MockDetachedCall{Cmd: cmd, Args: args, Spec: spec})
	}
	if m.ExecDetachedFn != nil {
		return m.ExecDetachedFn(ctx, cmd, args, spec)
	}
	if m.Job_ != nil {
		return m.Job_, nil
	}
	return &MockJob{}, nil
}

func (m *MockSandbox) GetJob(ctx context.Context, id string) (msb.Job, error) {
	if m.GetJobFn != nil {
		return m.GetJobFn(ctx, id)
	}
	if m.Job_ != nil {
		return m.Job_, nil
	}
	return &MockJob{}, nil
}

func (m *MockSandbox) ListJobs(ctx context.Context, query msb.JobQuery) ([]msb.JobInfo, error) {
	if m.ListJobsFn != nil {
		return m.ListJobsFn(ctx, query)
	}
	return m.Jobs, nil
}

func (m *MockSandbox) Attach(_ context.Context, _ string, _ ...string) (int, error) {
	return m.AttachCode, m.AttachErr
}

func (m *MockSandbox) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	req msb.AttachRequest,
) (int, error) {
	m.AttachCmd = command
	m.AttachArgs = args
	m.AttachUser = req.User
	m.AttachCwd = req.Cwd
	m.AttachEnv = req.Env
	if m.AttachFn != nil {
		return m.AttachFn(ctx)
	}
	return m.AttachCode, m.AttachErr
}

func (m *MockSandbox) Detach(ctx context.Context) error {
	if m.DetachFn != nil {
		return m.DetachFn(ctx)
	}
	return m.DetachErr
}

func (m *MockSandbox) Destroy(_ context.Context, _ msb.LifecycleOptions) error {
	if m.DestroyErr != nil {
		return m.DestroyErr
	}
	m.Destroyed = true
	return nil
}
func (m *MockSandbox) DidDestroy() bool { return m.Destroyed }
func (m *MockSandbox) Close() error     { return m.CloseErr }

func (m *MockSandbox) Metrics(_ context.Context) (*msb.Metrics, error) {
	return m.MetricsResult, m.MetricsErr
}

func (m *MockSandbox) Logs(_ context.Context, query msb.LogQuery) ([]msb.LogEntry, error) {
	m.LogsOptions = append(m.LogsOptions, query)
	return m.LogsResult, m.LogsErr
}

func (m *MockSandbox) ShellStream(
	_ context.Context,
	command string,
	_ msb.ExecRequest,
) (msb.StreamHandle, error) {
	if m.ShellStreamCmds != nil {
		*m.ShellStreamCmds = append(*m.ShellStreamCmds, command)
	}
	if m.ShellStreamFn != nil {
		return m.ShellStreamFn(command)
	}
	if m.StreamHandle_ != nil {
		return m.StreamHandle_, nil
	}
	return &emptyStreamHandle{}, nil
}

// emptyStreamHandle returns stream-closed immediately; tests that need real
// streaming provide a StreamHandle_ with events.
type emptyStreamHandle struct{}

func (emptyStreamHandle) Recv(context.Context) (msb.StreamEvent, error) {
	return msb.StreamEvent{}, errors.New("stream closed")
}
func (emptyStreamHandle) Close() error { return nil }

// SandboxOpts configures a MockSandbox via NewMockSandbox.
// Zero/unset values produce sensible defaults.
type SandboxOpts struct {
	FSValue    any
	ShellOut   map[string]msb.ShellResult
	ShellErr   error
	ExecOut    map[string]msb.ShellResult
	ExecErr    error
	AttachCode int
	AttachErr  error
	DetachErr  error
	DestroyErr error
	CloseErr   error
	// StreamHandle_ is returned by ShellStream when ShellStreamFn is nil.
	StreamHandle_ msb.StreamHandle
	// ShellStreamFn overrides StreamHandle_ when set.
	ShellStreamFn func(command string) (msb.StreamHandle, error)
	// ShellStreamCmds records every command passed to ShellStream.
	ShellStreamCmds *[]string

	// Detached jobs.
	ExecDetachedFn    func(ctx context.Context, cmd string, args []string, spec msb.JobSpec) (msb.Job, error)
	ExecDetachedCalls *[]MockDetachedCall
	Job_              msb.Job
	GetJobFn          func(ctx context.Context, id string) (msb.Job, error)
	ListJobsFn        func(ctx context.Context, query msb.JobQuery) ([]msb.JobInfo, error)
	Jobs              []msb.JobInfo
}

// NewMockSandbox returns a Sandbox configured by opts. Zero/unset values produce
// sensible defaults so callers only name the fields they care about.
func NewMockSandbox(opts SandboxOpts) msb.Sandbox {
	return &MockSandbox{
		FSValue_:          opts.FSValue,
		ShellOut:          opts.ShellOut,
		ShellErr:          opts.ShellErr,
		ExecOut:           opts.ExecOut,
		ExecErr:           opts.ExecErr,
		AttachCode:        opts.AttachCode,
		AttachErr:         opts.AttachErr,
		DetachErr:         opts.DetachErr,
		DestroyErr:        opts.DestroyErr,
		CloseErr:          opts.CloseErr,
		StreamHandle_:     opts.StreamHandle_,
		ShellStreamFn:     opts.ShellStreamFn,
		ShellStreamCmds:   opts.ShellStreamCmds,
		ExecDetachedFn:    opts.ExecDetachedFn,
		ExecDetachedCalls: opts.ExecDetachedCalls,
		Job_:              opts.Job_,
		GetJobFn:          opts.GetJobFn,
		ListJobsFn:        opts.ListJobsFn,
		Jobs:              opts.Jobs,
	}
}

// TestFS is a test double for SandboxFS that supports configurable file contents
// and error injection.
type TestFS struct {
	Contents map[string][]byte
	ReadErr  error
	WriteErr error
	MkdirErr error
	Writes   map[string][]byte
	Mkdirs   []string
	Removed  []string
}

// NewTestFS creates a SandboxFS backed by the given files. Files is a map from
// path to file content. Nil map values produce sensible defaults.
func NewTestFS(files map[string][]byte) *TestFS {
	return &TestFS{Contents: files}
}

func (t *TestFS) Exists(_ context.Context, path string) (bool, error) {
	_, ok := t.Contents[path]
	return ok, nil
}

func (t *TestFS) ReadString(_ context.Context, path string) (string, error) {
	if t.ReadErr != nil {
		return "", t.ReadErr
	}
	if d, ok := t.Contents[path]; ok {
		return string(d), nil
	}
	return "", fmt.Errorf("file not found: %s", path)
}

func (t *TestFS) Mkdir(_ context.Context, path string) error {
	if t.MkdirErr != nil {
		return t.MkdirErr
	}
	t.Mkdirs = append(t.Mkdirs, path)
	return nil
}

func (t *TestFS) Write(_ context.Context, path string, data []byte) error {
	if t.WriteErr != nil {
		return t.WriteErr
	}
	if t.Writes == nil {
		t.Writes = make(map[string][]byte)
	}
	t.Writes[path] = data
	return nil
}

func (t *TestFS) Read(_ context.Context, path string) ([]byte, error) {
	if t.ReadErr != nil {
		return nil, t.ReadErr
	}
	if d, ok := t.Contents[path]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}

func (t *TestFS) Remove(_ context.Context, path string) error {
	t.Removed = append(t.Removed, path)
	return nil
}

// TestResult implements ShellResult for tests.
type TestResult struct {
	success     bool
	exitCode    int
	stdout      string
	stderr      string
	stdoutBytes []byte
}

// NewTestResult creates a ShellResult for tests.
func NewTestResult(success bool, exitCode int, stdout, stderr string, stdoutBytes []byte) msb.ShellResult {
	return &TestResult{success: success, exitCode: exitCode, stdout: stdout, stderr: stderr, stdoutBytes: stdoutBytes}
}

func (t *TestResult) Success() bool  { return t.success }
func (t *TestResult) ExitCode() int  { return t.exitCode }
func (t *TestResult) Stdout() string { return t.stdout }
func (t *TestResult) Stderr() string { return t.stderr }
func (t *TestResult) StdoutBytes() []byte {
	if t.stdoutBytes != nil {
		return t.stdoutBytes
	}
	return []byte(t.stdout)
}

type MockVolumeHandle struct {
	Name_          string
	Path_          string
	Kind_          msb.VolumeKind
	CreatedAt_     time.Time
	QuotaMiB_      *uint32
	UsedBytes_     uint64
	CapacityBytes_ *uint64
	DiskFormat_    *string
	DiskFstype_    *string
	Labels_        map[string]string
}

func (m MockVolumeHandle) Name() string { return m.Name_ }
func (m MockVolumeHandle) Path() string { return m.Path_ }
func (m MockVolumeHandle) Kind() msb.VolumeKind {
	if m.Kind_ == "" {
		return msb.VolumeKindDir
	}
	return m.Kind_
}
func (m MockVolumeHandle) CreatedAt() time.Time   { return m.CreatedAt_ }
func (m MockVolumeHandle) QuotaMiB() *uint32      { return m.QuotaMiB_ }
func (m MockVolumeHandle) UsedBytes() uint64      { return m.UsedBytes_ }
func (m MockVolumeHandle) CapacityBytes() *uint64 { return m.CapacityBytes_ }
func (m MockVolumeHandle) DiskFormat() *string    { return m.DiskFormat_ }
func (m MockVolumeHandle) DiskFstype() *string    { return m.DiskFstype_ }
func (m MockVolumeHandle) Labels() map[string]string {
	return m.Labels_
}

type MockImageHandle struct {
	Reference_      string
	ManifestDigest_ string
	LastUsedAt_     time.Time
	SizeBytes_      *int64
	CreatedAt_      time.Time
}

func (m MockImageHandle) Reference() string      { return m.Reference_ }
func (m MockImageHandle) ManifestDigest() string { return m.ManifestDigest_ }
func (m MockImageHandle) LastUsedAt() time.Time  { return m.LastUsedAt_ }
func (m MockImageHandle) SizeBytes() *int64      { return m.SizeBytes_ }
func (m MockImageHandle) CreatedAt() time.Time   { return m.CreatedAt_ }

// WithMsbMock replaces the global Get factory with the provided mock.
// It restores the original factory when the test ends.
func WithMsbMock(t *testing.T, mock msb.Client) {
	t.Helper()
	orig := msb.Get
	msb.Get = func() msb.Client { return mock }
	t.Cleanup(func() { msb.Get = orig })
}

// WithCountingMsbMock is WithMsbMock that also counts how often the Get
// factory is called, so a test can pin that an entry point resolves the client
// once and threads it instead of re-reading the global.
func WithCountingMsbMock(t *testing.T, mock msb.Client) *int {
	t.Helper()
	resolutions := 0
	orig := msb.Get
	msb.Get = func() msb.Client {
		resolutions++
		return mock
	}
	t.Cleanup(func() { msb.Get = orig })
	return &resolutions
}

func InstallFailFastGet() { msb.Get = func() msb.Client { return &failFastMsbClient{} } }

// failFastMsbClient is installed by InstallFailFastGet under tests; any method call
// panics to signal a test reached the real msb client without opting in.
type failFastMsbClient struct{}

// Compile-time check that failFastMsbClient implements Client.
var _ msb.Client = (*failFastMsbClient)(nil)

func (f *failFastMsbClient) mustMock() {
	panic("msb.Get reached the real msb client; install msbtest.WithMsbMock(t, ...) in the test")
}

func (f *failFastMsbClient) EnsureInstalled(_ context.Context) error {
	f.mustMock()
	return nil
}

func (f *failFastMsbClient) GetSandbox(_ context.Context, _ string) (msb.SandboxHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) CreateSandbox(
	_ context.Context,
	_ string,
	_ msb.SandboxSpec,
) (msb.Sandbox, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ListSandboxes(_ context.Context, _ map[string]string) ([]msb.SandboxHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) GetVolume(_ context.Context, _ string) (msb.VolumeHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) CreateVolume(
	_ context.Context,
	_ string,
	_ msb.VolumeSpec,
) (msb.VolumeHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ListVolumes(_ context.Context) ([]msb.VolumeHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) RemoveVolume(_ context.Context, _ string) error {
	f.mustMock()
	return nil
}

func (f *failFastMsbClient) ImageGet(_ context.Context, _ string) error {
	f.mustMock()
	return nil
}

func (f *failFastMsbClient) ImageList(_ context.Context) ([]msb.ImageHandle, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ImagePrune(_ context.Context) (*msb.ImagePruneReport, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) PruneStorage(
	_ context.Context,
	_ msb.PruneRequest,
) (*msb.PruneReport, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ImageRemove(_ context.Context, _ string, _ bool) error {
	f.mustMock()
	return nil
}

func (f *failFastMsbClient) ImageLoad(_ context.Context, _ string, _ io.Reader) error {
	f.mustMock()
	return nil
}

func (f *failFastMsbClient) ImageInspect(_ context.Context, _ string) (*msb.ImageConfig, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ImageInspectDetail(_ context.Context, _ string) (*msb.ImageDetail, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) StorageUsage(_ context.Context) (*msb.StorageReport, error) {
	f.mustMock()
	return nil, nil
}

func (f *failFastMsbClient) ShellStream(_ context.Context, _ string, _ msb.ExecRequest) (msb.StreamHandle, error) {
	f.mustMock()
	return nil, nil
}
