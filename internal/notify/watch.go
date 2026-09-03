package notify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
)

// maxStreamAttempts caps how many times Watch reconnects before giving up.
const maxStreamAttempts = 10

// sessionBusyAware is implemented by backends that want to be told when a
// session returns to work, so in-flight dedup claims can be released.
type sessionBusyAware interface {
	SessionBusy(sessionID string)
}

// watchBackoff is the delay before reconnecting a dropped stream. It is a test
// seam; production uses the default.
//
//nolint:gochecknoglobals // test seam
var watchBackoff = time.Second

// Watch relays the in-VM SSE stream to the tracker and backend. When the
// stream drops (exits, connection reset) it reconnects with a bounded number
// of attempts and a growing backoff, until ctx is cancelled or the cap is hit.
func Watch(ctx context.Context, sb msb.Sandbox, spec agent.EventStreamSpec, sink Backend) error {
	if sink == nil {
		return nil
	}
	tracker := NewTracker(spec)
	var err error
loop:
	for attempt := 1; ; attempt++ {
		dropped := watchOnce(ctx, sb, tracker, sink, spec.StreamCommand)
		switch {
		case ctx.Err() != nil:
			break loop
		case dropped && attempt >= maxStreamAttempts:
			err = fmt.Errorf("notify stream dropped %d times; giving up", maxStreamAttempts)
			break loop
		}
		select {
		case <-ctx.Done():
			break loop
		case <-time.After(watchBackoff):
		}
	}
	return err
}

// watchOnce opens one SSE stream and feeds the tracker until it drops or ctx
// is cancelled. It reports whether the stream dropped (vs ctx ending). SSE
// events are blocks of `event:`/`data:` lines separated by blank lines, so
// lines are accumulated and parsed one block at a time.
func watchOnce(
	ctx context.Context,
	sb msb.Sandbox,
	tracker *Tracker,
	sink Backend,
	streamCommand string,
) bool {
	handle, err := sb.ShellStream(ctx, streamCommand)
	if err != nil {
		return true
	}
	defer handle.Close()

	scanner := bufio.NewScanner(&streamReader{ctx: ctx, handle: handle, chunk: make([]byte, 0), pos: 0})
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var block []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if e, ok := parseSSEBlock(block); ok {
				handleEvent(tracker, sink, e)
			}
			/*			if err := appendNotifyLog(block); err != nil {
						ui.Verbosef("notify watcher log: %v", err)
					}*/
			block = nil
			continue
		}
		block = append(block, line)
	}
	return ctx.Err() == nil
}

// handleEvent feeds one parsed event to the sink. When the session returns to
// work, a sessionBusyAware sink releases its in-flight dedup claims first, so a
// later transition for the session can notify again; then the tracker decides
// whether the event fires a notification.
func handleEvent(tracker *Tracker, sink Backend, e Event) {
	if e.SessionID != "" && eventTypeIn(e.Type, tracker.spec.BusyEvents) {
		if aware, ok := sink.(sessionBusyAware); ok {
			aware.SessionBusy(e.SessionID)
		}
	}
	if n := tracker.Handle(e); n != nil {
		sink.Notify(*n)
	}
}

// appendNotifyLog appends one SSE block to notify.log in the current working
// directory for debugging.
/*func appendNotifyLog(block []string) error {
	f, err := os.OpenFile("notify.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, l := range block {
		if _, err := fmt.Fprintln(f, l); err != nil {
			return err
		}
	}
	return nil
}*/

// streamReader adapts a msb.StreamHandle to io.Reader by copying each
// stdout chunk into the read buffer. A clean stream end is surfaced as io.EOF
// (Recv returns io.EOF on ExecEventDone); a stream exit/failure surfaces as an
// error so the scanner stops and Watch can reconnect. Stderr is ignored.
type streamReader struct {
	ctx    context.Context
	handle msb.StreamHandle
	chunk  []byte
	pos    int
}

func (r *streamReader) Read(p []byte) (int, error) {
	for r.pos >= len(r.chunk) {
		ev, err := r.handle.Recv(r.ctx)
		if err != nil {
			return 0, err
		}
		switch ev.Kind {
		case msb.StreamEventStdout:
			r.chunk = ev.Data
			r.pos = 0
		case msb.StreamEventExited:
			return 0, fmt.Errorf("stream exited with code %d", ev.ExitCode)
		case msb.StreamEventFailed:
			return 0, errors.New("stream failed")
		case msb.StreamEventStderr:
			// Ignore stderr; keep reading stdout.
		}
	}
	n := copy(p, r.chunk[r.pos:])
	r.pos += n
	return n, nil
}

// parseSSEBlock parses one SSE block (the lines between blank lines) into an
// Event. opencode's /global/event stream emits a single "data:" line whose
// JSON carries the event type under "payload.type".
func parseSSEBlock(lines []string) (Event, bool) {
	var payload []byte
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(l, "data:"); ok {
			payload = bytes.TrimSpace([]byte(rest))
		}
	}
	if len(payload) == 0 {
		return Event{}, false
	}
	var raw struct {
		Payload struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
			} `json:"properties"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, false
	}
	if raw.Payload.Type == "" {
		return Event{}, false
	}
	return Event{Type: raw.Payload.Type, SessionID: raw.Payload.Properties.SessionID, Data: payload}, true
}
