package msb

import (
	"time"
)

// ExecRequest projects msbSdk.ExecConfig (Cwd, Timeout, StdinPipe, TTY, User,
// Env), the configuration of a single exec or shell call. The launcher sets
// only User on most call sites today, but the projection carries the full set
// so the seam never leaks the SDK exec type.
type ExecRequest struct {
	User      string
	Cwd       string
	Env       map[string]string
	TTY       bool
	Timeout   time.Duration
	StdinPipe bool
}

// AttachRequest projects msbSdk.AttachConfig (Cwd, User, Env, DetachKeys), the
// configuration of an interactive attach session. The launcher sets Cwd, User
// and Env; DetachKeys is projected for completeness with the SDK field set.
type AttachRequest struct {
	Cwd        string
	User       string
	Env        map[string]string
	DetachKeys string
}

// JobSpec projects a detached job's exec configuration. The SDK's jobExecConfig
// embeds ExecConfig (plus unexported input and rlimit fields the launcher never
// sets), so a job spec is an exec request; the launcher only sets the user for
// detached jobs today.
type JobSpec struct {
	ExecRequest
}

// JobQuery projects the SDK's unexported jobListConfig (all, limit, cursor),
// one page of a detached job listing. A zero JobQuery requests the default
// page of running jobs.
type JobQuery struct {
	All    bool
	Limit  uint
	Cursor string
}

// LifecycleOptions projects the union of the SDK's unexported lifecycleOptions
// (Stop, Kill: timeout) and destroyOptions (Destroy: force, timeout). The SDK
// additionally tracks whether the timeout was explicitly set; the projection
// treats a non-zero Timeout as set and leaves the default otherwise.
type LifecycleOptions struct {
	Force   bool
	Timeout time.Duration
}

// LogSource identifies where a persisted sandbox log entry came from, mirroring
// msbSdk.LogSource. The launcher only requests the system source today; the
// full set keeps the projection complete.
type LogSource string

const (
	LogSourceStdout LogSource = "stdout"
	LogSourceStderr LogSource = "stderr"
	LogSourceOutput LogSource = "output"
	LogSourceSystem LogSource = "system"
)

// LogQuery projects msbSdk.LogOptions (Tail, Since, Until, Sources), the
// filter for persisted sandbox logs. Zero values read the default stdout and
// stderr sources.
type LogQuery struct {
	Tail    uint64
	Since   time.Time
	Until   time.Time
	Sources []LogSource
}

// CompactionRequest projects msbSdk.DiskCompactionOptions (Layers, DryRun,
// Disk, RootDiskOnly), the selection for an explicit disk-compaction
// maintenance run.
type CompactionRequest struct {
	Layers       *uint32
	DryRun       bool
	Disk         string
	RootDiskOnly bool
}
