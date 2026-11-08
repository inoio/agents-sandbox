package msb

import (
	"context"
	"io"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Job is the subset of *msb.Job used by the launcher for runtime-owned
// detached processes (dockerd, the agent serve daemon). Closing a job detaches
// the handle; it never terminates the process.
type Job interface {
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

// realJob adapts *msbSdk.Job to Job. Each delegation target is a package-level
// seam (a method expression) so the thin wrapper is unit-testable without a
// live VM.
type realJob struct {
	job *msbSdk.Job
}

var (
	realJobInspect = (*msbSdk.Job).Inspect
	realJobKill    = (*msbSdk.Job).Kill
	realJobLogs    = (*msbSdk.Job).Logs
	realJobClose   = (*msbSdk.Job).Close
)

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

var (
	realStreamRecv  = (*msbSdk.ExecHandle).Recv
	realStreamClose = (*msbSdk.ExecHandle).Close
)

// Recv maps the SDK's ExecEventKind onto StreamEventKind explicitly. The SDK
// enum has extra kinds (Started, StdinError, Done) so a numeric cast would
// mislabel events. Done is translated to io.EOF so callers can detect a clean
// stream end; Started/StdinError carry no stdout payload and are skipped.
func (h *realStreamHandle) Recv(ctx context.Context) (StreamEvent, error) {
	for {
		ev, err := realStreamRecv(h.handle, ctx)
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

func (h *realStreamHandle) Close() error { return realStreamClose(h.handle) }
