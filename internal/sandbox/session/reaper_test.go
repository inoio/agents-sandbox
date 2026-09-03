package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// opencodeAgent returns the registered opencode profile, which implements
// SessionStatusProvider (the v1 session-status endpoints the quiescence wait
// polls).
func opencodeAgent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// noSessionStatusAgent implements agent.Agent without SessionStatusProvider,
// modeling daemons whose servers expose no v1 session-status endpoints.
type noSessionStatusAgent struct{}

func (noSessionStatusAgent) Name() string               { return "no-session-status" }
func (noSessionStatusAgent) ConfigDirName() string      { return "nosessionstatus" }
func (noSessionStatusAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// --- ReapOnLastClient: no-op when other clients active ---

func TestReapOnLastClient_NoOpWhenOtherClientsActive(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "otherproj"
	release, _ := state.AcquireClientLease(slug)
	defer release()

	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err := reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	for _, w := range ui.WarnCalls {
		if w != "" {
			t.Errorf("expected no warnings on no-op, got: %q", w)
		}
	}
}

func TestReapOnLastClient_ClientLeaseHeld_NoReap(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "leaseproj"
	release, err := state.AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	defer release()

	// CountActiveClients >= 1 because current process holds the lease.
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err = reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("expected no error (lease held), got %v", err)
	}
}

// --- ReapOnLastClient: auto-stop mode ---

func TestReapOnLastClient_AutoStopOnActiveSessions_ReturnsImmediately(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "nopollproj"

	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err := reapOnLastClient(
		context.Background(),
		opencodeAgent(t),
		slug,
		sb,
		options.ReapPolicy{AutoStopOnActiveSessions: true},
		ui,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Spinner should NOT be created (no polling in auto-stop mode).
	if len(ui.SpinnerCalls) != 0 {
		t.Errorf("expected no spinner in auto-stop, got %d spinner calls", len(ui.SpinnerCalls))
	}

	// Should log via Verbose.
	found := false
	for _, v := range ui.VerboseCalls {
		if v != "" && len(v) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected verbose log in auto-stop mode")
	}
}

// --- ReapOnLastClient: wait mode, idle from start ---

func TestReapOnLastClient_WaitMode_IdleFromStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "idleproj"

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true,
			0,
			`{"s1":{"type":"idle","attempt":0}}`,
			"",
			nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	err := reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient: expected no error, got %v", err)
	}
}

// --- ReapOnLastClient: wait mode, empty status ---

func TestReapOnLastClient_WaitMode_EmptyStatus(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "emptyproj"

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(true, 0, `{}`, "", nil),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	err := reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient: expected no error, got %v", err)
	}
}

// --- ReapOnLastClient: context cancelled ---

func TestReapOnLastClient_ContextCancelled(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Session status is busy, so the poll loop keeps waiting.
	// Use a short timeout context that expires during the 2s ticker wait.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
			true,
			0,
			`{"s1":{"type":"busy","attempt":0}}`,
			"",
			nil,
		),
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	err := reapOnLastClient(shortCtx, opencodeAgent(t), "ctxproj", sb, options.ReapPolicy{}, ui)
	if err == nil {
		t.Fatal("expected error when context times out")
	}
}

func TestReapOnLastClient_WaitMode_BusyWithQuestionReaps(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "qproj"
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
				true, 0, `{"s1":{"type":"busy","attempt":0}}`, "", nil,
			),
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(
				true, 0, `[{"id":"que_1","sessionID":"s1","questions":[]}]`, "", nil,
			),
		},
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	err := reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient: expected no error, got %v", err)
	}
}

func TestReapOnLastClient_WaitMode_BusyWithQuestionErrorKeepsPolling(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	slug := "qerrproj"
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
				true, 0, `{"s1":{"type":"busy","attempt":0}}`, "", nil,
			),
			// /question returns a non-success (curl) so the session stays busy.
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(false, 7, "", "curl error", nil),
		},
	}
	sb.ExecOut = map[string]msb.ShellResult{
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}
	err := reapOnLastClient(shortCtx, opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err == nil {
		t.Fatal("expected error when context times out (session stays busy)")
	}
}

// --- ReapOnLastClient: nil sandbox ---

func TestReapOnLastClient_NilSandbox_AllModes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	tests := []struct {
		name   string
		policy options.ReapPolicy
	}{
		{"wait mode default", options.ReapPolicy{}},
		{"auto-stop mode", options.ReapPolicy{AutoStopOnActiveSessions: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &termio.Mock{}
			err := reapOnLastClient(context.Background(), opencodeAgent(t), "nilproj", nil, tt.policy, ui)
			if err != nil {
				t.Fatalf("expected no error with nil sb, got %v", err)
			}
		})
	}
}

// --- ReapOnLastClient: agent without session-status endpoints skips the wait ---

func TestReapOnLastClient_NoSessionStatusProvider_SkipsWait(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "nosessionproj"
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err := reapOnLastClient(context.Background(), noSessionStatusAgent{}, slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Spinner should NOT be created (no polling without session-status endpoints).
	if len(ui.SpinnerCalls) != 0 {
		t.Errorf("expected no spinner, got %d spinner calls", len(ui.SpinnerCalls))
	}
}

// --- ReapOnLastClient: does not fail successful attach ---

func TestReapDoesNotFailSuccessfulAttach(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	slug := "attachproj"
	release, err := state.AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(
				true,
				0,
				`{"s1":{"type":"idle","attempt":0}}`,
				"",
				nil,
			),
			"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
		},
	}

	// Release before calling reaper (simulating last client detaching).
	release()

	err = reapOnLastClient(context.Background(), opencodeAgent(t), slug, sb, options.ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient should not fail: %v", err)
	}
}

// --- ReapOnLastClient: skips agents without session-status endpoints ---

func TestReapOnLastClientSkipsNonDaemonAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	if _, isStatus := agent.AsSessionStatusProvider(a); isStatus {
		t.Skip("pi unexpectedly has a session-status provider")
	}
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	if err := reapOnLastClient(context.Background(), a, "slug", sb, options.ReapPolicy{}, ui); err != nil {
		t.Fatalf("reapOnLastClient = %v, want nil for agent without session-status endpoints", err)
	}
	if len(ui.SpinnerCalls) != 0 {
		t.Errorf("expected no spinner, got %d spinner calls", len(ui.SpinnerCalls))
	}
}

// --- quiescenceOf unit tests ---

func TestQuiescenceOf_AllIdle(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "idle", Attempt: 0},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "all idle", busy, stuck, 0, false)
}

func TestQuiescenceOf_AllRetryUnderCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 5},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "all retry under cap", busy, stuck, 0, false)
}

func TestQuiescenceOf_OneBusy(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "one busy", busy, stuck, 1, false)
}

func TestQuiescenceOf_MultipleBusy(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
		"s2": {Type: "busy", Attempt: 0},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "multiple busy", busy, stuck, 2, false)
}

func TestQuiescenceOf_RetryAtCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 10},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "retry at cap", busy, stuck, 0, true)
}

func TestQuiescenceOf_RetryOverCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 11},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "retry over cap", busy, stuck, 0, true)
}

func TestQuiescenceOf_MixedBusyAndRetry(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
		"s2": {Type: "retry", Attempt: 12},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "mixed busy and retry", busy, stuck, 1, true)
}

func TestQuiescenceOf_Empty(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{}, map[string]bool{}, 10)
	checkQuiescence(t, "empty", busy, stuck, 0, false)
}

func TestQuiescenceOf_MixedAllStates(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "idle", Attempt: 0},
		"s2": {Type: "busy", Attempt: 1},
		"s3": {Type: "retry", Attempt: 5},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "mixed all", busy, stuck, 1, false)
}

func TestQuiescenceOf_RetryRetryBoundary(t *testing.T) {
	// attempt = cap - 1 → not stuck
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 9},
	}, map[string]bool{}, 10)
	checkQuiescence(t, "retry one below cap", busy, stuck, 0, false)

	// attempt = cap → stuck
	_, stuck = quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 10},
	}, map[string]bool{}, 10)
	if !stuck {
		t.Error("attempt 10 == cap 10 should be stuck")
	}
}

func TestQuiescenceOf_BusyWithQuestionNotCounted(t *testing.T) {
	pending := map[string]bool{"s1": true}
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
	}, pending, 10)
	checkQuiescence(t, "busy with question", busy, stuck, 0, false)
}

func TestQuiescenceOf_BusyWithoutQuestionCounted(t *testing.T) {
	pending := map[string]bool{}
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
	}, pending, 10)
	checkQuiescence(t, "busy no question", busy, stuck, 1, false)
}

func TestQuiescenceOf_MixedQuestionAndWork(t *testing.T) {
	pending := map[string]bool{"s1": true}
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
		"s2": {Type: "busy", Attempt: 0},
	}, pending, 10)
	checkQuiescence(t, "mixed", busy, stuck, 1, false)
}

func TestQuiescenceOf_PendingDoesNotAffectRetry(t *testing.T) {
	pending := map[string]bool{"s1": true}
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 12},
	}, pending, 10)
	checkQuiescence(t, "retry unaffected", busy, stuck, 0, true)
}

// --- decodeSessionStates unit tests ---

func TestDecodeSessionStates_ValidJSON(t *testing.T) {
	input := `{"s1":{"type":"busy","attempt":3},"s2":{"type":"idle","attempt":0}}`
	got, err := decodeSessionStates(input)
	if err != nil {
		t.Fatalf("decodeSessionStates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["s1"].Type != "busy" || got["s1"].Attempt != 3 {
		t.Errorf("s1 = %+v, want {Type:busy Attempt:3}", got["s1"])
	}
	if got["s2"].Type != "idle" || got["s2"].Attempt != 0 {
		t.Errorf("s2 = %+v, want {Type:idle Attempt:0}", got["s2"])
	}
}

func TestDecodeSessionStates_EmptyJSON(t *testing.T) {
	got, err := decodeSessionStates(`{}`)
	if err != nil {
		t.Fatalf("decodeSessionStates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestDecodeSessionStates_EmptyString(t *testing.T) {
	got, err := decodeSessionStates("")
	if err != nil {
		t.Fatalf("decodeSessionStates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestDecodeSessionStates_MalformedJSON(t *testing.T) {
	_, err := decodeSessionStates(`{invalid json`)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecodeSessionStates_StringType(t *testing.T) {
	_, err := decodeSessionStates(`"string"`)
	if err == nil {
		t.Fatal("expected error for non-object JSON type")
	}
}

func TestDecodeSessionStates_ArrayType(t *testing.T) {
	_, err := decodeSessionStates(`[]`)
	if err == nil {
		t.Fatal("expected error for array JSON type")
	}
}

// --- options.ReapPolicy defaults ---

func TestDefaultMaxSessionRetriesIsTen(t *testing.T) {
	if defaultMaxSessionRetries != 10 {
		t.Errorf("defaultMaxSessionRetries = %d, want 10", defaultMaxSessionRetries)
	}
}

func TestReapPolicy_DefaultZeroValue(t *testing.T) {
	policy := options.ReapPolicy{}
	if policy.AutoStopOnActiveSessions {
		t.Error("default AutoStopOnActiveSessions should be false")
	}
	if policy.MaxSessionRetries != 0 {
		t.Errorf("default MaxSessionRetries = %d, want 0", policy.MaxSessionRetries)
	}
}

func TestDecodeQuestionRequests_ValidArray(t *testing.T) {
	input := `[{"id":"que_1","sessionID":"ses_a","questions":[{"question":"q"}]},{"id":"que_2","sessionID":"ses_b","questions":[{"question":"r"}]}]`
	got, err := decodeQuestionRequests(input)
	if err != nil {
		t.Fatalf("decodeQuestionRequests: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].SessionID != "ses_a" || got[1].SessionID != "ses_b" {
		t.Errorf("unexpected sessionIDs: %+v", got)
	}
}

func TestDecodeQuestionRequests_EmptyArray(t *testing.T) {
	got, err := decodeQuestionRequests(`[]`)
	if err != nil {
		t.Fatalf("decodeQuestionRequests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestDecodeQuestionRequests_EmptyString(t *testing.T) {
	got, err := decodeQuestionRequests("")
	if err != nil {
		t.Fatalf("decodeQuestionRequests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestDecodeQuestionRequests_MalformedJSON(t *testing.T) {
	if _, err := decodeQuestionRequests(`{invalid`); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecodeQuestionRequests_NonArray(t *testing.T) {
	if _, err := decodeQuestionRequests(`{}`); err == nil {
		t.Fatal("expected error for non-array JSON")
	}
}

func TestPendingQuestionSessionIDs_PopulatesSet(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(
				true, 0,
				`[{"id":"que_1","sessionID":"ses_a","questions":[]},{"id":"que_2","sessionID":"ses_b","questions":[]}]`,
				"", nil,
			),
		},
	}
	got, err := pendingQuestionSessionIDs(context.Background(), sb, "curl -sf http://127.0.0.1:4096/question")
	if err != nil {
		t.Fatalf("pendingQuestionSessionIDs: %v", err)
	}
	if !got["ses_a"] || !got["ses_b"] {
		t.Errorf("expected both sessions in set, got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestPendingQuestionSessionIDs_Empty(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(true, 0, `[]`, "", nil),
		},
	}
	got, err := pendingQuestionSessionIDs(context.Background(), sb, "curl -sf http://127.0.0.1:4096/question")
	if err != nil {
		t.Fatalf("pendingQuestionSessionIDs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty set, got %v", got)
	}
}

func TestPendingQuestionSessionIDs_CurlFailure(t *testing.T) {
	sb := &msb.MockSandbox{
		Name_: "test-vm",
		ShellOut: map[string]msb.ShellResult{
			"curl -sf http://127.0.0.1:4096/question": msb.NewTestResult(false, 7, "", "curl error", nil),
		},
	}
	_, err := pendingQuestionSessionIDs(context.Background(), sb, "curl -sf http://127.0.0.1:4096/question")
	if err == nil {
		t.Fatal("expected error on curl failure")
	}
}

func checkQuiescence(t *testing.T, name string, busy int, stuck bool, wantBusy int, wantStuck bool) {
	t.Helper()
	if busy != wantBusy {
		t.Errorf("%s: busy = %d, want %d", name, busy, wantBusy)
	}
	if stuck != wantStuck {
		t.Errorf("%s: stuckRetry = %v, want %v", name, stuck, wantStuck)
	}
}

func TestSessionStatesShellError(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")

	if _, err := sessionStates(context.Background(), sb, "curl -sf http://127.0.0.1:4096/session/status"); err == nil {
		t.Error("sessionStates() with Shell error should return error")
	}
}

func TestSessionStatesNonSuccess(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(false, 7, "", "curl error", nil),
	}

	if _, err := sessionStates(context.Background(), sb, "curl -sf http://127.0.0.1:4096/session/status"); err == nil {
		t.Error("sessionStates() with non-success result should return error")
	}
}

func TestPendingQuestionSessionIDsShellError(t *testing.T) {
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellErr = errors.New("shell failed")

	if _, err := pendingQuestionSessionIDs(
		context.Background(),
		sb,
		"curl -sf http://127.0.0.1:4096/question",
	); err == nil {
		t.Error("pendingQuestionSessionIDs() with Shell error should return error")
	}
}
