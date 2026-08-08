package sandbox

import (
	"context"
	"testing"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// --- ReapOnLastClient: no-op when other clients active ---

func TestReapOnLastClient_NoOpWhenOtherClientsActive(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	slug := "otherproj"
	release, _ := AcquireClientLease(slug)
	defer release()

	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err := ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{}, ui)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	slug := "leaseproj"
	release, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	// CountActiveClients >= 1 because current process holds the lease.
	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err = ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("expected no error (lease held), got %v", err)
	}
}

// --- ReapOnLastClient: auto-stop mode ---

func TestReapOnLastClient_AutoStopOnActiveSessions_ReturnsImmediately(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	slug := "nopollproj"

	ui := &termio.Mock{}
	sb := msb.NewMockSandbox(msb.SandboxOpts{})

	err := ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{AutoStopOnActiveSessions: true}, ui)
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
	SetStateDirForTest(t, t.TempDir()+"/opencode")

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
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	err := ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient: expected no error, got %v", err)
	}
}

// --- ReapOnLastClient: wait mode, empty status ---

func TestReapOnLastClient_WaitMode_EmptyStatus(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	slug := "emptyproj"

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{Name_: "test-vm"}
	sb.ShellOut = map[string]msb.ShellResult{
		"curl -sf http://127.0.0.1:4096/session/status": msb.NewTestResult(true, 0, `{}`, "", nil),
		"sleep 1h": msb.NewTestResult(true, 0, "", "", nil),
	}

	err := ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient: expected no error, got %v", err)
	}
}

// --- ReapOnLastClient: context cancelled ---

func TestReapOnLastClient_ContextCancelled(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

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

	err := ReapOnLastClient(shortCtx, "ctxproj", sb, ReapPolicy{}, ui)
	if err == nil {
		t.Fatal("expected error when context times out")
	}
}

// --- ReapOnLastClient: nil sandbox ---

func TestReapOnLastClient_NilSandbox_AllModes(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	tests := []struct {
		name   string
		policy ReapPolicy
	}{
		{"wait mode default", ReapPolicy{}},
		{"auto-stop mode", ReapPolicy{AutoStopOnActiveSessions: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &termio.Mock{}
			err := ReapOnLastClient(context.Background(), "nilproj", nil, tt.policy, ui)
			if err != nil {
				t.Fatalf("expected no error with nil sb, got %v", err)
			}
		})
	}
}

// --- ReapOnLastClient: does not fail successful attach ---

func TestReapDoesNotFailSuccessfulAttach(t *testing.T) {
	SetStateDirForTest(t, t.TempDir()+"/opencode")

	slug := "attachproj"
	release, err := AcquireClientLease(slug)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
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

	err = ReapOnLastClient(context.Background(), slug, sb, ReapPolicy{}, ui)
	if err != nil {
		t.Fatalf("ReapOnLastClient should not fail: %v", err)
	}
}

// --- quiescenceOf unit tests ---

func TestQuiescenceOf_AllIdle(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "idle", Attempt: 0},
	}, 10)
	checkQuiescence(t, "all idle", busy, stuck, 0, false)
}

func TestQuiescenceOf_AllRetryUnderCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 5},
	}, 10)
	checkQuiescence(t, "all retry under cap", busy, stuck, 0, false)
}

func TestQuiescenceOf_OneBusy(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
	}, 10)
	checkQuiescence(t, "one busy", busy, stuck, 1, false)
}

func TestQuiescenceOf_MultipleBusy(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
		"s2": {Type: "busy", Attempt: 0},
	}, 10)
	checkQuiescence(t, "multiple busy", busy, stuck, 2, false)
}

func TestQuiescenceOf_RetryAtCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 10},
	}, 10)
	checkQuiescence(t, "retry at cap", busy, stuck, 0, true)
}

func TestQuiescenceOf_RetryOverCap(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 11},
	}, 10)
	checkQuiescence(t, "retry over cap", busy, stuck, 0, true)
}

func TestQuiescenceOf_MixedBusyAndRetry(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "busy", Attempt: 0},
		"s2": {Type: "retry", Attempt: 12},
	}, 10)
	checkQuiescence(t, "mixed busy and retry", busy, stuck, 1, true)
}

func TestQuiescenceOf_Empty(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{}, 10)
	checkQuiescence(t, "empty", busy, stuck, 0, false)
}

func TestQuiescenceOf_MixedAllStates(t *testing.T) {
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "idle", Attempt: 0},
		"s2": {Type: "busy", Attempt: 1},
		"s3": {Type: "retry", Attempt: 5},
	}, 10)
	checkQuiescence(t, "mixed all", busy, stuck, 1, false)
}

func TestQuiescenceOf_RetryRetryBoundary(t *testing.T) {
	// attempt = cap - 1 → not stuck
	busy, stuck := quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 9},
	}, 10)
	checkQuiescence(t, "retry one below cap", busy, stuck, 0, false)

	// attempt = cap → stuck
	_, stuck = quiescenceOf(map[string]SessionStatus{
		"s1": {Type: "retry", Attempt: 10},
	}, 10)
	if !stuck {
		t.Error("attempt 10 == cap 10 should be stuck")
	}
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

// --- ReapPolicy defaults ---

func TestDefaultMaxSessionRetriesIsTen(t *testing.T) {
	if defaultMaxSessionRetries != 10 {
		t.Errorf("defaultMaxSessionRetries = %d, want 10", defaultMaxSessionRetries)
	}
}

func TestReapPolicy_DefaultZeroValue(t *testing.T) {
	policy := ReapPolicy{}
	if policy.AutoStopOnActiveSessions {
		t.Error("default AutoStopOnActiveSessions should be false")
	}
	if policy.MaxSessionRetries != 0 {
		t.Errorf("default MaxSessionRetries = %d, want 0", policy.MaxSessionRetries)
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
