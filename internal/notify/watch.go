package notify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// sessionBusyAware is implemented by backends that want to be told when a
// session returns to work, so in-flight dedup claims can be released.
type sessionBusyAware interface {
	SessionBusy(sessionID string)
}

// reconnectPolicy drives Watch's reconnect timing. fastRetries drops reconnect
// immediately (so we don't miss events on a blip), then backoff grows
// exponentially from backoffStart up to backoffMax with jitter. A connection
// that survives past longLived resets the streak, because the endpoint appears
// healthy again.
type reconnectPolicy struct {
	fastRetries  int
	backoffStart time.Duration
	backoffMax   time.Duration
	longLived    time.Duration
	jitter       func(time.Duration) time.Duration
}

// watchReconnectPolicy is the production policy. Tests replace it to make
// timing fast and jitter deterministic.
//
//nolint:gochecknoglobals // test seam
var watchReconnectPolicy = reconnectPolicy{
	fastRetries:  2,
	backoffStart: time.Second,
	backoffMax:   30 * time.Second,
	longLived:    30 * time.Second,
	jitter: func(d time.Duration) time.Duration {
		if d <= 0 {
			return d
		}
		//nolint:gosec // G404: jitter needn't be cryptographically secure
		return d/2 + time.Duration(rand.Int64N(int64(d)/2))
	},
}

// nextBackoff returns the delay before the next reconnect given the number of
// consecutive failed attempts and the previous backoff. The first
// fastRetries consecutive drops reconnect immediately (0 delay); afterwards
// backoff doubles from backoffStart up to backoffMax, with jitter applied.
func nextBackoff(consecutiveFails int, prev time.Duration) time.Duration {
	p := watchReconnectPolicy
	if consecutiveFails <= p.fastRetries {
		return 0
	}
	if prev == 0 {
		prev = p.backoffStart
	} else if prev < p.backoffMax {
		prev = min(prev*2, p.backoffMax)
	}
	return p.jitter(prev)
}

// summaryFirstDrops bounds how many early drops dropSummary retains.
const summaryFirstDrops = 3

// dropSummary accumulates a bounded report of stream drops so a long session
// with frequent drops doesn't grow an unbounded error. It keeps the first few
// drops, the running count, and the most recent drop.
type dropSummary struct {
	start time.Time
	count int
	first []string
	last  string
}

func (s *dropSummary) record(attempt int, reason string) {
	if s.start.IsZero() {
		s.start = time.Now()
	}
	entry := fmt.Sprintf("attempt %d: %s", attempt, reason)
	s.count++
	if len(s.first) < summaryFirstDrops {
		s.first = append(s.first, entry)
	}
	s.last = entry
}

// err returns the compound error summarizing all drops, or nil if none.
func (s *dropSummary) err() error {
	if s.count == 0 {
		return nil
	}
	parts := append([]string(nil), s.first...)
	if more := s.count - len(s.first); more > 0 {
		parts = append(parts, fmt.Sprintf("... %d more ...", more))
	}
	parts = append(parts, "last: "+s.last)
	over := time.Since(s.start).Round(time.Second)
	return fmt.Errorf("notify event stream dropped %d times over %s: %s",
		s.count, over, strings.Join(parts, "; "))
}

// Watch relays the in-VM SSE stream to the tracker and backend until ctx is
// cancelled. When the stream drops (clean end, exit, or connection failure) it
// reconnects with a fast-then-backoff policy (see watchReconnectPolicy). It
// returns a compound error summarizing the drops, or nil if the stream never
// dropped.
func Watch(ctx context.Context, sb msb.Sandbox, spec agent.EventStreamSpec, sink Backend) error {
	if sink == nil {
		return nil
	}
	tracker := NewTracker(spec)
	summary := &dropSummary{} //nolint:exhaustruct // fields zeroed, populated by record
	var consecutiveFails int
	var backoff time.Duration
loop:
	for attempt := 1; ; attempt++ {
		start := time.Now()
		reason := watchOnce(ctx, sb, tracker, sink, spec.StreamCommand)
		if ctx.Err() != nil {
			break loop
		}
		if time.Since(start) > watchReconnectPolicy.longLived {
			consecutiveFails = 0
			backoff = 0
		}
		consecutiveFails++
		summary.record(attempt, reason.Error())
		backoff = nextBackoff(consecutiveFails, backoff)
		select {
		case <-ctx.Done():
			break loop
		case <-time.After(backoff):
		}
	}
	return summary.err()
}

// watchOnce opens one SSE stream and feeds the tracker until it drops or ctx
// is cancelled. It returns nil if ctx was cancelled (not a drop), otherwise a
// non-nil error describing why the stream ended. SSE events are blocks of
// `event:`/`data:` lines separated by blank lines, so lines are accumulated
// and parsed one block at a time.
func watchOnce(
	ctx context.Context,
	sb msb.Sandbox,
	tracker *Tracker,
	sink Backend,
	streamCommand string,
) error {
	handle, err := sb.ShellStream(ctx, streamCommand)
	if err != nil {
		return fmt.Errorf("opening stream: %w", err)
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
			block = nil
			continue
		}
		block = append(block, line)
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	return errors.New("stream ended cleanly")
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
