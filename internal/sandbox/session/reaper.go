package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// Session type constants used by /session/status.
const (
	sessionTypeBusy  = "busy"
	sessionTypeRetry = "retry"
)

const defaultMaxSessionRetries = 10

// SessionStatus is the decoded server-side /session/status entry. Busy and idle are
// plain states; retry carries the server-maintained attempt counter.
//
//nolint:revive // session.SessionStatus avoids stutter with session.Status in a session package
type SessionStatus struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt"`
}

// QuestionRequest is a decoded entry of GET /question. Only sessionID is needed
// to map a pending question back to the session awaiting an answer.
type QuestionRequest struct {
	SessionID string `json:"sessionID"`
}

// reapOnLastClient runs after a client detaches. If this was not the last client it
// is a no-op. If it was, per policy it either returns immediately (auto-stop-on-active
// mode, so the idle timeout stops the VM) or holds the VM until sessions quiesce.
// An agent whose daemon does not expose the v1 session-status endpoints skips the
// quiescence wait, so the idle timeout stops the VM as in auto-stop mode.
func reapOnLastClient(
	ctx context.Context,
	a agent.Agent,
	k state.Key,
	sb msb.Sandbox,
	policy options.ReapPolicy,
	ui termio.UI,
) error {
	if state.CountActiveClients(k) > 0 {
		return nil
	}
	if policy.AutoStopOnActiveSessions {
		ui.Verbosef("auto-stop-on-active-sessions: not waiting; idle timeout will stop VM")
		return nil
	}
	if sb == nil {
		return nil
	}
	provider, ok := agent.AsSessionStatusProvider(a)
	if !ok {
		ui.Verbosef("agent %q has no session-status endpoints; skipping quiescence wait", a.Name())
		return nil
	}
	return waitQuiescent(ctx, k, sb, provider, policy.MaxSessionRetries, ui)
}

// waitQuiescent keeps the VM alive (keeper exec) and polls the provider's
// session-status endpoint until no session is busy and no session is retrying
// past the cutoff, or ctx is done. A client that reattaches during the wait
// aborts it.
func waitQuiescent(
	ctx context.Context,
	k state.Key,
	sb msb.Sandbox,
	provider agent.SessionStatusProvider,
	maxRetry int,
	ui termio.UI,
) error {
	if maxRetry <= 0 {
		maxRetry = defaultMaxSessionRetries
	}

	keeperCtx, cancelKeeper := context.WithCancel(context.Background())
	defer cancelKeeper()

	keeperDone := keepVMAlive(keeperCtx, sb)
	defer func() { _ = keeperDone() }()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// First poll happens immediately, then on the 2s interval.
	first := true
	waiting := ui.Spinner("waiting for active sessions to finish")
	defer waiting.Stop()

	for {
		if first {
			first = false
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}

		if state.CountActiveClients(k) > 0 {
			ui.Verbosef("client reattached during wait; aborting reaper")
			return nil
		}

		states, err := sessionStates(ctx, sb, provider.SessionStatusCmd())
		if err != nil {
			ui.Verbosef("session status poll failed: %v", err)
			continue
		}

		busy, stuckRetry := quiescenceOf(states, nil, maxRetry)
		if busy > 0 {
			pending, pendingErr := pendingQuestionSessionIDs(ctx, sb, provider.QuestionListCmd())
			if pendingErr != nil {
				ui.Verbosef("question status poll failed: %v", pendingErr)
			}
			busy, stuckRetry = quiescenceOf(states, pending, maxRetry)
		}
		ui.Verbosef("waiting: busy=%d stuckRetry=%v", busy, stuckRetry)

		if busy == 0 && !stuckRetry {
			return nil
		}
	}
}

// sessionStates reads the provider's session-status endpoint once via an in-VM
// curl and decodes the sessionID->status map.
func sessionStates(ctx context.Context, sb msb.Sandbox, cmd string) (map[string]SessionStatus, error) {
	res, err := sb.Shell(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if !res.Success() {
		return nil, fmt.Errorf("session status curl failed (exit %d): %s", res.ExitCode(), res.Stderr())
	}
	return decodeSessionStates(res.Stdout())
}

// pendingQuestionSessionIDs reads the provider's question endpoint once via an
// in-VM curl and returns the set of sessionIDs that have at least one pending,
// unanswered question. On any failure it returns an error and a nil set; the
// caller keeps those sessions busy (never risks cutting off real work).
func pendingQuestionSessionIDs(ctx context.Context, sb msb.Sandbox, cmd string) (map[string]bool, error) {
	res, err := sb.Shell(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if !res.Success() {
		return nil, fmt.Errorf("question curl failed (exit %d): %s", res.ExitCode(), res.Stderr())
	}
	reqs, err := decodeQuestionRequests(res.Stdout())
	if err != nil {
		return nil, err
	}
	pending := make(map[string]bool, len(reqs))
	for _, req := range reqs {
		pending[req.SessionID] = true
	}
	return pending, nil
}

// decodeSessionStates decodes the JSON response from /session/status into
// a map of sessionID to SessionStatus. Returns an empty map for empty input.
func decodeSessionStates(data string) (map[string]SessionStatus, error) {
	if data == "" {
		return map[string]SessionStatus{}, nil
	}
	var states map[string]SessionStatus
	if err := json.Unmarshal([]byte(data), &states); err != nil {
		return nil, fmt.Errorf("decode session status: %w", err)
	}
	return states, nil
}

func decodeQuestionRequests(data string) ([]QuestionRequest, error) {
	if data == "" {
		return []QuestionRequest{}, nil
	}
	var reqs []QuestionRequest
	if err := json.Unmarshal([]byte(data), &reqs); err != nil {
		return nil, fmt.Errorf("decode question requests: %w", err)
	}
	return reqs, nil
}

// quiescenceOf reports how many sessions are busy and whether any retry
// session has exceeded the retry cap. Sessions present in pending (awaiting an
// answer to a surfaced question) are not counted as busy.
func quiescenceOf(states map[string]SessionStatus, pending map[string]bool, maxRetry int) (int, bool) {
	busy := 0
	stuckRetry := false
	for id, st := range states {
		switch st.Type {
		case sessionTypeBusy:
			if !pending[id] {
				busy++
			}
		case sessionTypeRetry:
			if st.Attempt >= maxRetry {
				stuckRetry = true
			}
		}
	}
	return busy, stuckRetry
}

// keepVMAlive starts a benign long-running in-VM exec so active_exec_sessions > 0,
// suppressing the msb idle timer while we wait. Returns a func() error that
// cancels/stops the keeper exec.
func keepVMAlive(ctx context.Context, sb msb.Sandbox) func() error {
	done := make(chan struct{})
	keeper, keeperCancel := context.WithCancel(ctx)

	go func() {
		defer close(done)
		_, _ = sb.Exec(keeper, "sleep", []string{"1h"})
	}()

	return func() error {
		keeperCancel()
		<-done
		return nil
	}
}
