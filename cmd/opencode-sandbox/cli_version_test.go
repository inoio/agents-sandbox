package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestVersion(t *testing.T) {
	t.Run("version command exists", func(t *testing.T) {
		versionCmd, _ := setupVersionTestFixtures(t)

		if versionCmd == nil {
			t.Fatal("expected version command to be found")
		}
	})

	t.Run("default version is dev", func(t *testing.T) {
		orig := version
		t.Cleanup(func() { version = orig })
		version = "dev"

		versionCmd, testUI := setupVersionTestFixtures(t)

		versionCmd.Run(versionCmd, nil)

		if len(testUI.OutCalls) == 0 {
			t.Fatal("expected at least one OutCall from version command")
		}
		if !strings.Contains(testUI.OutCalls[0], "opencode-sandbox dev") {
			t.Errorf("expected version output to contain %q, got %q", "opencode-sandbox dev", testUI.OutCalls[0])
		}
	})

	t.Run("custom version is displayed correctly", func(t *testing.T) {
		orig := version
		t.Cleanup(func() { version = orig })
		version = "1.2.3"

		versionCmd, testUI := setupVersionTestFixtures(t)

		versionCmd.Run(versionCmd, nil)

		if len(testUI.OutCalls) == 0 {
			t.Fatal("expected at least one OutCall from version command")
		}
		if !strings.Contains(testUI.OutCalls[0], "opencode-sandbox 1.2.3") {
			t.Errorf("expected version output to contain %q, got %q", "opencode-sandbox 1.2.3", testUI.OutCalls[0])
		}
	})
}

// setupVersionTestFixtures builds the root command with a fresh mock UI and
// returns the version subcommand plus the UI. The UI is returned by pointer so
// that OutCalls is observed against the exact instance the command writes to.
func setupVersionTestFixtures(t *testing.T) (*cobra.Command, *termio.Mock) {
	t.Helper()
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	versionCmd, _, _ := root.Find([]string{"version"})
	return versionCmd, &testUI
}
