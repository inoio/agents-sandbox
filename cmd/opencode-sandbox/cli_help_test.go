package main

import (
	"strings"
	"testing"
)

func commandOut(t *testing.T, args ...string) string {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, args...)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute '%v': %v", strings.Join(args, " "), err)
	}
	return ui.StdOutBuffer.String()
}

func TestRootHelpListsAvailableCommands(t *testing.T) {
	out := commandOut(t, "--help")

	if !strings.Contains(out, "Available Commands:") {
		t.Fatalf("expected 'Available Commands:' section in root help output:\n%s", out)
	}
	for _, cmd := range []string{"build", "doctor", "kill", "list", "prune", "run", "shell", "stop", "version"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected root help output to list %q command:\n%s", cmd, out)
		}
	}
}

func TestRootHelpListsGroupCommands(t *testing.T) {
	out := commandOut(t, "--help")

	for _, cmd := range []string{"config", "image", "sandbox", "volume"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected root help output to list group command %q:\n%s", cmd, out)
		}
	}
}

func TestRootHelpDescribesImpliedRun(t *testing.T) {
	out := commandOut(t, "--help")

	if !strings.Contains(out, `When invoked without a subcommand, the "run" command is implied.`) {
		t.Errorf("expected root help to explain the implied run command:\n%s", out)
	}
}

func TestRootHelpListsGlobalFlags(t *testing.T) {
	out := commandOut(t, "--help")

	for _, flag := range []string{"--yes", "--quiet", "--log-level", "--dry-run"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected root help to list flag %q:\n%s", flag, out)
		}
	}
}

func TestEveryLeafCommandHelpRenders(t *testing.T) {
	for _, path := range [][]string{
		{"run"},
		{"shell"},
		{"stop"},
		{"kill"},
		{"doctor"},
		{"build"},
		{"list"},
		{"tree"},
		{"version"},
		{"prune"},
		{"config", "show"},
		{"config", "home"},
		{"image", "list"},
		{"image", "build"},
		{"volume", "list"},
		{"volume", "migrate"},
		{"volume", "reset"},
		{"volume", "edit"},
		{"sandbox", "list"},
		{"sandbox", "shell"},
		{"sandbox", "run"},
		{"sandbox", "stop"},
		{"sandbox", "kill"},
	} {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			out := commandOut(t, append(path, "--help")...)
			leaf := path[len(path)-1]
			if !strings.Contains(out, leaf) {
				t.Errorf("expected help for %q to reference command name %q:\n%s", leaf, leaf, out)
			}
		})
	}
}

func TestGroupCommandHelpListsSubcommands(t *testing.T) {
	for _, tc := range []struct {
		path []string
		subs []string
	}{
		{path: []string{"config"}, subs: []string{"show", "home"}},
		{path: []string{"image"}, subs: []string{"list", "build"}},
		{path: []string{"volume"}, subs: []string{"list", "migrate", "reset", "edit"}},
		{path: []string{"sandbox"}, subs: []string{"list", "shell", "run", "stop", "kill"}},
	} {
		t.Run(strings.Join(tc.path, "_"), func(t *testing.T) {
			out := commandOut(t, append(tc.path, "--help")...)
			for _, sub := range tc.subs {
				if !strings.Contains(out, sub) {
					t.Errorf("expected %q help to list subcommand %q:\n%s", strings.Join(tc.path, " "), sub, out)
				}
			}
		})
	}
}

func TestHelpCommandShowsHelp(t *testing.T) {
	for _, args := range [][]string{
		{"help"},
		{"help", "run"},
		{"run", "-h"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out := commandOut(t, args...)

			if len(out) == 0 {
				t.Errorf("%v: expected help output, got none", args)
			}
		})
	}
}
