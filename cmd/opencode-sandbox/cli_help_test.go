package main

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/testutil"
)

func TestRootHelpListsAvailableCommands(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	out := ui.StdOutBuffer.String()

	if !strings.Contains(out, "Available Commands:") {
		t.Fatalf("expected 'Available Commands:' section in root help output:\n%s", out)
	}
	for _, cmd := range []string{"build", "doctor", "kill", "list", "prune", "run", "shell", "stop", "version"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected root help output to list %q command:\n%s", cmd, out)
		}
	}
}
