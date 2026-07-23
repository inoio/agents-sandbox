package main

import (
	"strings"
	"testing"
)

func TestIsKnownSubcommandRecognizesRegisteredCommands(t *testing.T) {
	root := buildRootCmd()
	tests := []struct {
		arg  string
		want bool
	}{
		{"run", true},
		{"doctor", true},
		{"build", true},
		{"help", true},
		{"--help", true},
		{"-h", true},
		{"--tree", true},
		{"--version", true},
		{"-V", true},
		{"unknown-cmd", false},
		{"--branch", false},
		{"-b", false},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			if got := isKnownSubcommand(tt.arg, root); got != tt.want {
				t.Errorf("isKnownSubcommand(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestPrintTreeContainsAllCommands(t *testing.T) {
	root := buildRootCmd()
	var sb strings.Builder
	printTree(&sb, root, "")
	out := sb.String()
	expected := []string{"run", "doctor", "build"}
	for _, cmd := range expected {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected tree to contain %q, got:\n%s", cmd, out)
		}
	}
}

func TestRootHasGlobalFlags(t *testing.T) {
	root := buildRootCmd()
	flags := []string{"yes", "verbose", "quiet", "tree", "version"}
	for _, f := range flags {
		if root.PersistentFlags().Lookup(f) == nil {
			t.Errorf("expected persistent flag --%s on root", f)
		}
	}
}

func TestRunCommandHasExpectedFlags(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	flags := []string{"branch", "cpus", "memory", "rebuild", "dry-run", "no-auto"}
	for _, f := range flags {
		if runCmd.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s on run command", f)
		}
	}
}

func TestRunCommandFlagShortcuts(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	shortcuts := map[string]string{
		"b": "branch", "c": "cpus", "m": "memory",
		"r": "rebuild", "n": "dry-run", "y": "yes",
		"v": "verbose", "q": "quiet",
	}
	for short, long := range shortcuts {
		f := runCmd.Flags().ShorthandLookup(short)
		if f == nil {
			f = root.PersistentFlags().ShorthandLookup(short)
		}
		if f == nil {
			t.Errorf("expected shorthand -%s for --%s", short, long)
		}
	}
}
