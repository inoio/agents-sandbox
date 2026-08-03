package main

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/testhelpers"
)

func TestTree(t *testing.T) {
	t.Run("T1", func(t *testing.T) {
		// Tree root name is printed as first line of InfoCalls
		testUI := testhelpers.NewTestio(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		if len(testUI.InfoCalls) == 0 {
			t.Fatal("expected at least one InfoCall in tree output")
		}
		if testUI.InfoCalls[0] != root.Name() {
			t.Errorf("expected tree root name %q, got %q", root.Name(), testUI.InfoCalls[0])
		}
	})

	t.Run("T2", func(t *testing.T) {
		// Tree lists all subcommands
		testUI := testhelpers.NewTestio(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		out := strings.Join(testUI.InfoCalls, "\n")
		expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume", "stop", "kill"}
		for _, cmd := range expected {
			if !strings.Contains(out, cmd) {
				t.Errorf("expected tree to contain command %q, output:\n%s", cmd, out)
			}
		}
	})

	t.Run("T3", func(t *testing.T) {
		// Tree shows flag descriptions from persistent and local flags
		testUI := testhelpers.NewTestio(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		out := strings.Join(testUI.InfoCalls, "\n")
		descs := []string{
			"Assume yes to all prompts",
			"Memory limit",
		}
		for _, d := range descs {
			if !strings.Contains(out, d) {
				t.Errorf("expected flag description %q in tree output:\n%s", d, out)
			}
		}
	})

	t.Run("T4", func(t *testing.T) {
		// Default version is "dev"
		orig := version
		defer func() { version = orig }()
		version = "dev"

		testUI := testhelpers.NewTestio(t)
		root := buildRootCmd(&testUI)
		treeCmd, _, _ := root.Find([]string{"tree"})
		versionCmd, _, _ := root.Find([]string{"version"})

		if treeCmd == nil || versionCmd == nil {
			t.Fatal("expected tree and version commands to be found")
		}

		treeCmd.Run(treeCmd, nil)
		versionCmd.Run(versionCmd, nil)

		if len(testUI.OutCalls) == 0 {
			t.Fatal("expected at least one OutCall from version command")
		}
		if !strings.Contains(testUI.OutCalls[0], "opencode-msb dev") {
			t.Errorf("expected version output to contain %q, got %q", "opencode-msb dev", testUI.OutCalls[0])
		}
	})

	t.Run("T5", func(t *testing.T) {
		// Custom version is displayed correctly
		orig := version
		defer func() { version = orig }()
		version = "1.2.3"

		testUI := testhelpers.NewTestio(t)
		root := buildRootCmd(&testUI)
		versionCmd, _, _ := root.Find([]string{"version"})

		if versionCmd == nil {
			t.Fatal("expected version command to be found")
		}

		versionCmd.Run(versionCmd, nil)

		if len(testUI.OutCalls) == 0 {
			t.Fatal("expected at least one OutCall from version command")
		}
		if !strings.Contains(testUI.OutCalls[0], "opencode-msb 1.2.3") {
			t.Errorf("expected version output to contain %q, got %q", "opencode-msb 1.2.3", testUI.OutCalls[0])
		}
	})
}
