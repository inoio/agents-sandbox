package opencodemsb

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var version = "dev"

var (
	stateDir      string
	userConfigDir string
)

func init() {
	home, _ := os.UserHomeDir()
	stateDir = filepath.Join(home, ".local", "share", "opencode-msb")
	userConfigDir = filepath.Join(home, ".config", "inoio-sandbox", "opencode")
}

func setLogOutput(w io.Writer) {
	logMu.Lock()
	defer logMu.Unlock()
	color := false
	if f, ok := w.(*os.File); ok {
		color = term.IsTerminal(int(f.Fd()))
	}
	logOut = newLogger(w, color)
}

func newTiming(enabled bool) (func(string), func()) {
	start := time.Now()
	var phases []struct {
		label   string
		elapsed time.Duration
	}

	tick := func(label string) {
		now := time.Now()
		elapsed := now.Sub(start)
		start = now
		phases = append(phases, struct {
			label   string
			elapsed time.Duration
		}{label, elapsed})
		if enabled {
			logMu.Lock()
			logOut.Timing(label, elapsed)
			logMu.Unlock()
		}
	}

	summary := func() {
		if !enabled {
			return
		}
		var total time.Duration
		for _, p := range phases {
			total += p.elapsed
		}
		logMu.Lock()
		logOut.Timing("total launcher overhead", total)
		logMu.Unlock()
	}

	return tick, summary
}

type RunOptions struct {
	Worktree       string
	ImageRebuild   bool
	VolumeFallback bool
	ResetHome      bool
	CPUs           uint8
	Memory         string
	Timing         bool
	Args           []string
}

func Execute() error {
	root := &cobra.Command{
		Use:     "opencode-msb",
		Short:   "Run opencode inside an ephemeral microsandbox VM",
		Version: version,
	}

	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildRunCmd())

	args := os.Args[1:]
	if len(args) == 0 || (args[0] != "doctor" && args[0] != "run" && args[0] != "help" && args[0] != "--help" && args[0] != "-h" && args[0] != "--version" && args[0] != "-v" && !strings.HasPrefix(args[0], "-")) {
		opts := parseRunFlags(args)
		err := runCommand(opts)
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		return err
	}

	return root.Execute()
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !CheckAll(cmd.Context()) {
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
			opts := RunOptions{Args: args}
			opts.Worktree, _ = cmd.Flags().GetString("worktree")
			opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
			opts.VolumeFallback, _ = cmd.Flags().GetBool("volume-fallback")
			opts.ResetHome, _ = cmd.Flags().GetBool("reset-home")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.Timing, _ = cmd.Flags().GetBool("timing")
			return runCommand(opts)
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

func parseRunFlags(args []string) RunOptions {
	opts := RunOptions{Memory: "4G"}
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--worktree" || arg == "-w":
			if i+1 < len(args) {
				opts.Worktree = args[i+1]
				i += 2
				continue
			}
		case arg == "--image-rebuild":
			opts.ImageRebuild = true
		case arg == "--volume-fallback":
			opts.VolumeFallback = true
		case arg == "--reset-home":
			opts.ResetHome = true
		case arg == "--timing":
			opts.Timing = true
		case arg == "--cpus" && i+1 < len(args):
			var cpus uint8
			if _, err := fmt.Sscanf(args[i+1], "%d", &cpus); err == nil {
				opts.CPUs = cpus
			}
			i += 2
			continue
		case arg == "--memory" && i+1 < len(args):
			opts.Memory = args[i+1]
			i += 2
			continue
		case !strings.HasPrefix(arg, "-"):
			opts.Args = append(opts.Args, arg)
		}
		i++
	}
	return opts
}
