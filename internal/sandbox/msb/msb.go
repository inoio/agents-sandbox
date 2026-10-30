package msb

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// Get is the factory clients can use to get an Client.
// Tests override Get to inject mocks.
var Get = func() Client {
	return &realMsbClient{}
}

var skipRuntimeInstall bool

// IsRealClient reports whether production SDK calls are active.
func IsRealClient() bool {
	_, ok := Get().(*realMsbClient)
	return ok
}

// SkipRuntimeInstall prevents later VM lifecycle calls from invoking the SDK's
// replacement-capable installer after runtime preparation has made a decision.
func SkipRuntimeInstall() { skipRuntimeInstall = true }

// resolveRuntime/ensureRuntime wrap the SDK setup calls so tests can observe the
// RuntimeConfig passed to them. Both name the private home explicitly so
// resolution does not depend on whether ApplyHome already set MSB_HOME.
var (
	resolveRuntime = msbSdk.ResolveRuntime
	ensureRuntime  = msbSdk.EnsureRuntime
)

// runtimeConfig names agents-sandbox's private runtime home explicitly. MSB_HOME
// still governs the FFI cache and all non-setup SDK operations.
func runtimeConfig() msbSdk.RuntimeConfig {
	return msbSdk.RuntimeConfig{Home: configpaths.MSBHome()}
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

// ValidateInstalled checks the SDK-managed runtime without downloading it.
func ValidateInstalled(_ context.Context) error {
	_, err := resolveRuntime(runtimeConfig())
	return err
}

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

// SandboxHandle is the subset of *msb.SandboxHandle used by the launcher.
type SandboxHandle interface {
	Name() string
	ID() string
	Status() msbSdk.SandboxStatus
	UpdatedAt() time.Time
	CreatedAt() time.Time
	BackendKind() msbSdk.BackendKind
	Image() string
	Connect(ctx context.Context) (Sandbox, error)
	Refresh(ctx context.Context) (SandboxHandle, error)
	Start(ctx context.Context) (Sandbox, error)
	Stop(ctx context.Context, opts ...msbSdk.StopOption) error
	Kill(ctx context.Context, opts ...msbSdk.KillOption) error
	Remove(ctx context.Context) error
	Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error
	Config() (*msbSdk.SandboxConfig, error)
	// ConfigMounts returns the mounts the persisted config records, keyed by
	// guest path. Config() cannot supply them: the SDK's
	// SandboxConfig.UnmarshalJSON drops the persisted mounts, so
	// cfg.Volumes is always empty on a read-back
	// (specs/live-mount-readback.md).
	ConfigMounts() (map[string]msbSdk.MountConfig, error)
	Modify(ctx context.Context, opts msbSdk.ModifyOptions) (*msbSdk.SandboxModificationPlan, error)
	Compact(ctx context.Context, opts msbSdk.DiskCompactionOptions) (*msbSdk.DiskCompactionResult, error)
}

// Sandbox is the subset of *msb.Sandbox used by the launcher.
type Sandbox interface {
	FS() SandboxFS
	Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	Exec(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error)
	ShellStream(ctx context.Context, command string, opts ...msbSdk.ExecOption) (StreamHandle, error)
	ExecDetached(ctx context.Context, cmd string, args []string, opts ...msbSdk.JobExecOption) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context, opts ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error)
	AttachWith(ctx context.Context, command string, args []string, opts ...msbSdk.AttachOption) (int, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
	Stop(ctx context.Context, opts ...msbSdk.StopOption) error
	Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error
	Close() error
}

// Job is the subset of *msb.Job used by the launcher for runtime-owned
// detached processes (dockerd, the agent serve daemon). Closing a job detaches
// the handle; it never terminates the process.
type Job interface {
	ID() string
	Inspect(ctx context.Context) (*msbSdk.JobInfo, error)
	Kill(ctx context.Context) error
	Logs(ctx context.Context, options msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error)
	Close() error
}

// ShellResult is the subset of the MSB shell/exec result type used by the launcher.
type ShellResult interface {
	Success() bool
	ExitCode() int
	Stdout() string
	Stderr() string
	StdoutBytes() []byte
}

// StreamEventKind distinguishes the payload of a StreamEvent.
type StreamEventKind int

const (
	StreamEventStdout StreamEventKind = iota
	StreamEventStderr
	StreamEventExited
	StreamEventFailed
)

// StreamEvent is one event from a streaming exec session.
//
//exhaustruct:ignore
type StreamEvent struct {
	Kind     StreamEventKind
	Data     []byte
	ExitCode int
}

// StreamHandle is a live streaming exec session inside a sandbox.
type StreamHandle interface {
	Recv(ctx context.Context) (StreamEvent, error)
	Close() error
}

// VolumeHandle is the subset of *msb.VolumeHandle that the launcher needs.
type VolumeHandle interface {
	Name() string
	Path() string
	Kind() msbSdk.VolumeKind
	CreatedAt() time.Time
	IsDefault() bool
	QuotaMiB() *uint32
	UsedBytes() uint64
	CapacityBytes() *uint64
	DiskFormat() *string
	DiskFstype() *string
	Labels() map[string]string
}

// ImageHandle is the subset of *msb.ImageHandle that the launcher needs.
type ImageHandle interface {
	Reference() string
	ManifestDigest() string
	LastUsedAt() time.Time
	SizeBytes() *int64
	CreatedAt() time.Time
}

// ImageDetail is the inspected detail of a cached image: its manifest identity
// plus the parsed OCI config and layer list. It mirrors the useful fields of
// microsandbox's ImageDetail without exposing the SDK handle.
//
//exhaustruct:ignore
type ImageDetail struct {
	Reference      string
	ManifestDigest string
	Architecture   string
	OS             string
	SizeBytes      *int64
	CreatedAt      time.Time
	Config         msbSdk.ImageConfig
	Layers         []msbSdk.ImageLayer
}

// SandboxFS is the subset of the sandbox filesystem operations used by the launcher.
type SandboxFS interface {
	Exists(ctx context.Context, path string) (bool, error)
	Stat(ctx context.Context, path string) (*msbSdk.FsStat, error)
	List(ctx context.Context, path string) ([]msbSdk.FsEntry, error)
	ReadString(ctx context.Context, path string) (string, error)
	ReadStream(ctx context.Context, path string) (*msbSdk.FsReadStream, error)
	Mkdir(ctx context.Context, path string) error
	Write(ctx context.Context, path string, data []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Remove(ctx context.Context, path string) error
}

// VMStatusKind is the semantic lifecycle class of a sandbox status, as
// consumed by the launcher's VM lifecycle decisions.
type VMStatusKind int

const (
	VMStatusUnknown VMStatusKind = iota
	VMStatusCreated
	VMStatusStarting
	VMStatusActive
	VMStatusStopped
)

// GetVMStatus maps a sandbox status to its lifecycle class. Starting is an
// active runtime transition; running, draining, and paused have an active
// runtime; created has no runtime but can be started; stopped and crashed are
// terminal. Unknown statuses return an error so callers can fail closed.
func GetVMStatus(status msbSdk.SandboxStatus) (VMStatusKind, error) {
	switch status {
	case msbSdk.SandboxStatusCreated:
		return VMStatusCreated, nil
	case msbSdk.SandboxStatusStarting:
		return VMStatusStarting, nil
	case msbSdk.SandboxStatusRunning, msbSdk.SandboxStatusDraining, msbSdk.SandboxStatusPaused:
		return VMStatusActive, nil
	case msbSdk.SandboxStatusStopped, msbSdk.SandboxStatusCrashed:
		return VMStatusStopped, nil
	default:
		return VMStatusUnknown, fmt.Errorf("unexpected sandbox status: %q", status)
	}
}

// IsSandboxActive reports whether a sandbox has a live runtime or is in an
// active runtime transition. Starting is included to protect a VM that another
// caller is booting, although it is not ready for commands yet.
func IsSandboxActive(status msbSdk.SandboxStatus) bool {
	kind, err := GetVMStatus(status)
	return err == nil && (kind == VMStatusStarting || kind == VMStatusActive)
}

// IsSandboxInactive reports whether a sandbox has no live runtime and is safe
// to treat as a stopped resource. Unknown statuses return false so callers
// that remove or reuse resources fail closed.
func IsSandboxInactive(status msbSdk.SandboxStatus) bool {
	kind, err := GetVMStatus(status)
	return err == nil && (kind == VMStatusCreated || kind == VMStatusStopped)
}

// IsNotFound reports whether err is the microsandbox "sandbox does not exist"
// error, unwrapping any wrapped errors.
func IsNotFound(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxNotFound)
}

// IsVolumeNotFound reports whether err is the microsandbox "volume does not
// exist" error, unwrapping any wrapped errors. Absence must be distinguished
// from a transient lookup failure: callers that recover or recreate a missing
// resource treat any other error as a failure rather than as absence.
func IsVolumeNotFound(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrVolumeNotFound)
}

// IsVolumeAlreadyExists reports whether err is the microsandbox "volume with
// this name already exists" error, unwrapping any wrapped errors.
func IsVolumeAlreadyExists(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrVolumeAlreadyExists)
}

// IsReplaced reports whether err indicates that a handle's name now refers to a
// different persisted sandbox identity (the handle was replaced), unwrapping any
// wrapped errors. Operate on live identities and treat a replaced handle as a
// no-op rather than acting on the stranger that now shares the name.
func IsReplaced(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrSandboxReplaced)
}

// IsUnsupportedOperation reports whether err indicates the selected backend or
// native library cannot observe storage (cloud backends and older native
// libraries), unwrapping any wrapped errors. Callers degrade the affected
// categories to unknown rather than reporting zero.
func IsUnsupportedOperation(err error) bool {
	return msbSdk.IsKind(err, msbSdk.ErrUnsupportedOperation)
}

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
	return &realVolumeHandle{val: v}, nil
}

func (realMsbClient) CreateVolume(ctx context.Context, name string, opts ...msbSdk.VolumeOption) (VolumeHandle, error) {
	vol, err := createVolume(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	return &realVolumeHandle{val: vol}, nil
}

func (realMsbClient) ListVolumes(ctx context.Context) ([]VolumeHandle, error) {
	handles, err := listVolumes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]VolumeHandle, len(handles))
	for i, h := range handles {
		result[i] = &realVolumeHandle{val: h}
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

// realVolumeHandle adapts *msbSdk.Volume or *msbSdk.VolumeHandle to VolumeHandle.
type realVolumeHandle struct {
	val any
}

func (v realVolumeHandle) Name() string {
	switch t := v.val.(type) {
	case *msbSdk.VolumeHandle:
		return t.Name()
	case *msbSdk.Volume:
		return t.Name()
	}
	return ""
}

func (v realVolumeHandle) Path() string {
	switch t := v.val.(type) {
	case *msbSdk.VolumeHandle:
		return t.Path()
	case *msbSdk.Volume:
		return t.Path()
	}
	return ""
}

func (v realVolumeHandle) Kind() msbSdk.VolumeKind {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.Kind()
	}
	return msbSdk.VolumeKindDir
}

func (v realVolumeHandle) CreatedAt() time.Time {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.CreatedAt()
	}
	return time.Time{}
}

func (v realVolumeHandle) IsDefault() bool {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.IsDefault()
	}
	return false
}

func (v realVolumeHandle) QuotaMiB() *uint32 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.QuotaMiB()
	}
	return nil
}

func (v realVolumeHandle) UsedBytes() uint64 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.UsedBytes()
	}
	return 0
}

func (v realVolumeHandle) CapacityBytes() *uint64 {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.CapacityBytes()
	}
	return nil
}

func (v realVolumeHandle) DiskFormat() *string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.DiskFormat()
	}
	return nil
}

func (v realVolumeHandle) DiskFstype() *string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.DiskFstype()
	}
	return nil
}

func (v realVolumeHandle) Labels() map[string]string {
	if h, ok := v.val.(*msbSdk.VolumeHandle); ok {
		return h.Labels()
	}
	return nil
}

// realSandboxHandle adapts *msbSdk.SandboxHandle to SandboxHandle.
type realSandboxHandle struct {
	handle *msbSdk.SandboxHandle
}

func (w realSandboxHandle) Name() string {
	return w.handle.Name()
}

func (w realSandboxHandle) ID() string {
	return w.handle.ID()
}

func (w realSandboxHandle) Status() msbSdk.SandboxStatus {
	return w.handle.Status()
}

func (w realSandboxHandle) UpdatedAt() time.Time {
	return w.handle.UpdatedAt()
}

func (w realSandboxHandle) CreatedAt() time.Time {
	return w.handle.CreatedAt()
}

func (w realSandboxHandle) BackendKind() msbSdk.BackendKind {
	return w.handle.BackendKind()
}

func (w realSandboxHandle) Image() string {
	cfg, err := w.handle.Config()
	if err != nil {
		return ""
	}
	return cfg.Image
}

func (w realSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	sb, err := w.handle.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Refresh(ctx context.Context) (SandboxHandle, error) {
	h, err := w.handle.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	return realSandboxHandle{handle: h}, nil
}

func (w realSandboxHandle) Start(ctx context.Context) (Sandbox, error) {
	sb, err := w.handle.Start(ctx)
	if err != nil {
		return nil, err
	}
	return realSandbox{sandbox: sb}, nil
}

func (w realSandboxHandle) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	return w.handle.Stop(ctx, opts...)
}

func (w realSandboxHandle) Kill(ctx context.Context, opts ...msbSdk.KillOption) error {
	return w.handle.Kill(ctx, opts...)
}

func (w realSandboxHandle) Remove(ctx context.Context) error {
	return w.handle.Remove(ctx)
}

func (w realSandboxHandle) Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error {
	return w.handle.Destroy(ctx, opts...)
}

func (w realSandboxHandle) Config() (*msbSdk.SandboxConfig, error) {
	return w.handle.Config()
}

func (w realSandboxHandle) ConfigMounts() (map[string]msbSdk.MountConfig, error) {
	return ParseConfigMounts(w.handle.ConfigJSON())
}

func (w realSandboxHandle) Modify(
	ctx context.Context,
	opts msbSdk.ModifyOptions,
) (*msbSdk.SandboxModificationPlan, error) {
	return w.handle.Modify(ctx, opts)
}

func (w realSandboxHandle) Compact(
	ctx context.Context,
	opts msbSdk.DiskCompactionOptions,
) (*msbSdk.DiskCompactionResult, error) {
	return w.handle.Compact(ctx, opts)
}

// realSandbox adapts *msbSdk.Sandbox to Sandbox.
type realSandbox struct {
	sandbox *msbSdk.Sandbox
}

func (s realSandbox) FS() SandboxFS {
	return s.sandbox.FS()
}

func (s realSandbox) Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error) {
	return s.sandbox.Shell(ctx, command, opts...)
}

func (s realSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	return s.sandbox.Exec(ctx, command, args, opts...)
}

func (s realSandbox) ShellStream(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (StreamHandle, error) {
	handle, err := s.sandbox.ShellStream(ctx, command, opts...)
	if err != nil {
		return nil, err
	}
	return &realStreamHandle{handle: handle}, nil
}

func (s realSandbox) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	opts ...msbSdk.JobExecOption,
) (Job, error) {
	job, err := realSandboxExecDetached(s.sandbox, ctx, cmd, args, opts...)
	if err != nil {
		return nil, err
	}
	return realJob{job: job}, nil
}

func (s realSandbox) GetJob(ctx context.Context, id string) (Job, error) {
	job, err := realSandboxGetJob(s.sandbox, ctx, id)
	if err != nil {
		return nil, err
	}
	return realJob{job: job}, nil
}

// ListJobs returns every job known to the sandbox, following pagination.
func (s realSandbox) ListJobs(ctx context.Context, opts ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error) {
	var result []msbSdk.JobInfo
	var cursor *string
	for {
		pageOpts := opts
		if cursor != nil {
			pageOpts = append(append([]msbSdk.JobListOption{}, opts...), msbSdk.WithJobCursor(*cursor))
		}
		page, err := realSandboxListJobs(s.sandbox, ctx, pageOpts...)
		if err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		cursor = page.NextCursor
		// The SDK drops an empty cursor (its listJobs only sends "cursor" when
		// non-empty), so a pointer to "" means no more pages; treating it as a
		// live cursor would re-request the first page forever.
		if cursor == nil || *cursor == "" {
			return result, nil
		}
	}
}

// realJob adapts *msbSdk.Job to Job. Each delegation target is a package-level
// seam (a method expression) so the thin wrapper is unit-testable without a
// live VM.
type realJob struct {
	job *msbSdk.Job
}

var (
	realSandboxExecDetached = (*msbSdk.Sandbox).ExecDetached
	realSandboxGetJob       = (*msbSdk.Sandbox).GetJob
	realSandboxListJobs     = (*msbSdk.Sandbox).ListJobs
	realJobID               = (*msbSdk.Job).ID
	realJobInspect          = (*msbSdk.Job).Inspect
	realJobKill             = (*msbSdk.Job).Kill
	realJobLogs             = (*msbSdk.Job).Logs
	realJobClose            = (*msbSdk.Job).Close
)

func (j realJob) ID() string { return realJobID(j.job) }

func (j realJob) Inspect(ctx context.Context) (*msbSdk.JobInfo, error) {
	return realJobInspect(j.job, ctx)
}

func (j realJob) Kill(ctx context.Context) error { return realJobKill(j.job, ctx) }

func (j realJob) Logs(ctx context.Context, options msbSdk.JobLogOptions) ([]msbSdk.JobLogEntry, error) {
	return realJobLogs(j.job, ctx, options)
}

func (j realJob) Close() error { return realJobClose(j.job) }

// realStreamHandle adapts *msbSdk.ExecHandle to StreamHandle.
type realStreamHandle struct {
	handle *msbSdk.ExecHandle
}

// Recv maps the SDK's ExecEventKind onto StreamEventKind explicitly. The SDK
// enum has extra kinds (Started, StdinError, Done) so a numeric cast would
// mislabel events. Done is translated to io.EOF so callers can detect a clean
// stream end; Started/StdinError carry no stdout payload and are skipped.
func (h *realStreamHandle) Recv(ctx context.Context) (StreamEvent, error) {
	for {
		ev, err := h.handle.Recv(ctx)
		if err != nil {
			return StreamEvent{}, err
		}
		event, ok, err := mapExecEvent(*ev)
		if err != nil {
			return StreamEvent{}, err
		}
		if !ok {
			continue
		}
		return event, nil
	}
}

// mapExecEvent translates one SDK exec event into a StreamEvent. ok reports
// whether the event carries a payload to surface; events that do not (Started,
// StdinError) are skipped so Recv keeps reading. Done is translated to io.EOF
// so callers can detect a clean end of stream.
func mapExecEvent(ev msbSdk.ExecEvent) (StreamEvent, bool, error) {
	switch ev.Kind {
	case msbSdk.ExecEventStdout:
		return StreamEvent{Kind: StreamEventStdout, Data: ev.Data}, true, nil
	case msbSdk.ExecEventStderr:
		return StreamEvent{Kind: StreamEventStderr, Data: ev.Data}, true, nil
	case msbSdk.ExecEventExited:
		return StreamEvent{Kind: StreamEventExited, ExitCode: ev.ExitCode}, true, nil
	case msbSdk.ExecEventFailed:
		return StreamEvent{Kind: StreamEventFailed}, true, nil
	case msbSdk.ExecEventDone:
		return StreamEvent{}, false, io.EOF
	case msbSdk.ExecEventStarted, msbSdk.ExecEventStdinError:
		// carry no payload; skip and keep reading.
		return StreamEvent{}, false, nil
	default:
		// unknown event kind: skip and keep reading.
		return StreamEvent{}, false, nil
	}
}

func (h *realStreamHandle) Close() error { return h.handle.Close() }

// The realSandbox lifecycle/passthrough methods delegate to the SDK sandbox.
// Each delegation target is a package-level seam (a method expression) so the
// thin wrapper is unit-testable without a live VM.
var (
	realSandboxAttach     = (*msbSdk.Sandbox).Attach
	realSandboxAttachWith = (*msbSdk.Sandbox).AttachWith
	realSandboxDetach     = (*msbSdk.Sandbox).Detach
	realSandboxStop       = (*msbSdk.Sandbox).Stop
	realSandboxDestroy    = (*msbSdk.Sandbox).Destroy
	realSandboxClose      = (*msbSdk.Sandbox).Close
)

func (s realSandbox) Attach(ctx context.Context, command string, args ...string) (int, error) {
	return realSandboxAttach(s.sandbox, ctx, command, args...)
}

func (s realSandbox) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.AttachOption,
) (int, error) {
	return realSandboxAttachWith(s.sandbox, ctx, command, args, opts...)
}

func (s realSandbox) Detach(ctx context.Context) error {
	return realSandboxDetach(s.sandbox, ctx)
}

func (s realSandbox) Stop(ctx context.Context, opts ...msbSdk.StopOption) error {
	return realSandboxStop(s.sandbox, ctx, opts...)
}

func (s realSandbox) Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error {
	return realSandboxDestroy(s.sandbox, ctx, opts...)
}

func (s realSandbox) Close() error {
	return realSandboxClose(s.sandbox)
}
