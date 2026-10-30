package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// dueRootState seeds a root document whose opencode check is due (older than the
// once-per-day window), so checkAgentUpgrade would run the version request.
func dueRootState() state.RootState {
	return state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}
}

// TestCheckAgentUpgradeDefersWhenRootLockHeld pins that a contended root lock at
// the cached read defers the step without running the version request: the
// non-blocking read returns ok=false, and checkAgentUpgrade must propagate it
// rather than fall through to the network.
func TestCheckAgentUpgradeDefersWhenRootLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(dueRootState()); err != nil {
		t.Fatal(err)
	}

	// Hold the root lock for the whole call: another process's in-flight
	// campaign is exactly the contended case the non-blocking read defers on.
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = state.WithRootState(true, func(*state.RootTxn) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	t.Cleanup(func() {
		close(release)
		<-done
	})

	latestCalled := false
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, &termio.Mock{})
	if checked {
		t.Error("checkAgentUpgrade must defer while the root lock is held")
	}
	if entry != (state.AgentCheckState{}) {
		t.Errorf("entry = %+v, want the zero value on defer", entry)
	}
	if latestCalled {
		t.Error("the version request must not run when the cached read was skipped")
	}
}

// TestCheckAgentUpgradeDefersWhenRootStateUnreadable pins the unreadable-cached
// read: an error from the root transaction warns and defers, and the version
// request never runs over an unknown document.
func TestCheckAgentUpgradeDefersWhenRootStateUnreadable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	latestCalled := false
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui)
	if checked {
		t.Error("checkAgentUpgrade must defer when the root state is unreadable")
	}
	if entry != (state.AgentCheckState{}) {
		t.Errorf("entry = %+v, want the zero value on defer", entry)
	}
	if latestCalled {
		t.Error("the version request must not run when the cached read failed")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read upgrade check state") {
		t.Errorf("expected a root-state read warning, got %v", ui.WarnCalls)
	}
}

// TestCheckAgentUpgradeDefersWhenVersionRequestFails pins the offline arm: a
// failed version request warns, leaves the window open, and defers (checked
// false) so the caller retries next run.
func TestCheckAgentUpgradeDefersWhenVersionRequestFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(dueRootState()); err != nil {
		t.Fatal(err)
	}

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("offline")
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui)
	if checked {
		t.Error("checkAgentUpgrade must defer when the version request fails")
	}
	if entry != (state.AgentCheckState{}) {
		t.Errorf("entry = %+v, want the zero value on defer", entry)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not check for opencode updates") {
		t.Errorf("expected an offline warning, got %v", ui.WarnCalls)
	}
}

// TestCheckAgentUpgradeCheckerLessAgentSkipsVersionRequest pins the hasChecker
// operand: a checker-less agent returns its cached entry without running the
// version request, even when the entry is due.
func TestCheckAgentUpgradeCheckerLessAgentSkipsVersionRequest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(dueRootState()); err != nil {
		t.Fatal(err)
	}

	latestCalled := false
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), false, &termio.Mock{})
	if !checked {
		t.Error("a checker-less agent still returns its cached state")
	}
	if entry.LatestVersion != "1.0.0" {
		t.Errorf("entry.LatestVersion = %q, want the cached 1.0.0", entry.LatestVersion)
	}
	if latestCalled {
		t.Error("a checker-less agent must not run the version request")
	}
}

// TestCheckAgentUpgradeDefersWhenRootLockHeldDuringWrite pins the post-request
// lock contention: if another process takes the root lock while the version
// request runs, the write is skipped and the step defers, but the freshly
// fetched entry is still returned so the caller can offer it.
func TestCheckAgentUpgradeDefersWhenRootLockHeldDuringWrite(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(dueRootState()); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		// Take the root lock while the request is "in flight" so the
		// subsequent write transaction contends.
		go func() {
			defer close(done)
			_, _ = state.WithRootState(true, func(*state.RootTxn) error {
				close(held)
				<-release
				return nil
			})
		}()
		<-held
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })
	t.Cleanup(func() {
		close(release)
		<-done
	})

	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, &termio.Mock{})
	if checked {
		t.Error("checkAgentUpgrade must defer when the write transaction is contended")
	}
	if entry.LatestVersion != "2.0.0" {
		t.Errorf("entry.LatestVersion = %q, want the freshly fetched 2.0.0", entry.LatestVersion)
	}
}
