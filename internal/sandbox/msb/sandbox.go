package msb

import (
	"context"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// SandboxIdentity exposes a sandbox's stable identity attributes.
type SandboxIdentity interface {
	Name() string
	ID() string
	Status() msbSdk.SandboxStatus
	UpdatedAt() time.Time
	CreatedAt() time.Time
	BackendKind() msbSdk.BackendKind
	Image() string
}

// SandboxLifecycleHandle connects to, stops, kills, removes and destroys a
// sandbox by its stable identity.
type SandboxLifecycleHandle interface {
	Connect(ctx context.Context) (Sandbox, error)
	// ConnectOrStart connects to this exact sandbox when it runs, waits while
	// another caller starts it, or starts it when it is created, stopped or
	// crashed. booted reports whether this call started it: of concurrent
	// callers on one stopped sandbox exactly one boots it and the others
	// connect. A same-name replacement is rejected (IsReplaced).
	ConnectOrStart(ctx context.Context) (sb Sandbox, booted bool, err error)
	Stop(ctx context.Context, opts ...msbSdk.StopOption) error
	Kill(ctx context.Context, opts ...msbSdk.KillOption) error
	Remove(ctx context.Context) error
	Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error
}

// SandboxConfigReader reads a sandbox's persisted configuration.
type SandboxConfigReader interface {
	Config() (*msbSdk.SandboxConfig, error)
	// ConfigMounts returns the mounts the persisted config records, keyed by
	// guest path. Config() cannot supply them: the SDK's
	// SandboxConfig.UnmarshalJSON drops the persisted mounts, so
	// cfg.Volumes is always empty on a read-back
	// (specs/vm/live-mount-readback.md).
	ConfigMounts() (map[string]msbSdk.MountConfig, error)
	// ConfigPorts returns the published port bindings the persisted config
	// records. Config() cannot supply them either: the same SDK decode drops
	// the persisted network.ports, so cfg.PortBindings is always empty on a
	// read-back (specs/vm/live-mount-readback.md).
	ConfigPorts() ([]msbSdk.PortBinding, error)
}

// SandboxResourceControl modifies, compacts and observes a sandbox.
type SandboxResourceControl interface {
	Modify(ctx context.Context, opts msbSdk.ModifyOptions) (*msbSdk.SandboxModificationPlan, error)
	Compact(ctx context.Context, opts msbSdk.DiskCompactionOptions) (*msbSdk.DiskCompactionResult, error)
	Metrics(ctx context.Context) (*msbSdk.Metrics, error)
	Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error)
}

// SandboxHandle is the subset of *msb.SandboxHandle used by the launcher.
type SandboxHandle interface {
	SandboxIdentity
	SandboxLifecycleHandle
	SandboxConfigReader
	SandboxResourceControl
}

// SandboxExecutor runs commands in a sandbox and exposes its filesystem.
type SandboxExecutor interface {
	Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	Exec(ctx context.Context, command string, args []string, opts ...msbSdk.ExecOption) (ShellResult, error)
	FS() SandboxFS
}

// SandboxStreamer runs a shell and its streaming variant in a sandbox.
type SandboxStreamer interface {
	Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error)
	ShellStream(ctx context.Context, command string, opts ...msbSdk.ExecOption) (StreamHandle, error)
}

// SandboxAttacher attaches and detaches an interactive session on a sandbox.
type SandboxAttacher interface {
	AttachWith(ctx context.Context, command string, args []string, opts ...msbSdk.AttachOption) (int, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
}

// SandboxJobs runs and lists detached jobs in a sandbox.
type SandboxJobs interface {
	ExecDetached(ctx context.Context, cmd string, args []string, opts ...msbSdk.JobExecOption) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context, opts ...msbSdk.JobListOption) ([]msbSdk.JobInfo, error)
}

// SandboxObserver reads a sandbox's metrics and logs.
type SandboxObserver interface {
	Metrics(ctx context.Context) (*msbSdk.Metrics, error)
	Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error)
}

// SandboxLifecycle destroys and closes a sandbox.
type SandboxLifecycle interface {
	Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error
	Close() error
}

// Sandbox is the subset of *msb.Sandbox used by the launcher.
type Sandbox interface {
	SandboxExecutor
	SandboxStreamer
	SandboxAttacher
	SandboxJobs
	SandboxObserver
	SandboxLifecycle
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
	cfg, err := realSandboxHandleConfig(w.handle)
	if err != nil {
		return ""
	}
	return cfg.Image
}

func (w realSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	sb, err := realSandboxHandleConnect(w.handle, ctx)
	if err != nil {
		return nil, err
	}
	return &realSandbox{sandbox: sb}, nil
}

// The handle config/connect delegation targets are package-level seams (method
// expressions) so the thin adapters are unit-testable without a live VM.
var (
	realSandboxHandleConfig     = (*msbSdk.SandboxHandle).Config
	realSandboxHandleConfigJSON = (*msbSdk.SandboxHandle).ConfigJSON
	realSandboxHandleConnect    = (*msbSdk.SandboxHandle).Connect
)

// The connect-or-start delegation targets are package-level seams (method
// expressions) so the boot report is unit-testable without a live VM.
var (
	realHandleConnectOrStart = (*msbSdk.SandboxHandle).ConnectOrStart
	realSandboxOwnsLifecycle = (*msbSdk.Sandbox).OwnsLifecycle
)

// ConnectOrStart starts attached (not detached), so the returned sandbox owns
// the VM process exactly when this call booted it; ownership is read before the
// caller can detach. A sandbox whose ownership cannot be read is closed and the
// error returned, because guessing would run or skip the once-per-boot work.
func (w realSandboxHandle) ConnectOrStart(ctx context.Context) (Sandbox, bool, error) {
	sb, err := realHandleConnectOrStart(w.handle, ctx)
	if err != nil {
		return nil, false, err
	}
	booted, err := realSandboxOwnsLifecycle(sb)
	if err != nil {
		_ = realSandboxClose(sb)
		return nil, false, fmt.Errorf("read whether this run booted the sandbox: %w", err)
	}
	return realSandbox{sandbox: sb}, booted, nil
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
	return realSandboxHandleConfig(w.handle)
}

func (w realSandboxHandle) ConfigMounts() (map[string]msbSdk.MountConfig, error) {
	return ParseConfigMounts(realSandboxHandleConfigJSON(w.handle))
}

func (w realSandboxHandle) ConfigPorts() ([]msbSdk.PortBinding, error) {
	return ParseConfigPorts(realSandboxHandleConfigJSON(w.handle))
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

func (w realSandboxHandle) Metrics(ctx context.Context) (*msbSdk.Metrics, error) {
	return realSandboxHandleMetrics(w.handle, ctx)
}

func (w realSandboxHandle) Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error) {
	return realSandboxHandleLogs(w.handle, ctx, opts)
}

// realSandbox adapts *msbSdk.Sandbox to Sandbox.
type realSandbox struct {
	sandbox *msbSdk.Sandbox
}

func (s realSandbox) FS() SandboxFS {
	return s.sandbox.FS()
}

func (s realSandbox) Shell(ctx context.Context, command string, opts ...msbSdk.ExecOption) (ShellResult, error) {
	return realSandboxShell(s.sandbox, ctx, command, opts...)
}

func (s realSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	opts ...msbSdk.ExecOption,
) (ShellResult, error) {
	return realSandboxExec(s.sandbox, ctx, command, args, opts...)
}

func (s realSandbox) ShellStream(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (StreamHandle, error) {
	handle, err := realSandboxShellStream(s.sandbox, ctx, command, opts...)
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

// The realSandbox lifecycle/passthrough methods delegate to the SDK sandbox.
// Each delegation target is a package-level seam (a method expression) so the
// thin wrapper is unit-testable without a live VM.
var (
	realSandboxShell        = (*msbSdk.Sandbox).Shell
	realSandboxExec         = (*msbSdk.Sandbox).Exec
	realSandboxShellStream  = (*msbSdk.Sandbox).ShellStream
	realSandboxExecDetached = (*msbSdk.Sandbox).ExecDetached
	realSandboxGetJob       = (*msbSdk.Sandbox).GetJob
	realSandboxListJobs     = (*msbSdk.Sandbox).ListJobs
	realSandboxAttach       = (*msbSdk.Sandbox).Attach
	realSandboxAttachWith   = (*msbSdk.Sandbox).AttachWith
	realSandboxDetach       = (*msbSdk.Sandbox).Detach
	realSandboxDestroy      = (*msbSdk.Sandbox).Destroy
	realSandboxClose        = (*msbSdk.Sandbox).Close
	realSandboxMetrics      = (*msbSdk.Sandbox).Metrics
	realSandboxLogs         = (*msbSdk.Sandbox).Logs

	realSandboxHandleMetrics = (*msbSdk.SandboxHandle).Metrics
	realSandboxHandleLogs    = (*msbSdk.SandboxHandle).Logs
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

func (s realSandbox) Destroy(ctx context.Context, opts ...msbSdk.DestroyOption) error {
	return realSandboxDestroy(s.sandbox, ctx, opts...)
}

func (s realSandbox) Close() error {
	return realSandboxClose(s.sandbox)
}

func (s realSandbox) Metrics(ctx context.Context) (*msbSdk.Metrics, error) {
	return realSandboxMetrics(s.sandbox, ctx)
}

func (s realSandbox) Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error) {
	return realSandboxLogs(s.sandbox, ctx, opts)
}
