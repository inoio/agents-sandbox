package vm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
// given baked agent version.
func withExistingAgentVersion(t *testing.T, version string) {
	t.Helper()
	orig := existingAgentVersion
	existingAgentVersion = func(_ context.Context, _, _ string) (string, bool) { return version, true }
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
	if err := state.WriteState(k, *hs); err != nil {
		t.Fatalf("WriteState: %v", err)
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

	got, upgraded, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{},
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

	got, upgraded, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		&termio.Mock{},
		options.RunOptions{},
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

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	// No recorded baseline (as after the updater.yaml migration), but the
	// project's existing image carries it.
	withExistingAgentVersion(t, "1.0.0")

	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, options.RunOptions{})
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("resolveBuildVersion = %q, want recovered baseline 1.0.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for a keep choice")
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

func TestResolveOpenCodeVersionRecoveredBaselineReusedOffline(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("offline")
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	withExistingAgentVersion(t, "1.0.0")

	got, upgraded, err := resolveBuildVersion(
		context.Background(), k, opencodeAgent(t), &termio.Mock{}, options.RunOptions{},
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

func TestResolveOpenCodeVersionNoNewerVersion(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "2.0.0", agentSourceTool)
	opts := options.RunOptions{}
	ui := &termio.Mock{}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "r", nil
		},
	}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatalf("WriteRootState: %v", err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	if err := state.WriteState(k, *hs); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatal(err)
	}

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	if _, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts); err != nil {
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

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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

	opts := options.RunOptions{}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	opts := options.RunOptions{}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "q", nil
		},
	}

	_, _, err := resolveBuildVersion(context.Background(), k, opencodeAgent(t), ui, opts)
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
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatal(err)
	}
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) { return "2.0.0", nil }
	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}
	if _, _, err := resolveBuildVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{},
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

	release, ok, err := state.LockRootState(true)
	if err != nil || !ok {
		t.Fatalf("LockRootState(blocking) = ok %v, err %v", ok, err)
	}
	defer release()

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
	if err := state.WriteState(k, *hs); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}}); err != nil {
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

// TestDetermineAgentVersionDryRunPrefersPin pins the --dry-run contract: an
// explicit pin wins, resolveBuildVersion is not consulted (agentLatestVersion is
// never called), and the dry-run verbose is emitted.
func TestDetermineAgentVersionDryRunPrefersPin(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		t.Error("agentLatestVersion must not be called under --dry-run")
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{}
	got, upgrade, err := determineAgentVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{DryRun: true, AgentVersion: "3.0.0"},
	)
	if err != nil {
		t.Fatalf("determineAgentVersion: %v", err)
	}
	if got != "3.0.0" || upgrade {
		t.Errorf("determineAgentVersion = (%q, %v), want (3.0.0, false)", got, upgrade)
	}
	if !slices.Contains(ui.VerboseCalls, "dry-run: would resolve build version") {
		t.Errorf("expected dry-run verbose, got %v", ui.VerboseCalls)
	}
}

// TestDetermineAgentVersionDryRunFallsBackToRecordedVersion pins the --dry-run
// fallback: without a pin the recorded project version is used, no prompt fires,
// and the network is never touched.
func TestDetermineAgentVersionDryRunFallsBackToRecordedVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", agentSourceTool)

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		t.Error("agentLatestVersion must not be called under --dry-run")
		return "2.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			t.Error("--dry-run must not prompt")
			return "", nil
		},
	}
	got, upgrade, err := determineAgentVersion(
		context.Background(),
		k,
		opencodeAgent(t),
		ui,
		options.RunOptions{DryRun: true},
	)
	if err != nil {
		t.Fatalf("determineAgentVersion: %v", err)
	}
	if got != "1.0.0" || upgrade {
		t.Errorf("determineAgentVersion = (%q, %v), want (1.0.0, false)", got, upgrade)
	}
	if !slices.Contains(ui.VerboseCalls, "dry-run: would resolve build version") {
		t.Errorf("expected dry-run verbose, got %v", ui.VerboseCalls)
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
	if err := state.WriteRootState(state.RootState{
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
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}); err != nil {
		t.Fatal(err)
	}
	stateDir := configpaths.Get().UserStateDir()
	if err := os.WriteFile(filepath.Join(stateDir, "root.lock"), nil, 0o600); err != nil {
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
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}); err != nil {
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
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read project state") {
		t.Errorf("expected a project-state read warning, got %v", ui.WarnCalls)
	}
}

// TestPendingUpgradeOffersWhenProjectStateMissing covers the ErrStateNotFound
// arm: with no project state file the offered set is empty, so the version is
// offered and persisted into a fresh HomeState.
func TestPendingUpgradeOffersWhenProjectStateMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}); err != nil {
		t.Fatal(err)
	}

	latest, offer := pendingUpgrade(context.Background(), opencodeAgent(t), k, &termio.Mock{}, "1.0.0")
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !hs.Offered("2.0.0") {
		t.Errorf("offered not persisted to a fresh HomeState: %+v", hs)
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
	if err := state.WriteRootState(state.RootState{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}); err != nil {
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
	if !strings.Contains(joined, "could not persist project state") {
		t.Errorf("expected a project-state persist warning, got %q", joined)
	}
}
