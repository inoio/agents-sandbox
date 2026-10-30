package vm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// withExistingAgentVersion makes the project's existing runner image report the
// fixture baseline version.
func withExistingAgentVersion(t *testing.T) {
	t.Helper()
	orig := existingAgentVersion
	existingAgentVersion = func(_ context.Context, _, _ string) (string, bool) { return "1.0.0", true }
	t.Cleanup(func() { existingAgentVersion = orig })
}

// seedAgentState writes the agent version/source into this project's HomeState,
// preserving any other fields already present.
func seedAgentState(t *testing.T, k state.Key, version, source string) {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		hs = &state.HomeState{}
	}
	hs.AgentVersion = version
	hs.AgentSource = source
	if err := seedHomeState(k, *hs); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
}

func TestResolveBuildVersionSkipsCheckForUserAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "9.9.9", agentSourceUser)

	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for a user-provided agent")
	}
	if got != "9.9.9" {
		t.Errorf("resolveBuildVersion = %q, want recorded 9.9.9", got)
	}
	if latestCalled {
		t.Error("agentLatestVersion must not be called for a user-provided agent")
	}
}

func TestResolveBuildVersionUserAgentEmptyVersionLeavesResolutionToImageBuild(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "", agentSourceUser)

	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if upgraded {
		t.Error("expected upgraded=false for a user-provided agent")
	}
	if got != "" {
		t.Errorf("resolveBuildVersion = %q, want empty version", got)
	}
	if latestCalled {
		t.Error("agentLatestVersion must not be called for a user-provided agent")
	}
}

// TestResolveAgentVersionForBuild pins the one shared precedence both the
// standalone build and a run derive from: a user-provided agent is not owned by
// the tool, so the pin is ignored and the recorded version is used with
// userProvided=true; every other project honors the requested pin.
func TestResolveAgentVersionForBuild(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		recordedVersion  string
		requestedVersion string
		wantVersion      string
		wantUserProvided bool
	}{
		{
			name:             "user-provided agent ignores the pin",
			source:           agentSourceUser,
			recordedVersion:  "1.0.0",
			requestedVersion: "9.9.9",
			wantVersion:      "1.0.0",
			wantUserProvided: true,
		},
		{
			name:             "user-provided agent without a recorded version leaves resolution to the build",
			source:           agentSourceUser,
			recordedVersion:  "",
			requestedVersion: "9.9.9",
			wantVersion:      "",
			wantUserProvided: true,
		},
		{
			name:             "tool-managed agent honors the pin",
			source:           agentSourceTool,
			recordedVersion:  "1.0.0",
			requestedVersion: "9.9.9",
			wantVersion:      "9.9.9",
			wantUserProvided: false,
		},
		{
			name:             "tool-managed agent without a pin passes the request through",
			source:           agentSourceTool,
			recordedVersion:  "1.0.0",
			requestedVersion: "",
			wantVersion:      "",
			wantUserProvided: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := state.Key{Slug: "proj", Agent: "opencode"}
			seedAgentState(t, k, testCase.recordedVersion, testCase.source)

			version, userProvided := ResolveAgentVersionForBuild(k, testCase.requestedVersion)
			if version != testCase.wantVersion {
				t.Errorf("version = %q, want %q", version, testCase.wantVersion)
			}
			if userProvided != testCase.wantUserProvided {
				t.Errorf("userProvided = %v, want %v", userProvided, testCase.wantUserProvided)
			}
		})
	}
}

func TestResolveOpenCodeVersionPinnedSkipsUpdateCheck(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	opts := options.RunOptions{AgentVersion: "2.0.0"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected pinned version 2.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a pinned version")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called when the version is pinned")
	}
}

func TestResolveOpenCodeVersionRebuildStillChecksUpdate(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	opts := options.RunOptions{Rebuild: true}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0 for a non-interactive keep, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a non-interactive keep")
	}
	if !latestCalled {
		t.Error("agentLatestVersion should be called when Rebuild is set; --rebuild only bypasses the cache")
	}
}

func TestResolveOpenCodeVersionUserProvidedWinsOverPin(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceUser)
	opts := options.RunOptions{AgentVersion: "9.9.9"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected recorded user-provided version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a user-provided agent")
	}
	if latestCalled {
		t.Error("agentLatestVersion must not be called for a user-provided agent")
	}
}

func TestResolveOpenCodeVersionNoBaseline(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty version (no baseline), got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false without a baseline")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called without a baseline")
	}
}

func TestResolveOpenCodeVersionRecoversBaselineFromExistingImage(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// An existing state without a recorded baseline (as after the updater.yaml
	// migration), and an image that carries the baseline.
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol-1"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	withExistingAgentVersion(t)

	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}

	got, upgraded, offered, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want recovered baseline 1.0.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a keep choice")
	}
	if offered != "2.0.0" {
		t.Errorf("offered = %q, want the shown release 2.0.0 returned for deferred persistence", offered)
	}
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt %q must name both the recovered baseline and the available version (%s)", prompt, want)
		}
	}

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentVersion != "1.0.0" {
		t.Errorf("recorded agent_version = %q, want recovered 1.0.0 persisted", hs.AgentVersion)
	}
}

func TestPersistOfferedVersionMarksExistingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	persistOfferedVersion(k, "2.0.0", &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Errorf("offered set not recorded: %+v", hs.OfferedVersions)
	}
}

func TestPersistOfferedVersionSkipsMissingState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}

	persistOfferedVersion(k, "2.0.0", &termio.Mock{})

	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

func TestPersistOfferedVersionNoVersionIsNoop(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	persistOfferedVersion(k, "", &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if len(hs.OfferedVersions) != 0 {
		t.Errorf("expected no offered versions, got %v", hs.OfferedVersions)
	}
}

func TestPersistOfferedVersionWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	persistOfferedVersion(k, "2.0.0", ui)

	if !contains(joinStrings(ui.WarnCalls), "could not persist project state") {
		t.Errorf("expected a persist warning, got %v", ui.WarnCalls)
	}
}

// TestPersistAgentVersionWarnsOnError verifies that a failure to record the
// baked agent version only warns: recording must never fail a run.
func TestPersistAgentVersionWarnsOnError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	persistAgentVersion(k, "2.0.0", ui)

	if !contains(joinStrings(ui.WarnCalls), "could not persist the agent version") {
		t.Errorf("expected a persist warning, got %v", ui.WarnCalls)
	}
}

// TestDeferredOfferedPersistedAfterStateRecreated is the regression test for the
// "don't remind again" choice being forgotten when the project state was
// missing: the offered release returned by resolveBuildVersion is persisted
// after home resolution recreates the state.
func TestDeferredOfferedPersistedAfterStateRecreated(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	withExistingAgentVersion(t)

	_, _, offered, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if offered != "2.0.0" {
		t.Fatalf("offered = %q, want 2.0.0", offered)
	}

	// Home resolution recreates the state, then the deferred mark is recorded.
	if err := seedHomeState(k, state.HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	persistOfferedVersion(k, offered, &termio.Mock{})

	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Errorf("offered set not persisted after state recreation: %v", hs.OfferedVersions)
	}
}

func TestResolveOpenCodeVersionRecoveryDoesNotCreatePartialState(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	// No state file at all, but the image carries a baseline. Recovery must use
	// it without creating a fingerprint-less partial state that would poison
	// home recovery (#183).
	withExistingAgentVersion(t)

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want recovered 1.0.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a non-interactive keep")
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no partial state written)", err)
	}
}

func TestResolveOpenCodeVersionRecoveredBaselineReusedOffline(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("offline")
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	withExistingAgentVersion(t)

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want recovered baseline 1.0.0 so the existing image is reused", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when the check is offline")
	}
}

// TestResolveBuildVersionReconcilesStaleBaselineWithImage pins that a recorded
// baseline which disagrees with the present image is stale: the image label wins
// (it is what is actually installed) and the cache is rewritten, so the stale
// value can never select an older build.
func TestResolveBuildVersionReconcilesStaleBaselineWithImage(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "1.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "0.9.0", agentSourceTool) // stale: the image holds 1.0.0
	withExistingAgentVersion(t)                    // seam returns "1.0.0", true

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the image's 1.0.0, not the stale 0.9.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false: 1.0.0 is not newer than the image's 1.0.0")
	}
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if hs.AgentVersion != "1.0.0" {
		t.Errorf("recorded agent_version = %q, want the image's 1.0.0", hs.AgentVersion)
	}
}

// TestResolveBuildVersionPinWinsOverImageLabel pins that reconciliation never
// reaches an explicit pin: the pin returns before the recovery block.
func TestResolveBuildVersionPinWinsOverImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "0.9.0", agentSourceTool)
	withExistingAgentVersion(t) // image reports 1.0.0

	got, _, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{},
		options.RunOptions{Agent: "opencode", AgentVersion: "3.0.0"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "3.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the explicit pin 3.0.0", got)
	}
}

// TestResolveBuildVersionKeepsBaselineWhenImageAbsent pins the offline-reuse
// fallback: with no image label to reconcile against, the recorded value stays
// authoritative.
func TestResolveBuildVersionKeepsBaselineWhenImageAbsent(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool) // no withExistingAgentVersion: seam defaults to absent

	ui := &termio.Mock{SelectFn: func(string, []termio.Choice, string) (string, error) { return "k", nil }}
	got, upgraded, _, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), ui, options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want the recorded 1.0.0 kept for offline reuse", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a keep choice")
	}
}

func TestResolveOpenCodeVersionRecoveryPersistFailureStillUsesVersion(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	withExistingAgentVersion(t)
	// A directory at the state path makes every state read/write fail, so the
	// recovery write cannot be recorded and the project state is unreadable.
	if err := os.MkdirAll(filepath.Join(state.KeyDir(k), "state.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want recovered 1.0.0 even when persistence fails", got)
	}
	if upgraded {
		t.Error("expected upgraded=false")
	}
	if !contains(joinStrings(ui.VerboseCalls), "could not record recovered agent version") {
		t.Errorf("expected a verbose note about the failed recovery write, got %v", ui.VerboseCalls)
	}
}

func TestResolveOpenCodeVersionNoNewerVersion(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "2.0.0", agentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected current version 2.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when no newer version exists")
	}
}

func TestResolveOpenCodeVersionNonInteractiveLogsUpgradeAvailable(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false in a non-interactive session")
	}
	wantInfo := "opencode 2.0.0 available (image has 1.0.0); run 'agents-sandbox build' to upgrade"
	if !slices.Contains(ui.InfoCalls, wantInfo) {
		t.Errorf("expected info %q in output, got %v", wantInfo, ui.InfoCalls)
	}
}

func TestResolveOpenCodeVersionInteractiveRebuild(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("expected new version 2.0.0, got %q", got)
	}
	if !upgraded {
		t.Error("expected upgraded=true after choosing to rebuild")
	}
}

func TestResolveOpenCodeVersionSkipsCheckWhenCheckedToday(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("seedRootState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when already checked today")
	}
	if latestCalled {
		t.Error("agentLatestVersion should NOT have been called when already checked today")
	}
}

func TestResolveOpenCodeVersionDoesNotReOfferVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatal(err)
	}
	hs.OfferedVersions = []string{"2.0.0"}
	if err := seedHomeState(k, *hs); err != nil {
		t.Fatal(err)
	}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for an already-offered version")
	}
}

func TestResolveOpenCodeVersionRecordsOfferedBeforePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	if _, _, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The prompt was shown (mock default returns "r" → Rebuild), so 2.0.0 must
	// now be recorded as offered in this project's HomeState, and the root
	// window refreshed.
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Error("expected 2.0.0 to be recorded as offered after the prompt")
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Error("expected LastChecked to be refreshed after a successful check")
	}
}

func TestResolveOpenCodeVersionUnparsableLatestKeepsCurrent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	// An unparsable latest version must never fail the session or offer an upgrade.
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "not-a-version", nil }

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("unparsable latest version must not fail the session, got: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when the latest version is unparsable")
	}
}

func TestResolveOpenCodeVersionOfflineDoesNotUpdateLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("network unreachable")
	}

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if err != nil {
		t.Fatalf("offline check must not fail the session, got: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("expected current version 1.0.0 when offline, got %q", got)
	}
	if upgraded {
		t.Error("expected upgraded=false when offline")
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning about the failed update check")
	}

	// The failed (offline) check must NOT refresh LastChecked, so the next
	// online run within the same day still retries.
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if !root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Errorf(
			"expected LastChecked to remain zero after offline check, got %v",
			root.AgentCheck("opencode").LastChecked,
		)
	}
}

func TestResolveOpenCodeVersionInteractiveQuit(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}

	_, _, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
	if !errors.Is(err, errUpgradeQuit) {
		t.Fatalf("expected errUpgradeQuit, got %v", err)
	}
}

// TestResolveBuildVersionPromptNamesThisProjectsVersion is the cross-project
// regression: a globally cached latest version must never make the prompt name
// another project's image version. The prompt's "current" is this project's
// HomeState baseline (1.0.0), not a machine-global value.
func TestResolveBuildVersionPromptNamesThisProjectsVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	// latest known version cached globally; this project's image baseline is 1.0.0
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	origLatest := agentLatestVersion
	t.Cleanup(func() { agentLatestVersion = origLatest })
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) { return "2.0.0", nil }
	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}
	if _, _, _, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{Agent: "opencode"},
	); err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if !strings.Contains(prompt, "1.0.0") || !strings.Contains(prompt, "2.0.0") {
		t.Errorf("prompt %q must name both 1.0.0 and 2.0.0", prompt)
	}
}

// TestPromptUpgradeNamesBothVersions pins the PR #225 wording: the prompt and
// the Rebuild choice both name the available release and this project's current
// version, so the user can tell exactly what a rebuild would change.
func TestPromptUpgradeNamesBothVersions(t *testing.T) {
	var prompt string
	var choices []termio.Choice
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, c []termio.Choice, _ string) (string, error) {
			prompt, choices = p, c
			return "k", nil
		},
	}
	if _, err := promptUpgrade(ui, opencodeAgent(t), "1.0.0", "2.0.0"); err != nil {
		t.Fatalf("promptUpgrade: %v", err)
	}
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt %q must name %s", prompt, want)
		}
	}
	rebuild := slices.IndexFunc(choices, func(c termio.Choice) bool { return c.Key == "r" })
	if rebuild < 0 {
		t.Fatalf("Rebuild choice missing from %+v", choices)
	}
	description := choices[rebuild].Description
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(description, want) {
			t.Errorf("Rebuild description %q must name %s", description, want)
		}
	}
}

// TestPendingUpgradeLockHeldDefers pins that a held root-state lock (e.g. an
// in-flight runtime-home copy campaign) defers the whole upgrade step to the
// next run instead of racing the other writer.
func TestPendingUpgradeLockHeldDefers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	ok, err := state.WithRootState(true, func(*state.RootTxn) error {
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
				t.Error("must not prompt while the root-state lock is held")
				return "r", nil
			},
		}
		latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, ui, "1.0.0")
		if offer || latest != "" {
			t.Errorf("pendingUpgrade = (%q, %v), want no offer with a held lock", latest, offer)
		}
		return nil
	})
	if err != nil || !ok {
		t.Fatalf("WithRootState(blocking) = ok %v, err %v", ok, err)
	}
}

// TestPendingUpgradeSuppressedWhenAlreadyOffered pins the HomeState offered
// gate: a version already offered for this project is not offered again.
func TestPendingUpgradeSuppressedWhenAlreadyOffered(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatal(err)
	}
	hs.MarkOffered("2.0.0")
	if err := seedHomeState(k, *hs); err != nil {
		t.Fatal(err)
	}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	latest, offer := pendingUpgrade(
		context.Background(),
		opencodeAgent(t),
		k,
		&termio.Mock{IsInteractiveResult: true},
		"1.0.0",
	)
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer for an already-offered version", latest, offer)
	}
}

// TestPendingUpgradeClaimSuppressesNextOffer pins the atomic claim: one
// pendingUpgrade call both checks the offered gate and records the mark in a
// single home transaction, so an immediate second launch reading the same state
// does not prompt again.
func TestPendingUpgradeClaimSuppressesNextOffer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("seedRootState: %v", err)
	}

	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("first pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}

	latest, offer = pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if offer || latest != "" {
		t.Errorf("second pendingUpgrade = (%q, %v), want no offer after the first claimed it", latest, offer)
	}
}

// TestPendingUpgradeOfferedWritePreservesFingerprints is the property guard for
// pendingUpgrade's offered mark: recording the offered version must merge into
// the existing HomeState, never replace it, so the project's env/secret
// fingerprints survive.
func TestPendingUpgradeOfferedWritePreservesFingerprints(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seed := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "d",
		AgentVersion: "1.0.0",
		AgentSource:  agentSourceTool,
		EnvState:     state.EnvState{Hash: "e"},
		SecretState:  state.SecretState{Hash: "s"},
	}
	if err := seedHomeState(k, seed); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("seedRootState: %v", err)
	}

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		t.Error("agentLatestVersion must not be called when the check is not due")
		return "", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}

	got, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.EnvState.Hash != "e" || got.SecretState.Hash != "s" {
		t.Errorf("fingerprints not preserved: %+v", got)
	}
	want := seed
	want.MarkOffered("2.0.0")
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("offered write changed unrelated fields:\n got %+v\nwant %+v", *got, want)
	}
}

// TestPendingUpgradeRootStateReadErrorDoesNotClobber is the regression for the
// clobber bug: an unreadable root state (which holds the migrations registry and
// every other agent's cached check) must never be overwritten by the upgrade
// check. On a read error the step warns, returns no offer, and leaves the file
// byte-for-byte unchanged.
func TestPendingUpgradeRootStateReadErrorDoesNotClobber(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	if err := seedRootState(state.RootState{
		Migrations: map[string]state.Migration{
			"runtime_home": {Status: state.MigrationDismissed},
		},
	}); err != nil {
		t.Fatal(err)
	}
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	corrupt := []byte("not: valid: yaml: [")
	if err := os.WriteFile(rootStatePath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, ui, "1.0.0")
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer on a root-state read error", latest, offer)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read upgrade check state") {
		t.Errorf("expected a root-state read warning, got %v", ui.WarnCalls)
	}
	got, err := os.ReadFile(rootStatePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, corrupt) {
		t.Errorf("root state was rewritten on a read error: got %q, want %q", got, corrupt)
	}
}

// TestPendingUpgradeWarnsOnRootStateWriteError covers the root-state write
// failure: the state directory is read-only, so ReadRootState succeeds but
// WriteRootState cannot create its temp file. The step warns and the offer
// still proceeds from the freshly fetched latest version.
func TestPendingUpgradeWarnsOnRootStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	stateDir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "locks", "root.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Skipf("cannot chmod state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, ui, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not persist upgrade check state") {
		t.Errorf("expected a root-state persist warning, got %v", ui.WarnCalls)
	}
}

// TestPendingUpgradeNoOfferWhenLatestEmpty covers the empty-cached-latest arm:
// a successful check that yields no version never offers.
func TestPendingUpgradeNoOfferWhenLatestEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "", nil }
	t.Cleanup(func() { agentLatestVersion = origLatest })

	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer for an empty latest", latest, offer)
	}
}

// TestPendingUpgradeWarnsOnProjectStateReadError covers the non-not-found
// HomeState read error: a directory at the project state file path makes
// ReadState fail, so the step warns and offers nothing.
func TestPendingUpgradeWarnsOnProjectStateReadError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent, "state.yaml"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	ui := &termio.Mock{}
	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, ui, "1.0.0")
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer on a project-state read error", latest, offer)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read or persist project state") {
		t.Errorf("expected a project-state read warning, got %v", ui.WarnCalls)
	}
}

// TestPendingUpgradeOffersWhenProjectStateMissing covers the ErrStateNotFound
// arm: with no project state file the offered set is empty, so the version is
// offered, but it is not persisted: creating a state here (before home
// resolution) would leave a fingerprint-less partial state that poisons home
// recovery (#183).
func TestPendingUpgradeOffersWhenProjectStateMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no partial state written)", err)
	}
}

// TestPendingUpgradeWarnsOnProjectStateWriteError covers the HomeState write
// failure: the key directory is read-only, so ReadState succeeds but WriteState
// cannot create its temp file. The step still returns the offer.
func TestPendingUpgradeWarnsOnProjectStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	keyDir := state.KeyDir(k)
	if err := os.Chmod(keyDir, 0o555); err != nil {
		t.Skipf("cannot chmod key dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(keyDir, 0o700) })

	ui := &termio.Mock{}
	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, ui, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	joined := strings.Join(ui.WarnCalls, " ")
	if !strings.Contains(joined, "could not read or persist project state") {
		t.Errorf("expected a project-state persist warning, got %q", joined)
	}
}

// TestCheckAgentUpgradeNetworkRunsWithoutRootLock is the #334 regression: the
// agent-version request must not run while the launcher-global root lock is held,
// so unrelated projects are not serialized behind one project's egress check. The
// stub asserts the lock is free by taking it non-blocking from inside the request.
func TestCheckAgentUpgradeNetworkRunsWithoutRootLock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		ok, err := state.WithRootState(false, func(*state.RootTxn) error { return nil })
		if err != nil {
			t.Fatalf("WithRootState during the version request: %v", err)
		}
		if !ok {
			t.Error("the root lock is held while the version request runs")
		}
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, &termio.Mock{})
	if !checked {
		t.Fatal("checkAgentUpgrade deferred unexpectedly")
	}
	if entry.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want 2.0.0", entry.LatestVersion)
	}
}

// TestCheckAgentUpgradeSkipsWriteWhenWindowRefreshedConcurrently is the
// single-write guard for #334: while one process's version request is in flight,
// another process may refresh the window. The re-check under the lock must then
// skip this process's write and reuse the freshest cached entry, so two
// concurrent due checks persist at most one refresh.
func TestCheckAgentUpgradeSkipsWriteWhenWindowRefreshedConcurrently(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	concurrentCheckedAt := now()
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		ok, err := state.WithRootState(false, func(txn *state.RootTxn) error {
			txn.State().SetAgentCheck("opencode", state.AgentCheckState{
				LastChecked:   concurrentCheckedAt,
				LatestVersion: "9.9.9",
			})
			return txn.Commit()
		})
		if err != nil {
			t.Fatalf("concurrent refresh: %v", err)
		}
		if !ok {
			t.Fatal("concurrent refresh could not take the free root lock")
		}
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, &termio.Mock{})
	if !checked {
		t.Fatal("checkAgentUpgrade deferred unexpectedly")
	}
	if entry.LatestVersion != "9.9.9" {
		t.Errorf("LatestVersion = %q, want the concurrently refreshed 9.9.9", entry.LatestVersion)
	}
	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if got := root.AgentCheck("opencode").LatestVersion; got != "9.9.9" {
		t.Errorf("persisted LatestVersion = %q, want 9.9.9 (this process's write must be skipped)", got)
	}
}

// TestCheckAgentUpgradeSecondReadErrorDefers covers the root state becoming
// unreadable between the cached read and the post-fetch write: the step warns and
// defers rather than persisting over an unknown document.
func TestCheckAgentUpgradeSecondReadErrorDefers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		// The document becomes unreadable while the request is in flight.
		if err := os.WriteFile(rootStatePath, []byte("not: valid: yaml: ["), 0o600); err != nil {
			t.Fatalf("corrupt root state: %v", err)
		}
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui)
	if checked {
		t.Error("checkAgentUpgrade must defer when the root state becomes unreadable")
	}
	if entry != (state.AgentCheckState{}) {
		t.Errorf("entry = %+v, want the zero value on defer", entry)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read upgrade check state") {
		t.Errorf("expected a root-state read warning, got %v", ui.WarnCalls)
	}
}
