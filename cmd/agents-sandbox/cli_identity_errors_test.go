package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPersistentPreRunFailsWhenIdentityUnresolvable pins #354 on the CLI entry:
// a cwd whose git dir cannot be resolved aborts every command before it derives
// a project slug (and so a state dir, VM and home volume).
func TestPersistentPreRunFailsWhenIdentityUnresolvable(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdVersion)
	t.Chdir(unresolvableIdentityDir(t))

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "resolve project identity") {
		t.Fatalf("error = %v, want a project identity resolution error", err)
	}
	// The cause must survive the wrap, so the message alone is not enough.
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestConfigPathsErrorsWhenIdentityUnresolvable covers the config paths RunE
// branch directly: the leaf command resolves the identity itself.
func TestConfigPathsErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	cmd := buildConfigPathsCmd(&termio.Mock{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("config paths succeeded with an unresolvable identity, want an error")
	}
}

// TestBuildErrorsWhenIdentityUnresolvable covers the build RunE branch: a dry
// run skips the Docker prerequisite and fails on the identity resolution.
func TestBuildErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	cmd := buildBuildCmd(&termio.Mock{})
	cmd.SetArgs([]string{"--dry-run"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("build succeeded with an unresolvable identity, want an error")
	}
}

// TestPrepareVolumeOpErrorsWhenIdentityUnresolvable covers the volume-op branch:
// the agent resolves from the flag before the identity resolution fails.
func TestPrepareVolumeOpErrorsWhenIdentityUnresolvable(t *testing.T) {
	t.Chdir(unresolvableIdentityDir(t))

	cmd := &cobra.Command{}
	cmd.Flags().String(flagAgent, defaultAgentName, "")
	if _, _, err := prepareVolumeOp(cmd, &termio.Mock{}, true); err == nil {
		t.Fatal("prepareVolumeOp succeeded with an unresolvable identity, want an error")
	}
}
