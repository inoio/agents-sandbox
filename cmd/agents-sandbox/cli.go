package main

import (
	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// devVersion is the version baked into locally built binaries via ldflags
// only for releases.
const devVersion = "dev"

var version = devVersion

// execute runs the CLI with the given arguments and UI.
//
// For integration testing, override the injection seams using:
//   - msb:   msb.WithMsbMock (replaces the msb.Client factory for one test)
//   - docker:  docker.WithNoopDockerMock / docker.WithDefaultErrorDockerMock / docker.WithDockerMock
//
// Example:
//
//	func TestListSandboxCommand(t *testing.T) {
//	    msb.WithMsbMock(t, mock)
//
//	    ui := &termio.Mock{}
//	    err := Execute([]string{"list"}, ui)
//	    // ...assert...
//	}
func execute(args []string, ui termio.UI) error {
	rootCmd := buildRootCmd(ui)
	rootCmd.SetArgs(args)
	// buildRootCmd already routes cmd output to the UI; execute additionally
	// silences cobra's own usage/error printing so main() is the only reporter.
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	return rootCmd.Execute()
}

// applyCLISettings sets the terminal output level and assume-yes state on the UI
// based on the effective --log-level/--yes flags of the running command.
//
// It must run after cobra parses the real command tree and after launcher
// config has been merged, so that flags work regardless of position or how
// short shorthands are grouped (e.g. "-ny").
func applyCLISettings(cmd *cobra.Command, ui termio.TerminalState, r *launcherconfig.Resolver) error {
	if cmd == nil || r == nil {
		return nil
	}
	level, err := termio.ParseLevel(r.LogLevel())
	if err != nil {
		return err
	}
	ui.SetLevel(level)
	ui.SetAssumeYes(r.Yes())
	ui.SetQuiet(r.Quiet())
	return nil
}
