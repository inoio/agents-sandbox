package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestFirstRunMenuNoopWhenLearnAlreadySet pins that an explicit --learn is left
// alone: the menu must not override the user's flag, so it returns before any
// config or host probing.
func TestFirstRunMenuNoopWhenLearnAlreadySet(t *testing.T) {
	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		t.Error("Select must not be called when --learn is already set")
		return "", nil
	}

	err := firstRunMenu(ui, &options.RunOptions{Learn: true}, "widget-abc", "pi")

	require.NoError(t, err)
}

// TestFirstRunMenuReportsHostHomeResolutionFailure pins that a host home the
// resolver cannot produce is surfaced rather than silently skipping setup, and
// its cause stays reachable through the wrapper.
func TestFirstRunMenuReportsHostHomeResolutionFailure(t *testing.T) {
	homeErr := errors.New("no home")
	configpathstest.WithMockConfigPathsHomeError(t, homeErr)

	err := firstRunMenu(&termiotest.Mock{}, &options.RunOptions{}, "widget-abc", "pi")

	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve the host home")
	require.ErrorIs(t, err, homeErr, "the host-home cause must stay reachable through the wrapper")
}

// TestFirstRunMenuReportsBuildFactsError pins that a failed fact assembly (an
// unknown agent name) reaches the caller, so offerFirstRunMenu can warn.
func TestFirstRunMenuReportsBuildFactsError(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	err := firstRunMenu(&termiotest.Mock{}, &options.RunOptions{}, "widget-abc", "not-a-registered-agent")

	require.Error(t, err)
}

// TestFirstRunMenuWarnsWhenSeedingIsIncomplete pins the host-seed failure path:
// the choice is still applied, but a seed the agent cannot sanitize is surfaced
// as a warning rather than blocking the run.
func TestFirstRunMenuWarnsWhenSeedingIsIncomplete(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeFirstRunHostConfig(t, "hermes", "config.yaml", "model:\n  provider: moonshot\n  api_key: sk-inline\n")

	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "learn-from-host", nil
	}

	err := firstRunMenu(ui, &options.RunOptions{}, "widget-abc", "hermes")

	require.NoError(t, err)
	require.True(
		t,
		containsSubstring(ui.WarnCalls, "seeding from your host config was incomplete"),
		"warnings = %v, want the incomplete seed surfaced",
		ui.WarnCalls,
	)
}

// TestFirstRunMenuLogsSeededSecrets pins the successful host-seed path: the
// count of migrated secrets is reported, so the user can see the seed worked.
func TestFirstRunMenuLogsSeededSecrets(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeFirstRunHostConfig(
		t,
		"hermes",
		"config.yaml",
		"model:\n  provider: moonshot\n  api_key: sk-inline\n  base_url: https://api.custom.example/v1\n",
	)

	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "learn-from-host", nil
	}

	err := firstRunMenu(ui, &options.RunOptions{}, "widget-abc", "hermes")

	require.NoError(t, err)
	require.True(
		t,
		containsSubstring(ui.InfoCalls, "seeded 1 secret"),
		"info messages = %v, want the seeded-secret count reported",
		ui.InfoCalls,
	)
}

// TestFirstRunMenuDoesNotRecordWhenNotOffered pins that a menu the user was not
// shown leaves no decision behind: a non-interactive run that skips the prompt
// must not record a first-run choice (which would suppress a future prompt).
func TestFirstRunMenuDoesNotRecordWhenNotOffered(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	err := firstRunMenu(&termiotest.Mock{}, &options.RunOptions{}, "widget-abc", "pi")

	require.NoError(t, err)
	_, ok, readErr := state.ReadFirstRun("widget-abc", "pi")
	require.NoError(t, readErr)
	require.False(t, ok, "a menu that was not offered must not record a decision")
}

// TestFirstRunMenuDoesNotLogAZeroSeed pins that the seeded-secret count is
// reported only when a secret was migrated: a clean host config seeds zero, so
// no "seeded" note is printed.
func TestFirstRunMenuDoesNotLogAZeroSeed(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeFirstRunHostConfig(t, "hermes", "config.yaml", "model:\n  provider: moonshot\n")

	ui := &termiotest.Mock{IsInteractiveResult: true}
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		return "learn-from-host", nil
	}

	err := firstRunMenu(ui, &options.RunOptions{}, "widget-abc", "hermes")

	require.NoError(t, err)
	require.False(
		t,
		containsSubstring(ui.InfoCalls, "seeded"),
		"info messages = %v, want no seeded-secret note for a zero seed",
		ui.InfoCalls,
	)
}

// TestOfferFirstRunMenuWarnsOnMenuError pins that a first-run setup error guides
// the run without blocking it: offerFirstRunMenu surfaces it as a warning.
func TestOfferFirstRunMenuWarnsOnMenuError(t *testing.T) {
	original := firstRunMenu
	firstRunMenu = func(termio.UI, *options.RunOptions, string, string) error {
		return errors.New("boom")
	}
	t.Cleanup(func() { firstRunMenu = original })

	ui := &termiotest.Mock{}

	offerFirstRunMenu(ui, &options.RunOptions{}, options.ModeRun)

	require.True(
		t,
		containsSubstring(ui.WarnCalls, "first-run setup"),
		"warnings = %v, want the menu error surfaced",
		ui.WarnCalls,
	)
}

// writeFirstRunHostConfig writes an agent host config under the mock host home.
func writeFirstRunHostConfig(t *testing.T, agentDir, name, content string) {
	t.Helper()
	hostHome, err := configpaths.Get().UserHomeDir()
	require.NoError(t, err)
	dir := filepath.Join(hostHome, "."+agentDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func containsSubstring(haystack []string, needle string) bool {
	return strings.Contains(strings.Join(haystack, "\n"), needle)
}
