package prepare

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
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestAgentLatestVersionForSeamResolution pins the seam contract: a nil
// options.Seams.AgentLatestVersion uses the production default
// (agentLatestVersion), while a set field is used as-is.
func TestAgentLatestVersionForSeamResolution(t *testing.T) {
	orig := agentLatestVersion
	t.Cleanup(func() { agentLatestVersion = orig })
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) { return "default", nil }

	if got, _ := agentLatestVersionFor(options.Seams{})(context.Background(), opencodeAgent(t)); got != "default" {
		t.Errorf("nil seam version = %q, want the production default %q", got, "default")
	}

	seam := func(context.Context, agent.Agent) (string, error) { return "seam", nil }
	if got, _ := agentLatestVersionFor(options.Seams{AgentLatestVersion: seam})(
		context.Background(), opencodeAgent(t),
	); got != "seam" {
		t.Errorf("set seam version = %q, want %q", got, "seam")
	}
}

// TestAgentLatestVersionAsksOnlyUpgradeCheckers pins that the latest release
// comes from the agent's upgrade checker, and an agent without one reports none.
func TestAgentLatestVersionAsksOnlyUpgradeCheckers(t *testing.T) {
	cases := []struct {
		name  string
		agent agent.Agent
		want  string
	}{
		{name: "upgrade checker", agent: upgradeCheckerAgent{}, want: "9.9.9"},
		{name: "no upgrade checker", agent: &fakeAgent{}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := agentLatestVersion(context.Background(), tc.agent)
			if err != nil {
				t.Fatalf("agentLatestVersion: %v", err)
			}
			if got != tc.want {
				t.Errorf("agentLatestVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgentVersionResolverForSeamResolution pins the resolver contract: a nil
// seam uses the shared buildVersionResolver default, a set seam resolves the
// latest version through it (unless a version is requested), and a requested
// version always wins.
func TestAgentVersionResolverForSeamResolution(t *testing.T) {
	orig := buildVersionResolver
	t.Cleanup(func() { buildVersionResolver = orig })
	buildVersionResolver = func(context.Context, agent.Agent, string) (string, error) { return "default", nil }

	if got, _ := agentVersionResolverFor(options.Seams{})(
		context.Background(), opencodeAgent(t), "",
	); got != "default" {
		t.Errorf("nil seam resolver = %q, want the default %q", got, "default")
	}

	seam := options.Seams{AgentLatestVersion: func(context.Context, agent.Agent) (string, error) {
		return "seam", nil
	}}
	if got, _ := agentVersionResolverFor(seam)(context.Background(), opencodeAgent(t), ""); got != "seam" {
		t.Errorf("set seam resolver = %q, want %q", got, "seam")
	}
	if got, _ := agentVersionResolverFor(seam)(context.Background(), opencodeAgent(t), "pinned"); got != "pinned" {
		t.Errorf("requested version resolver = %q, want the pin %q", got, "pinned")
	}
}

// TestBuildVersionResolverPrefersRequestedVersion pins the prepare-injected
// resolver's contract: a non-empty requested version is returned as-is without
// consulting the shared latest-version lookup, and an empty request falls back
// to it. A mutated guard that ignored the pin would rebuild with "latest".
func TestBuildVersionResolverPrefersRequestedVersion(t *testing.T) {
	withMockAgentLatestVersion(t, "2.0.0")
	a := opencodeAgent(t)

	got, err := buildVersionResolver(context.Background(), a, "1.2.3")
	if err != nil {
		t.Fatalf("buildVersionResolver(requested=1.2.3): %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("buildVersionResolver(requested=1.2.3) = %q, want the requested 1.2.3", got)
	}

	got, err = buildVersionResolver(context.Background(), a, "")
	if err != nil {
		t.Fatalf("buildVersionResolver(requested=\"\"): %v", err)
	}
	if got != "2.0.0" {
		t.Errorf("buildVersionResolver(requested=\"\") = %q, want the latest 2.0.0", got)
	}
}

// TestResolveBuildVersionNoRebuildSkipsUpgradeCheck pins that --no-rebuild never
// runs the update check: accepting an upgrade would rebuild the image the user
// asked to keep. The version stays the one the existing image reports.
func TestResolveBuildVersionNoRebuildSkipsUpgradeCheck(t *testing.T) {
	origLatest := agentLatestVersion
	t.Cleanup(func() { agentLatestVersion = origLatest })
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) {
		t.Fatal("--no-rebuild must not run the update check")
		return "", nil
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
	withExistingAgentVersion(t)

	got, upgraded, offered, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode", NoRebuild: true},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.0.0" || upgraded || offered != "" {
		t.Errorf("resolveBuildVersion = (%q, %v, %q), want (\"1.0.0\", false, \"\")", got, upgraded, offered)
	}
}

// TestResolveBuildVersionAppliesTheUpgradePromptAnswer pins what each answer
// to the interactive upgrade prompt does: rebuild takes the newer release,
// keep and an unrecognized answer stay on the recorded one, quit aborts the
// session, and a failing prompt is reported.
func TestResolveBuildVersionAppliesTheUpgradePromptAnswer(t *testing.T) {
	selectErr := errors.New("selection failed")
	cases := []struct {
		name         string
		answer       string
		answerErr    error
		wantVersion  string
		wantUpgraded bool
		wantErr      error
	}{
		{name: "rebuild", answer: "r", wantVersion: "2.0.0", wantUpgraded: true},
		{name: "keep", answer: "k", wantVersion: "1.0.0"},
		{name: "unrecognized answer keeps", answer: "unknown", wantVersion: "1.0.0"},
		{name: "quit", answer: "q", wantErr: errUpgradeQuit},
		{name: "prompt fails", answerErr: selectErr, wantErr: selectErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			withMockAgentLatestVersion(t, "2.0.0")
			k := state.Key{Slug: "proj", Agent: "opencode"}
			seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn: func(string, []termio.Choice, string) (string, error) {
					return tc.answer, tc.answerErr
				},
			}

			got, upgraded, _, err := resolveBuildVersion(
				context.Background(), docker.Get(), k, opencodeResolved(t), ui, options.RunOptions{Agent: "opencode"},
			)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBuildVersion: %v", err)
			}
			if got != tc.wantVersion || upgraded != tc.wantUpgraded {
				t.Errorf("resolveBuildVersion = (%q, upgraded=%v), want (%q, upgraded=%v)",
					got, upgraded, tc.wantVersion, tc.wantUpgraded)
			}
		})
	}
}

// TestResolveBuildVersionWithoutUpgradeChecker pins that an agent without an
// upgrade checker reuses the recorded version without prompting.
func TestResolveBuildVersionWithoutUpgradeChecker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "fake"}
	seedAgentState(t, k, "1.5.0", state.AgentSourceTool)
	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		agent.Resolve(&fakeAgent{}),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.5.0" {
		t.Errorf("resolveBuildVersion = %q, want recorded version 1.5.0", got)
	}
	if upgraded {
		t.Error("expected upgraded=false for an agent without an upgrade checker")
	}
}

// TestResolveBuildVersionWithoutUpgradeCheckerIgnoresImageLabel pins the
// no-checker guard's purpose: the recorded version is reused verbatim, never
// reconciled against the existing image label. Reconciliation is the
// upgrade-check path's job, and a checker-less agent must not silently adopt a
// version the image reports (which would defeat the stable image identity the
// guard preserves).
func TestResolveBuildVersionWithoutUpgradeCheckerIgnoresImageLabel(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "fake"}
	seedAgentState(t, k, "1.5.0", state.AgentSourceTool)
	withExistingAgentVersionValue(t, "2.0.0")

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		agent.Resolve(&fakeAgent{}),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
	)
	if err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if got != "1.5.0" {
		t.Errorf(
			"resolveBuildVersion = %q, want the recorded 1.5.0 (a checker-less agent must not adopt the image's 2.0.0)",
			got,
		)
	}
	if upgraded {
		t.Error("expected upgraded=false for an agent without an upgrade checker")
	}
}

func TestResolveBuildVersionSkipsCheckForUserAgentSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "9.9.9", state.AgentSourceUser)

	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k,
		opencodeResolved(t),
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
	seedAgentState(t, k, "", state.AgentSourceUser)

	var latestCalled bool
	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "10.0.0", nil
	}
	t.Cleanup(func() { agentLatestVersion = origLatest })

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k,
		opencodeResolved(t),
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

func TestResolveBuildVersionPinnedSkipsUpdateCheck(t *testing.T) {
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

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionRebuildStillChecksUpdate(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
	opts := options.RunOptions{Rebuild: true}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionUserProvidedWinsOverPin(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	latestCalled := false
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		latestCalled = true
		return "2.0.0", nil
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceUser)
	opts := options.RunOptions{AgentVersion: "9.9.9"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionNoBaseline(t *testing.T) {
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

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionRecoversBaselineFromExistingImage(t *testing.T) {
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

	got, upgraded, offered, err := resolveBuildVersion(context.Background(), docker.Get(), k,
		opencodeResolved(t),
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

func TestResolveBuildVersionRecoveryDoesNotCreatePartialState(t *testing.T) {
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
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
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

func TestResolveBuildVersionRecoveredBaselineReusedOffline(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("offline")
	}

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	withExistingAgentVersion(t)

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
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
	seedAgentState(t, k, "0.9.0", state.AgentSourceTool) // stale: the image holds 1.0.0
	withExistingAgentVersion(t)                          // seam returns "1.0.0", true

	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		&termio.Mock{},
		options.RunOptions{Agent: "opencode"},
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
	seedAgentState(t, k, "0.9.0", state.AgentSourceTool)
	withExistingAgentVersion(t) // image reports 1.0.0

	got, _, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), &termio.Mock{},
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
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool) // no withExistingAgentVersion: seam defaults to absent

	ui := &termio.Mock{SelectFn: func(string, []termio.Choice, string) (string, error) { return "k", nil }}
	got, upgraded, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		ui,
		options.RunOptions{Agent: "opencode"},
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

func TestResolveBuildVersionRecoveryPersistFailureStillUsesVersion(t *testing.T) {
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
	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k,
		opencodeResolved(t),
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

func TestResolveBuildVersionNoNewerVersion(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "2.0.0", state.AgentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionNonInteractiveLogsUpgradeAvailable(t *testing.T) {
	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: false}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionSkipsCheckWhenCheckedToday(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionDoesNotReOfferVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionRecordsOfferedBeforePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	if _, _, _, err := resolveBuildVersion(
		context.Background(),
		docker.Get(),
		k,
		opencodeResolved(t),
		ui,
		opts,
	); err != nil {
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

func TestResolveBuildVersionUnparsableLatestKeepsCurrent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	// An unparsable latest version must never fail the session or offer an upgrade.
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "not-a-version", nil }

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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

func TestResolveBuildVersionOfflineDoesNotUpdateLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)

	origLatest := agentLatestVersion
	defer func() { agentLatestVersion = origLatest }()
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) {
		return "", errors.New("network unreachable")
	}

	opts := options.RunOptions{Agent: "opencode"}
	ui := &termio.Mock{IsInteractiveResult: true}

	got, upgraded, _, err := resolveBuildVersion(context.Background(), docker.Get(), k, opencodeResolved(t), ui, opts)
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
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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
	if _, _, _, err := resolveBuildVersion(context.Background(), docker.Get(), k,
		opencodeResolved(t),
		ui,
		options.RunOptions{Agent: "opencode"},
	); err != nil {
		t.Fatalf("resolveBuildVersion: %v", err)
	}
	if !strings.Contains(prompt, "1.0.0") || !strings.Contains(prompt, "2.0.0") {
		t.Errorf("prompt %q must name both 1.0.0 and 2.0.0", prompt)
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
			source:           state.AgentSourceUser,
			recordedVersion:  "1.0.0",
			requestedVersion: "9.9.9",
			wantVersion:      "1.0.0",
			wantUserProvided: true,
		},
		{
			name:             "user-provided agent without a recorded version leaves resolution to the build",
			source:           state.AgentSourceUser,
			recordedVersion:  "",
			requestedVersion: "9.9.9",
			wantVersion:      "",
			wantUserProvided: true,
		},
		{
			name:             "tool-managed agent honors the pin",
			source:           state.AgentSourceTool,
			recordedVersion:  "1.0.0",
			requestedVersion: "9.9.9",
			wantVersion:      "9.9.9",
			wantUserProvided: false,
		},
		{
			name:             "tool-managed agent without a pin passes the request through",
			source:           state.AgentSourceTool,
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

// TestPendingUpgradeWithoutUpgradeChecker pins that an agent without an upgrade
// checker is never offered an upgrade, even when a newer release is cached for
// its name (there is no checker to compare versions with).
func TestPendingUpgradeWithoutUpgradeChecker(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "fake"}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		(&fakeAgent{}).Name(): {LastChecked: now().Add(-time.Hour), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	latest, offer := pendingUpgrade(
		context.Background(), agent.Resolve(&fakeAgent{}), k, &termio.Mock{}, "1.0.0", agentLatestVersion,
	)
	if offer {
		t.Errorf("expected no upgrade offer for an agent without a checker, got latest=%q", latest)
	}
}

// TestPendingUpgradeLockHeldDefers pins that a held root-state lock (e.g. an
// in-flight runtime-home copy campaign) defers the whole upgrade step to the
// next run instead of racing the other writer.
func TestPendingUpgradeLockHeldDefers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)

	ok, err := state.WithRootState(true, func(*state.RootTxn) error {
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
				t.Error("must not prompt while the root-state lock is held")
				return "r", nil
			},
		}
		latest, offer := pendingUpgrade(context.Background(), opencodeResolved(t), k, ui, "1.0.0", agentLatestVersion)
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
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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
		opencodeResolved(t),
		k,
		&termio.Mock{IsInteractiveResult: true},
		"1.0.0",
		agentLatestVersion,
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
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatalf("seedRootState: %v", err)
	}

	latest, offer := pendingUpgrade(
		context.Background(),
		opencodeResolved(t),
		k,
		&termio.Mock{},
		"1.0.0",
		agentLatestVersion,
	)
	if !offer || latest != "2.0.0" {
		t.Fatalf("first pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}

	latest, offer = pendingUpgrade(
		context.Background(),
		opencodeResolved(t),
		k,
		&termio.Mock{},
		"1.0.0",
		agentLatestVersion,
	)
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
		AgentSource:  state.AgentSourceTool,
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

	latest, offer := pendingUpgrade(
		context.Background(),
		opencodeResolved(t),
		k,
		&termio.Mock{},
		"1.0.0",
		agentLatestVersion,
	)
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
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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
	latest, offer := pendingUpgrade(context.Background(), opencodeResolved(t), k, ui, "1.0.0", agentLatestVersion)
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

// TestPendingUpgradeWarnsOnRootStateWriteError pins that a failing root-state
// write (a read-only state directory) is a warning, and the offer still
// proceeds from the freshly fetched latest version.
func TestPendingUpgradeWarnsOnRootStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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
	latest, offer := pendingUpgrade(context.Background(), opencodeResolved(t), k, ui, "1.0.0", agentLatestVersion)
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not persist upgrade check state") {
		t.Errorf("expected a root-state persist warning, got %v", ui.WarnCalls)
	}
}

// TestPendingUpgradeNoOfferWhenLatestEmpty pins that a successful check that
// yields no version never offers.
func TestPendingUpgradeNoOfferWhenLatestEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)

	origLatest := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "", nil }
	t.Cleanup(func() { agentLatestVersion = origLatest })

	latest, offer := pendingUpgrade(
		context.Background(),
		opencodeResolved(t),
		k,
		&termio.Mock{},
		"1.0.0",
		agentLatestVersion,
	)
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer for an empty latest", latest, offer)
	}
}

// TestPendingUpgradeWarnsOnProjectStateReadError pins that an unreadable
// project state (a directory at its path) is a warning and offers nothing.
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
	latest, offer := pendingUpgrade(context.Background(), opencodeResolved(t), k, ui, "1.0.0", agentLatestVersion)
	if offer || latest != "" {
		t.Errorf("pendingUpgrade = (%q, %v), want no offer on a project-state read error", latest, offer)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, " "), "could not read or persist project state") {
		t.Errorf("expected a project-state read warning, got %v", ui.WarnCalls)
	}
}

// TestPendingUpgradeOffersWhenProjectStateMissing pins that a project without
// state is offered the version but nothing is persisted: creating a state
// before home resolution would leave a fingerprint-less partial state that
// poisons home recovery.
func TestPendingUpgradeOffersWhenProjectStateMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now(), LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}

	latest, offer := pendingUpgrade(
		context.Background(),
		opencodeResolved(t),
		k,
		&termio.Mock{},
		"1.0.0",
		agentLatestVersion,
	)
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	if _, err := state.ReadState(k); !errors.Is(err, state.ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no partial state written)", err)
	}
}

// TestPendingUpgradeWarnsOnProjectStateWriteError pins that a failing
// project-state write (a read-only key directory) is a warning and the offer is
// still returned.
func TestPendingUpgradeWarnsOnProjectStateWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	seedAgentState(t, k, "1.0.0", state.AgentSourceTool)
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
	latest, offer := pendingUpgrade(context.Background(), opencodeResolved(t), k, ui, "1.0.0", agentLatestVersion)
	if !offer || latest != "2.0.0" {
		t.Fatalf("pendingUpgrade = (%q, %v), want (2.0.0, true)", latest, offer)
	}
	joined := strings.Join(ui.WarnCalls, " ")
	if !strings.Contains(joined, "could not read or persist project state") {
		t.Errorf("expected a project-state persist warning, got %q", joined)
	}
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

	entry, checked := checkAgentUpgrade(
		context.Background(),
		opencodeAgent(t),
		true,
		&termio.Mock{},
		agentLatestVersion,
	)
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
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui, agentLatestVersion)
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
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui, agentLatestVersion)
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

	entry, checked := checkAgentUpgrade(
		context.Background(),
		opencodeAgent(t),
		false,
		&termio.Mock{},
		agentLatestVersion,
	)
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

	entry, checked := checkAgentUpgrade(
		context.Background(),
		opencodeAgent(t),
		true,
		&termio.Mock{},
		agentLatestVersion,
	)
	if checked {
		t.Error("checkAgentUpgrade must defer when the write transaction is contended")
	}
	if entry.LatestVersion != "2.0.0" {
		t.Errorf("entry.LatestVersion = %q, want the freshly fetched 2.0.0", entry.LatestVersion)
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

	entry, checked := checkAgentUpgrade(
		context.Background(),
		opencodeAgent(t),
		true,
		&termio.Mock{},
		agentLatestVersion,
	)
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

	entry, checked := checkAgentUpgrade(
		context.Background(),
		opencodeAgent(t),
		true,
		&termio.Mock{},
		agentLatestVersion,
	)
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

// TestCheckAgentUpgradeSecondReadErrorDefers pins that a root state that
// becomes unreadable between the cached read and the post-fetch write is warned
// about and deferred, rather than overwritten.
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
	entry, checked := checkAgentUpgrade(context.Background(), opencodeAgent(t), true, ui, agentLatestVersion)
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

// TestCurrentAgentSourceReadsTheRecordedSource pins that the recorded agent
// source is returned, and nothing for a project without state.
func TestCurrentAgentSourceReadsTheRecordedSource(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentSource(k); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty", got)
	}
	if err := seedHomeState(k, state.HomeState{AgentSource: state.AgentSourceUser}); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentSource(k); got != state.AgentSourceUser {
		t.Errorf("currentAgentSource() = %q, want user", got)
	}
}

// TestCurrentAgentVersionAndSourceAreEmptyWhenStateCannotBeRead pins that an
// unreadable project state counts as "nothing recorded" rather than an error.
func TestCurrentAgentVersionAndSourceAreEmptyWhenStateCannotBeRead(t *testing.T) {
	k := state.Key{Slug: "proj", Agent: "opencode"}
	unreadableStates := map[string]func(t *testing.T){
		"corrupt state file": func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			stateDir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(stateDir, "state.yaml"),
				[]byte("::: not yaml :::"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
		"state directory cannot be read": withFailingStateDir,
	}
	for name, arrange := range unreadableStates {
		t.Run(name, func(t *testing.T) {
			arrange(t)
			if got := currentAgentVersion(k); got != "" {
				t.Errorf("currentAgentVersion() = %q, want empty", got)
			}
			if got := currentAgentSource(k); got != "" {
				t.Errorf("currentAgentSource() = %q, want empty", got)
			}
		})
	}
}

// withFailingStateDir installs a ConfigPaths whose UserStateDir is a regular
// file, so any attempt to read or write <stateDir>/... fails.
func withFailingStateDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "userstate")
	if err := os.WriteFile(stateFile, []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := configpaths.Get
	configpaths.Get = func() configpaths.ConfigPaths {
		return failingStateDirConfigPaths{stateDir: stateFile}
	}
	t.Cleanup(func() { configpaths.Get = orig })
}

// dueRootState seeds a root document whose opencode check is due (older than the
// once-per-day window), so checkAgentUpgrade would run the version request.
func dueRootState() state.RootState {
	return state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: now().Add(-25 * time.Hour), LatestVersion: "1.0.0"},
	}}}
}

// withExistingAgentVersion makes the project's existing runner image report the
// fixture baseline version.
func withExistingAgentVersion(t *testing.T) {
	t.Helper()
	withExistingAgentVersionValue(t, "1.0.0")
}

// withExistingAgentVersionValue makes the project's existing runner image report
// version, restoring the seam afterwards.
func withExistingAgentVersionValue(t *testing.T, version string) {
	t.Helper()
	orig := existingAgentVersion
	existingAgentVersion = func(_ context.Context, _ docker.Client, _, _ string) (string, bool) { return version, true }
	t.Cleanup(func() { existingAgentVersion = orig })
}
