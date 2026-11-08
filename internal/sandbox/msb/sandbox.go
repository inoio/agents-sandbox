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
	Status() SandboxStatus
	UpdatedAt() time.Time
	CreatedAt() time.Time
	BackendKind() BackendKind
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
	Stop(ctx context.Context, opts LifecycleOptions) error
	Kill(ctx context.Context, opts LifecycleOptions) error
	Remove(ctx context.Context) error
	Destroy(ctx context.Context, opts LifecycleOptions) error
}

// SandboxConfigReader reads a sandbox's persisted configuration.
type SandboxConfigReader interface {
	Config() (*SandboxConfig, error)
	// ConfigMounts returns the mounts the persisted config records, keyed by
	// guest path. Config() cannot supply them: the SDK's
	// SandboxConfig.UnmarshalJSON drops the persisted mounts, so
	// cfg.Volumes is always empty on a read-back
	// (specs/vm/live-mount-readback.md).
	ConfigMounts() (map[string]MountConfig, error)
	// ConfigPorts returns the published port bindings the persisted config
	// records. Config() cannot supply them either: the same SDK decode drops
	// the persisted network.ports, so cfg.PortBindings is always empty on a
	// read-back (specs/vm/live-mount-readback.md).
	ConfigPorts() ([]PortBinding, error)
}

// SandboxResourceControl modifies, compacts and observes a sandbox.
type SandboxResourceControl interface {
	Modify(ctx context.Context, opts ModifyOptions) (*SandboxModificationPlan, error)
	Compact(ctx context.Context, req CompactionRequest) (*DiskCompactionResult, error)
	Metrics(ctx context.Context) (*Metrics, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
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
	Shell(ctx context.Context, command string, req ExecRequest) (ShellResult, error)
	Exec(ctx context.Context, command string, args []string, req ExecRequest) (ShellResult, error)
	FS() SandboxFS
}

// SandboxStreamer runs a shell and its streaming variant in a sandbox.
type SandboxStreamer interface {
	Shell(ctx context.Context, command string, req ExecRequest) (ShellResult, error)
	ShellStream(ctx context.Context, command string, req ExecRequest) (StreamHandle, error)
}

// SandboxAttacher attaches and detaches an interactive session on a sandbox.
type SandboxAttacher interface {
	AttachWith(ctx context.Context, command string, args []string, req AttachRequest) (int, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
}

// SandboxJobs runs and lists detached jobs in a sandbox.
type SandboxJobs interface {
	ExecDetached(ctx context.Context, cmd string, args []string, spec JobSpec) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context, query JobQuery) ([]JobInfo, error)
}

// SandboxObserver reads a sandbox's metrics and logs.
type SandboxObserver interface {
	Metrics(ctx context.Context) (*Metrics, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
}

// SandboxLifecycle destroys and closes a sandbox.
type SandboxLifecycle interface {
	Destroy(ctx context.Context, opts LifecycleOptions) error
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

func (w realSandboxHandle) Status() SandboxStatus {
	return sandboxStatusFrom(realSandboxHandleStatus(w.handle))
}

func (w realSandboxHandle) UpdatedAt() time.Time {
	return w.handle.UpdatedAt()
}

func (w realSandboxHandle) CreatedAt() time.Time {
	return w.handle.CreatedAt()
}

func (w realSandboxHandle) BackendKind() BackendKind {
	return backendKindFrom(realSandboxHandleBackendKind(w.handle))
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
	realSandboxHandleModify     = (*msbSdk.SandboxHandle).Modify
	realSandboxHandleCompact    = (*msbSdk.SandboxHandle).Compact
)

// The handle identity accessors delegate through package-level seams (method
// expressions) so their enum translation is unit-testable without a live VM.
var (
	realSandboxHandleStatus      = (*msbSdk.SandboxHandle).Status
	realSandboxHandleBackendKind = (*msbSdk.SandboxHandle).BackendKind
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

func (w realSandboxHandle) Stop(ctx context.Context, opts LifecycleOptions) error {
	stopOptions, _, _ := lifecycleOptions(opts)
	return w.handle.Stop(ctx, stopOptions...)
}

func (w realSandboxHandle) Kill(ctx context.Context, opts LifecycleOptions) error {
	_, killOptions, _ := lifecycleOptions(opts)
	return w.handle.Kill(ctx, killOptions...)
}

func (w realSandboxHandle) Remove(ctx context.Context) error {
	return w.handle.Remove(ctx)
}

func (w realSandboxHandle) Destroy(ctx context.Context, opts LifecycleOptions) error {
	_, _, destroyOptions := lifecycleOptions(opts)
	return w.handle.Destroy(ctx, destroyOptions...)
}

func (w realSandboxHandle) Config() (*SandboxConfig, error) {
	config, err := realSandboxHandleConfig(w.handle)
	if err != nil {
		return nil, err
	}
	return sandboxConfigFrom(config), nil
}

func (w realSandboxHandle) ConfigMounts() (map[string]MountConfig, error) {
	return ParseConfigMounts(realSandboxHandleConfigJSON(w.handle))
}

func (w realSandboxHandle) ConfigPorts() ([]PortBinding, error) {
	return ParseConfigPorts(realSandboxHandleConfigJSON(w.handle))
}

func (w realSandboxHandle) Modify(
	ctx context.Context,
	opts ModifyOptions,
) (*SandboxModificationPlan, error) {
	plan, err := realSandboxHandleModify(w.handle, ctx, modifyOptions(opts))
	if err != nil {
		return nil, err
	}
	return modificationPlanFrom(plan), nil
}

func (w realSandboxHandle) Compact(
	ctx context.Context,
	req CompactionRequest,
) (*DiskCompactionResult, error) {
	result, err := realSandboxHandleCompact(w.handle, ctx, compactionOptions(req))
	if err != nil {
		return nil, err
	}
	return diskCompactionResultFrom(result), nil
}

func (w realSandboxHandle) Metrics(ctx context.Context) (*Metrics, error) {
	metrics, err := realSandboxHandleMetrics(w.handle, ctx)
	if err != nil {
		return nil, err
	}
	return metricsFrom(metrics), nil
}

func (w realSandboxHandle) Logs(ctx context.Context, query LogQuery) ([]LogEntry, error) {
	entries, err := realSandboxHandleLogs(w.handle, ctx, logOptions(query))
	if err != nil {
		return nil, err
	}
	return logEntriesFrom(entries), nil
}

// realSandbox adapts *msbSdk.Sandbox to Sandbox.
type realSandbox struct {
	sandbox *msbSdk.Sandbox
}

func (s realSandbox) FS() SandboxFS {
	return s.sandbox.FS()
}

func (s realSandbox) Shell(ctx context.Context, command string, req ExecRequest) (ShellResult, error) {
	return realSandboxShell(s.sandbox, ctx, command, execOptions(req)...)
}

func (s realSandbox) Exec(
	ctx context.Context,
	command string,
	args []string,
	req ExecRequest,
) (ShellResult, error) {
	return realSandboxExec(s.sandbox, ctx, command, args, execOptions(req)...)
}

func (s realSandbox) ShellStream(
	ctx context.Context,
	command string,
	req ExecRequest,
) (StreamHandle, error) {
	handle, err := realSandboxShellStream(s.sandbox, ctx, command, execOptions(req)...)
	if err != nil {
		return nil, err
	}
	return &realStreamHandle{handle: handle}, nil
}

func (s realSandbox) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	spec JobSpec,
) (Job, error) {
	job, err := realSandboxExecDetached(s.sandbox, ctx, cmd, args, jobExecOptions(spec)...)
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
func (s realSandbox) ListJobs(ctx context.Context, query JobQuery) ([]JobInfo, error) {
	opts := jobListOptions(query)
	var result []JobInfo
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
		result = append(result, jobInfosFrom(page.Items)...)
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
	req AttachRequest,
) (int, error) {
	return realSandboxAttachWith(s.sandbox, ctx, command, args, attachOptions(req)...)
}

func (s realSandbox) Detach(ctx context.Context) error {
	return realSandboxDetach(s.sandbox, ctx)
}

func (s realSandbox) Destroy(ctx context.Context, opts LifecycleOptions) error {
	_, _, destroyOptions := lifecycleOptions(opts)
	return realSandboxDestroy(s.sandbox, ctx, destroyOptions...)
}

func (s realSandbox) Close() error {
	return realSandboxClose(s.sandbox)
}

func (s realSandbox) Metrics(ctx context.Context) (*Metrics, error) {
	metrics, err := realSandboxMetrics(s.sandbox, ctx)
	if err != nil {
		return nil, err
	}
	return metricsFrom(metrics), nil
}

func (s realSandbox) Logs(ctx context.Context, query LogQuery) ([]LogEntry, error) {
	entries, err := realSandboxLogs(s.sandbox, ctx, logOptions(query))
	if err != nil {
		return nil, err
	}
	return logEntriesFrom(entries), nil
}
