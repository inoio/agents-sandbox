package msb

import (
	"context"
	"encoding/json"
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
	// guest path. Config() cannot supply them: the launcher's read-back model
	// does not carry them, so it reads them from the persisted spec
	// (specs/vm/live-mount-readback.md).
	ConfigMounts() (map[string]MountConfig, error)
	// ConfigPorts returns the published port bindings the persisted config
	// records. Config() cannot supply them either: the launcher's read-back
	// model does not carry them, so it reads them from the persisted spec
	// (specs/vm/live-mount-readback.md).
	ConfigPorts() ([]PortBinding, error)
}

// SandboxResourceControl modifies, compacts and observes a sandbox.
type SandboxResourceControl interface {
	Modify(ctx context.Context, opts ModifyOptions) (*SandboxModificationPlan, error)
	Compact(ctx context.Context, req CompactionRequest) (*DiskCompactionResult, error)
	Metrics(ctx context.Context) (*Metrics, error)
	// MetricsStream is the same subscription the observer exposes; it lives on
	// both roles so a persisted SandboxHandle, whose method set comes from here,
	// still satisfies SandboxObserver.
	MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
}

// SandboxHandle is the subset of *msb.Sandbox used by the launcher.
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

// SandboxStreamer runs a shell and its streaming variants in a sandbox.
type SandboxStreamer interface {
	Shell(ctx context.Context, command string, req ExecRequest) (ShellResult, error)
	ShellStream(ctx context.Context, command string, req ExecRequest) (StreamHandle, error)
	ExecStream(ctx context.Context, command string, args []string, req ExecRequest) (StreamHandle, error)
}

// SandboxAttacher attaches and detaches an interactive session on a sandbox.
type SandboxAttacher interface {
	AttachWith(ctx context.Context, command string, args []string, req AttachRequest) (int, error)
	Attach(ctx context.Context, command string, args ...string) (int, error)
	Detach(ctx context.Context) error
}

// AttachEndedAbnormally reports whether an interactive attach ended abnormally:
// a signal death (the guest reports exit code -1 for any signal) or a non-nil
// attach error. A clean exit, or a deliberate non-zero exit code, is normal.
// Callers use it to decide whether to clear the terminal's guest-TUI modes and
// whether the end warrants a resource-raise offer.
func AttachEndedAbnormally(attachErr error, exitCode int) bool {
	return attachErr != nil || exitCode == -1
}

// SandboxJobs runs and lists detached jobs in a sandbox.
type SandboxJobs interface {
	ExecDetached(ctx context.Context, cmd string, args []string, spec JobSpec) (Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	ListJobs(ctx context.Context, query JobQuery) ([]JobInfo, error)
}

// MetricsStream is a live metrics subscription. Recv blocks until the next
// sample arrives or the stream ends; Close releases the runtime-side resources.
// Recv returning (nil, nil) means the stream ended.
type MetricsStream interface {
	Recv(ctx context.Context) (*Metrics, error)
	Close() error
}

// SandboxObserver reads a sandbox's metrics and logs.
type SandboxObserver interface {
	Metrics(ctx context.Context) (*Metrics, error)
	// MetricsStream subscribes to samples delivered every interval. The caller
	// owns the returned handle and must Close it.
	MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error)
	Logs(ctx context.Context, query LogQuery) ([]LogEntry, error)
}

// SandboxLifecycle destroys and closes a sandbox.
type SandboxLifecycle interface {
	Destroy(ctx context.Context, opts LifecycleOptions) error
	Close() error
}

// SandboxActivity refreshes a sandbox's idle activity timer. It is the keeper's
// narrow dependency: a resident holder touches the VM so the runtime's idle
// monitor does not stop it while the holder waits.
type SandboxActivity interface {
	Touch(ctx context.Context) error
}

// Sandbox is the subset of *msb.Sandbox used by the launcher.
type Sandbox interface {
	SandboxExecutor
	SandboxStreamer
	SandboxAttacher
	SandboxJobs
	SandboxObserver
	SandboxLifecycle
	SandboxActivity
}

// realSandboxHandle adapts *msbSdk.Sandbox to SandboxHandle. The v0.8 SDK
// unified the persisted handle and the live sandbox into one resource and moved
// the mutable observations behind context-bearing lookups, so the adapter
// captures the identity (Info) and persisted config observations when the
// handle is resolved and keeps the old synchronous identity/config methods.
// Lifecycle and resource operations act on the retained resource.
//
// Each delegation target is a package-level seam (a method expression or a
// function) so the thin adapter is unit-testable without a live VM.
type realSandboxHandle struct {
	handle     *msbSdk.Sandbox
	info       *msbSdk.SandboxInfo
	config     *msbSdk.SandboxConfigInspection
	configJSON string
	configErr  error
}

// sandboxHandleFrom captures the observations the synchronous handle exposes and
// wraps the resource. The identity observation is required; a failed config
// lookup or decode is retained on the handle and reported by the config
// accessors, so a run can degrade to "config unreadable" rather than fail the
// whole lookup (specs/vm/reconfig-decision.md).
func sandboxHandleFrom(ctx context.Context, sandbox *msbSdk.Sandbox) (*realSandboxHandle, error) {
	info, err := realSandboxHandleInfo(sandbox, ctx)
	if err != nil {
		return nil, err
	}
	raw, configErr := realSandboxHandleConfigJSON(sandbox, ctx)
	var config *msbSdk.SandboxConfigInspection
	if configErr == nil {
		config, configErr = sandboxConfigInspection(raw)
	}
	return &realSandboxHandle{
		handle:     sandbox,
		info:       info,
		config:     config,
		configJSON: raw,
		configErr:  configErr,
	}, nil
}

// sandboxConfigInspection decodes the persisted-config inspection envelope.
func sandboxConfigInspection(raw string) (*msbSdk.SandboxConfigInspection, error) {
	var inspection msbSdk.SandboxConfigInspection
	if err := json.Unmarshal([]byte(raw), &inspection); err != nil {
		return nil, fmt.Errorf("decoding sandbox config inspection: %w", err)
	}
	return &inspection, nil
}

func (w realSandboxHandle) Name() string {
	return realSandboxName(w.handle)
}

func (w realSandboxHandle) ID() string {
	return realSandboxID(w.handle)
}

func (w realSandboxHandle) Status() SandboxStatus {
	if w.info == nil {
		return SandboxStatus("")
	}
	return sandboxStatusFrom(w.info.Status)
}

func (w realSandboxHandle) UpdatedAt() time.Time {
	if w.info == nil || w.info.UpdatedAt == nil {
		return time.Time{}
	}
	return *w.info.UpdatedAt
}

func (w realSandboxHandle) CreatedAt() time.Time {
	if w.info == nil || w.info.CreatedAt == nil {
		return time.Time{}
	}
	return *w.info.CreatedAt
}

func (w realSandboxHandle) BackendKind() BackendKind {
	return backendKindFrom(realSandboxBackendKind(w.handle))
}

func (w realSandboxHandle) Image() string {
	if w.configErr != nil || w.config == nil {
		return ""
	}
	return imageFromInspection(w.config.Spec)
}

// Connect attaches to this sandbox only when it is running; a stopped or
// starting sandbox is not connected. It never starts the sandbox, so a caller
// that reads live VM state (the reconfiguration decision) does not boot a VM as
// a side effect.
func (w realSandboxHandle) Connect(ctx context.Context) (Sandbox, error) {
	status, err := realSandboxStatus(w.handle, ctx)
	if err != nil {
		return nil, err
	}
	if status != msbSdk.SandboxStatusRunning {
		return nil, &msbSdk.Error{
			Kind:    msbSdk.ErrSandboxNotRunning,
			Message: fmt.Sprintf("sandbox %q is %s, not running", realSandboxName(w.handle), status),
		}
	}
	return &realSandbox{sandbox: w.handle}, nil
}

// ConnectOrStart starts attached (not detached), so the returned sandbox owns
// the VM process exactly when this call booted it; ownership is read before the
// caller can detach. A sandbox whose ownership cannot be read is closed and the
// error returned, because guessing would run or skip the once-per-boot work.
func (w realSandboxHandle) ConnectOrStart(ctx context.Context) (Sandbox, bool, error) {
	if err := realHandleEnsureRunning(w.handle, ctx); err != nil {
		return nil, false, err
	}
	booted, err := realSandboxOwnsLifecycle(w.handle)
	if err != nil {
		_ = realSandboxClose(w.handle)
		return nil, false, fmt.Errorf("read whether this run booted the sandbox: %w", err)
	}
	return realSandbox{sandbox: w.handle}, booted, nil
}

func (w realSandboxHandle) Stop(ctx context.Context, opts LifecycleOptions) error {
	stopOptions, _, _ := lifecycleOptions(opts)
	return realSandboxHandleStop(w.handle, ctx, stopOptions...)
}

func (w realSandboxHandle) Kill(ctx context.Context, opts LifecycleOptions) error {
	_, killOptions, _ := lifecycleOptions(opts)
	return realSandboxHandleKill(w.handle, ctx, killOptions...)
}

// Remove deletes a stopped sandbox's persisted state through the retained
// resource's identity-bearing Destroy, never by bare name: the v0.8 SDK's
// name-based msbSdk.RemoveSandbox cannot report a same-name replacement, so
// acting on it would delete a stranger's sandbox
// (specs/identity/sandbox-identity.md).
func (w realSandboxHandle) Remove(ctx context.Context) error {
	return w.Destroy(ctx, LifecycleOptions{})
}

func (w realSandboxHandle) Destroy(ctx context.Context, opts LifecycleOptions) error {
	_, _, destroyOptions := lifecycleOptions(opts)
	return realSandboxHandleDestroy(w.handle, ctx, destroyOptions...)
}

func (w realSandboxHandle) Config() (*SandboxConfig, error) {
	if w.configErr != nil {
		return nil, w.configErr
	}
	return sandboxConfigFrom(w.config), nil
}

func (w realSandboxHandle) ConfigMounts() (map[string]MountConfig, error) {
	if w.configErr != nil {
		return nil, w.configErr
	}
	return ParseConfigMounts(w.configJSON)
}

func (w realSandboxHandle) ConfigPorts() ([]PortBinding, error) {
	if w.configErr != nil {
		return nil, w.configErr
	}
	return ParseConfigPorts(w.configJSON)
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
	metrics, err := realSandboxMetrics(w.handle, ctx)
	if err != nil {
		return nil, err
	}
	return metrics, nil
}

func (w realSandboxHandle) MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error) {
	return newMetricsStream(w.handle, ctx, interval)
}

func (w realSandboxHandle) Logs(ctx context.Context, query LogQuery) ([]LogEntry, error) {
	entries, err := realSandboxLogs(w.handle, ctx, logOptions(query))
	if err != nil {
		return nil, err
	}
	return logEntriesFrom(entries), nil
}

// The handle delegation targets are package-level seams (method expressions or
// functions) so their translation is unit-testable without a live VM.
var (
	realSandboxName             = (*msbSdk.Sandbox).Name
	realSandboxID               = (*msbSdk.Sandbox).ID
	realSandboxBackendKind      = (*msbSdk.Sandbox).ContextKind
	realSandboxHandleInfo       = (*msbSdk.Sandbox).Info
	realSandboxHandleConfigJSON = (*msbSdk.Sandbox).ConfigJSON
	realSandboxStatus           = (*msbSdk.Sandbox).Status
	realSandboxHandleStop       = (*msbSdk.Sandbox).Stop
	realSandboxHandleKill       = (*msbSdk.Sandbox).Kill
	realSandboxHandleDestroy    = (*msbSdk.Sandbox).Destroy
	realSandboxHandleModify     = (*msbSdk.Sandbox).Modify
	realSandboxHandleCompact    = (*msbSdk.Sandbox).Compact
)

// The connect-or-start delegation targets are package-level seams (method
// expressions) so the boot report is unit-testable without a live VM.
var (
	realHandleEnsureRunning  = (*msbSdk.Sandbox).EnsureRunning
	realSandboxOwnsLifecycle = (*msbSdk.Sandbox).OwnsLifecycle
)

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

func (s realSandbox) ExecStream(
	ctx context.Context,
	command string,
	args []string,
	req ExecRequest,
) (StreamHandle, error) {
	handle, err := realSandboxExecStream(s.sandbox, ctx, command, args, execOptions(req)...)
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
	realSandboxExecStream   = (*msbSdk.Sandbox).ExecStream
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
	return metrics, nil
}

func (s realSandbox) MetricsStream(ctx context.Context, interval time.Duration) (MetricsStream, error) {
	return newMetricsStream(s.sandbox, ctx, interval)
}

// realMetricsStream wraps the SDK handle behind the launcher MetricsStream, so
// no SDK type crosses the seam. The SDK handle has no exported constructor, so
// Recv and Close route through method-expression seams a unit test swaps,
// exercising the wrapper without the native library (as realStreamHandle does).
type realMetricsStream struct {
	handle *msbSdk.MetricsStreamHandle
}

var (
	realMetricsStreamRecv  = (*msbSdk.MetricsStreamHandle).Recv
	realMetricsStreamClose = (*msbSdk.MetricsStreamHandle).Close
)

func (s realMetricsStream) Recv(ctx context.Context) (*Metrics, error) {
	return realMetricsStreamRecv(s.handle, ctx)
}

func (s realMetricsStream) Close() error {
	return realMetricsStreamClose(s.handle)
}

// sdkMetricsStream is the SDK call as a method-expression seam, so a unit test
// runs newMetricsStream's wrapper body without the native library.
var sdkMetricsStream = (*msbSdk.Sandbox).MetricsStream

// newMetricsStream is the SDK call as a seam returning the launcher handle, so
// a unit test injects a fake without the native library.
var newMetricsStream = func(
	s *msbSdk.Sandbox, ctx context.Context, interval time.Duration,
) (MetricsStream, error) {
	handle, err := sdkMetricsStream(s, ctx, interval)
	if err != nil {
		return nil, err
	}
	return realMetricsStream{handle: handle}, nil
}

func (s realSandbox) Logs(ctx context.Context, query LogQuery) ([]LogEntry, error) {
	entries, err := realSandboxLogs(s.sandbox, ctx, logOptions(query))
	if err != nil {
		return nil, err
	}
	return logEntriesFrom(entries), nil
}

// realSandboxTouch is the SDK Touch as a seam: the adapter discards the
// SDK's touch result and surfaces only the error.
var realSandboxTouch = func(sandbox *msbSdk.Sandbox, ctx context.Context) error {
	_, err := sandbox.Touch(ctx)
	return err
}

func (s realSandbox) Touch(ctx context.Context) error {
	return realSandboxTouch(s.sandbox, ctx)
}
