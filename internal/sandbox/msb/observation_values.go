package msb

import (
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// JobInfo is the launcher-owned projection of a detached job's metadata.
// Timestamps are Unix milliseconds. Optional fields stay nil when the runtime
// does not report them, so unknown never reads as zero.
type JobInfo struct {
	PID           *uint32
	StartedAt     *int64
	FinishedAt    *int64
	ExitCode      *int
	Failure       *ExecFailure
	Error         *string
	ID            string
	RuntimeBootID string
	State         string
	Command       []string
	CreatedAt     int64
	TTY           bool
	StdinClosed   bool
	TimedOut      bool
}

// ExecFailure is the SDK's structured detached-job launch-failure observation.
type ExecFailure = msbSdk.ExecFailure

// JobLogEntry is one retained output record of a detached job. Data holds the
// original output bytes and Cursor a job-specific replay token.
type JobLogEntry struct {
	Timestamp int64
	Source    string
	Data      []byte
	Cursor    string
}

// JobLogQuery selects a detached job's retained output. Follow continues after
// the snapshot the other fields select.
type JobLogQuery struct {
	Tail       *uint
	Since      *int64
	Until      *int64
	Sources    []string
	FromCursor string
	Follow     bool
}

// LogEntry is one persisted sandbox log entry.
type LogEntry struct {
	Source    LogSource
	SessionID *uint64
	Timestamp time.Time
	Data      []byte
	Cursor    string
}

// Metrics is the SDK's resource-usage observation. Optional values stay nil
// when the runtime cannot measure them; the launcher surfaces it directly.
type Metrics = msbSdk.Metrics
