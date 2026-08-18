package main

import (
	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/termio"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

var version = "dev"

// execute runs the CLI with the given arguments and UI.
//
// For integration testing, override the injection seams using:
//   - msb:   msb.ResetGetFn (replaces the msb.Client factory)
//   - docker:  docker.WithNoopDockerMock / docker.WithDefaultErrorDockerMock / docker.WithDockerMock
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    orig := msb.ResetGetFn(func() msb.Client { return mock })
//	    t.Cleanup(func() { msb.ResetGetFn(orig) })
//
//	    ui := &termio.Mock{}
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func execute(args []string, ui termio.UI) error {
	rootCmd := buildRootCmd(ui)
	rootCmd.SetArgs(args)
	rootCmd.SetOut(ui.StdOut())
	rootCmd.SetErr(ui.StdErr())
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	return rootCmd.Execute()
}

// applyCLISettings sets the terminal output level and assume-yes state on the UI
// based on the effective --verbose/--error/--yes flags of the running command.
//
// It must run after cobra parses the real command tree and after launcher
// config has been merged, so that flags work regardless of position or how
// short shorthands are grouped (e.g. "-nv").
func applyCLISettings(cmd *cobra.Command, ui termio.UI, r *launcherconfig.Resolver) {
	if cmd == nil || r == nil {
		return
	}
	quiet := r.Error()
	verbose := r.Verbose()
	yes := r.Yes()
	ui.SetLevel(levelFrom(quiet, verbose))
	ui.SetAssumeYes(yes)
}

func levelFrom(quiet, verbose bool) termio.Level {
	switch {
	case quiet:
		return termio.LevelQuiet
	case verbose:
		return termio.LevelVerbose
	default:
		return termio.LevelNormal
	}
}
