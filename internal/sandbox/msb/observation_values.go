package msb

import "time"

// JobInfo is the launcher-owned projection of a detached job's metadata.
// Timestamps are Unix milliseconds. Optional fields stay nil when the runtime
// does not report them, so unknown never reads as zero.
type JobInfo struct {
	ID            string
	RuntimeBootID string
	Command       []string
	State         string
	TTY           bool
	StdinClosed   bool
	PID           *uint32
	CreatedAt     int64
	StartedAt     *int64
	FinishedAt    *int64
	ExitCode      *int
	TimedOut      bool
	Failure       *ExecFailure
	Error         *string
}

// ExecFailure carries a detached job's structured launch failure.
type ExecFailure struct {
	Kind      string
	Errno     *int
	ErrnoName string
	Message   string
	Path      string
}

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

// Metrics is a launcher-owned snapshot of a sandbox's resource usage. Optional
// values stay nil when the runtime cannot measure them.
type Metrics struct {
	CPUPercent              float64
	VCPUTimeNs              uint64
	MemoryBytes             uint64
	MemoryAvailableBytes    *uint64
	MemoryHostResidentBytes *uint64
	MemoryLimitBytes        uint64
	DiskReadBytes           uint64
	DiskWriteBytes          uint64
	NetRxBytes              uint64
	NetTxBytes              uint64
	UpperUsedBytes          *uint64
	UpperFreeBytes          *uint64
	UpperHostAllocatedBytes *uint64
	Uptime                  time.Duration
}
