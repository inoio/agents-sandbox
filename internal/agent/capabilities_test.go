package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// stubUpgradeAgent is an Agent with an UpgradeChecker whose release lookup is
// fixed, so the shared resolver can be exercised without a network call.
type stubUpgradeAgent struct {
	latest string
	err    error
}

func (stubUpgradeAgent) Name() string               { return "stub-upgrade" }
func (stubUpgradeAgent) Experimental() bool         { return false }
func (stubUpgradeAgent) ConfigDirName() string      { return "stub-upgrade" }
func (stubUpgradeAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }
func (s stubUpgradeAgent) LatestVersion(context.Context) (string, error) {
	return s.latest, s.err
}
func (stubUpgradeAgent) NewerThan(_, _ string) (bool, error) { return false, nil }

// stubPlainAgent is an Agent implementing no optional capability.
type stubPlainAgent struct{}

func (stubPlainAgent) Name() string               { return "stub-plain" }
func (stubPlainAgent) Experimental() bool         { return false }
func (stubPlainAgent) ConfigDirName() string      { return "stub-plain" }
func (stubPlainAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// LatestVersion resolves the agent's newest release through its own
// UpgradeChecker, so every caller shares one resolution.
func TestLatestVersionResolvesThroughTheAgentsOwnChecker(t *testing.T) {
	got, err := agent.LatestVersion(context.Background(), stubUpgradeAgent{latest: "7.7.7"})
	if err != nil {
		t.Fatalf("LatestVersion error: %v", err)
	}
	if got != "7.7.7" {
		t.Errorf("LatestVersion = %q, want the checker's latest 7.7.7", got)
	}
}

// An agent without an UpgradeChecker has no release to resolve: the shared
// resolver reports an empty version and no error, so a caller can fall back
// instead of failing.
func TestLatestVersionWithoutUpgradeCheckerIsEmptyNotAnError(t *testing.T) {
	got, err := agent.LatestVersion(context.Background(), stubPlainAgent{})
	if err != nil {
		t.Fatalf("LatestVersion error: %v", err)
	}
	if got != "" {
		t.Errorf("LatestVersion for a non-checker = %q, want empty", got)
	}
}

// A failed release lookup is propagated unwrapped, so the caller decides how to
// tolerate it (the upgrade check warns and defers).
func TestLatestVersionPropagatesTheCheckerError(t *testing.T) {
	lookupErr := errors.New("offline")
	_, err := agent.LatestVersion(context.Background(), stubUpgradeAgent{err: lookupErr})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("LatestVersion error = %v, want it to wrap %v", err, lookupErr)
	}
}

// TestMergesSnippetName pins the shared predicate the managed write-back and
// the first-run host seed both consult: a caller is not second-guessed for an
// unknown agent, while a known merger's pattern is matched against the name's
// base so a path is reduced before matching. The registered no-merger case
// lives in capabilities_internal_test.go so its global registration is cleaned
// up again.
func TestMergesSnippetName(t *testing.T) {
	t.Run("unknown agent is not second-guessed", func(t *testing.T) {
		if !agent.MergesSnippetName("not-a-registered-agent", "anything.json") {
			t.Error("an unknown agent must report true")
		}
	})
	// The "agent without a config merger" branch registers a stub in the
	// process-global registry, so it lives in capabilities_internal_test.go
	// where that registration can be cleaned up again.
	t.Run("known merger matches its snippet pattern", func(t *testing.T) {
		if !agent.MergesSnippetName("claude-code", "settings.json") {
			t.Error("claudecode's settings.json must match its settings*.json* pattern")
		}
		if agent.MergesSnippetName("claude-code", "config.json") {
			t.Error("config.json must not match claudecode's settings*.json* pattern")
		}
		if !agent.MergesSnippetName("codex", "config.toml") {
			t.Error("codex's config.toml must match its config*.toml pattern")
		}
	})
	t.Run("a path is reduced to its base before matching", func(t *testing.T) {
		if !agent.MergesSnippetName("claude-code", "/home/dev/.claude/settings.local.json") {
			t.Error("the base name must be matched, not the whole path")
		}
	})
}

// opencode must expose a distinct WorktreeProvider capability, separate from
// DaemonProvider.
func TestOpencodeImplementsWorktreeProvider(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	if _, ok := agent.AsWorktreeProvider(a); !ok {
		t.Fatal("opencode should implement WorktreeProvider")
	}
	if _, ok := agent.AsDaemonProvider(a); !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
}

// The daemon agents declare the in-VM tools the launcher uses (checked at image
// build); agents that run no daemon declare none.
func TestGuestToolRequirer(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		requirer, ok := agent.AsGuestToolRequirer(a)
		wantDaemon := name == "opencode" || name == "opencode2"
		if ok != wantDaemon {
			t.Errorf("%s: AsGuestToolRequirer = %v, want %v", name, ok, wantDaemon)
			continue
		}
		if !wantDaemon {
			continue
		}
		want := []string{"curl"}
		if got := requirer.GuestTools(); !slices.Equal(got, want) {
			t.Errorf("%s: GuestTools() = %v, want %v", name, got, want)
		}
	}
}

func TestAgentEventStreamProviders(t *testing.T) {
	for _, name := range agent.Names() {
		a, _ := agent.Lookup(name)
		_, ok := agent.AsEventStreamProvider(a)
		want := name == "opencode" || name == "opencode2"
		if ok != want {
			t.Errorf("%s: AsEventStreamProvider = %v, want %v", name, ok, want)
		}
	}
}

// A session-list command without a parser cannot seed anything, and a nil
// parser used to make the notify watcher silently skip seeding. The pair is
// therefore built through NewSessionList, which rejects an incomplete one.
func TestNewSessionListRejectsIncompletePairs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		parse   func([]byte) []agent.SessionRef
	}{
		{name: "empty command", command: "", parse: func([]byte) []agent.SessionRef { return nil }},
		{name: "nil parser", command: "cmd", parse: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewSessionList did not panic on an incomplete pair")
				}
			}()
			agent.NewSessionList(tt.command, tt.parse)
		})
	}
}

func TestNewSessionListKeepsCommandAndParser(t *testing.T) {
	got := agent.NewSessionList("cmd", func(payload []byte) []agent.SessionRef {
		return []agent.SessionRef{{ID: "child:" + string(payload)}}
	})
	if got.Command() != "cmd" {
		t.Errorf("Command() = %q, want %q", got.Command(), "cmd")
	}
	if children := got.Parse([]byte("raw")); len(children) != 1 || children[0].ID != "child:raw" {
		t.Errorf("Parse() = %v, want [child:raw]", children)
	}
}
