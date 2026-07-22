package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
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
		os.Args = append([]string{os.Args[0], "run"}, args...)
	}

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
				return fmt.Errorf("preflight failed")
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
			opts := sandbox.RunOptions{Args: args}
			opts.Worktree, _ = cmd.Flags().GetString("worktree")
			opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
			opts.VolumeFallback, _ = cmd.Flags().GetBool("volume-fallback")
			opts.ResetHome, _ = cmd.Flags().GetBool("reset-home")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.Timing, _ = cmd.Flags().GetBool("timing")

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

	cmd.Flags().String("worktree", "", "Worktree name")
	cmd.Flags().Bool("image-rebuild", false, "Force image rebuild")
	cmd.Flags().Bool("volume-fallback", false, "Use host directories instead of msb volumes")
	cmd.Flags().Bool("reset-home", false, "Recreate the project home volume")
	cmd.Flags().Uint8("cpus", 0, "Number of CPUs (default: all)")
	cmd.Flags().String("memory", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("timing", false, "Print per-phase launcher timing to stderr")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:      filepath.Join(home, ".local", "share", "opencode-msb"),
		UserConfigDir: filepath.Join(home, ".config", "inoio-sandbox", "opencode"),
	}
}

func newLogger() *log.Logger {
	return log.New(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())))
}
