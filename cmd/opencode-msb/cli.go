package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

var version = "dev"

func Execute() error {
	root := &cobra.Command{
		Use:     "opencode-msb",
		Short:   "Run opencode inside an ephemeral microsandbox VM",
		Version: version,
	}

	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildRunCmd())

	args := os.Args[1:]
	if len(args) == 0 || !isKnownSubcommand(args[0]) {
		args = append([]string{"run"}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

func isKnownSubcommand(arg string) bool {
	switch arg {
	case "doctor", "run", "help", "--help", "-h", "--version", "-v":
		return true
	default:
		return false
	}
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := newLogger()
			if !sandbox.CheckAll(cmd.Context(), logger) {
				return errors.New("preflight failed")
			}
			fmt.Fprintln(os.Stderr, "doctor: all checks passed")
			return nil
		},
	}
}

func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := sandbox.RunOptions{Args: args, Auto: true}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.TestRun, _ = cmd.Flags().GetBool("test-run")
			if noAuto, _ := cmd.Flags().GetBool("no-auto"); noAuto {
				opts.Auto = false
			}
			if yes, _ := cmd.Flags().GetBool("yes"); yes {
				prompt.AssumeYes = true //nolint:reassign // CLI flag override, set once at startup
			}

			cfg := newConfig()
			logger := newLogger()

			err := sandbox.Run(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().String("branch", "", "Run in an isolated git clone for the given branch")
	cmd.Flags().Bool("image-rebuild", false, "Force image rebuild")
	cmd.Flags().Uint8("cpus", 0, "Number of CPUs (default: all)")
	cmd.Flags().String("memory", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")
	cmd.Flags().Bool("test-run", false, "Validate setup without running opencode")
	cmd.Flags().BoolP("yes", "y", false, "Assume yes to all prompts")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:      filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir: filepath.Join(home, ".config", "opencode-msb", "opencode"),
	}
}

func newLogger() *log.Logger {
	return log.New(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
}
