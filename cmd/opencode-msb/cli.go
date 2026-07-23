package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

var version = "dev" //nolint:gochecknoglobals // set at link time

func Execute() error {
	root := buildRootCmd()

	args := os.Args[1:]

	for _, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case "--tree":
			printTree(os.Stdout, root, "")
			return nil
		case "--version", "-V":
			fmt.Fprintf(os.Stdout, "opencode-msb version %s\n", version)
			return nil
		}
	}

	if len(args) == 0 || !isKnownSubcommand(args[0], root) {
		args = append([]string{"run"}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "opencode-msb",
		Short: "Run opencode inside an ephemeral microsandbox VM",
	}

	root.PersistentFlags().BoolP("yes", "y", false, "Assume yes to all prompts")
	root.PersistentFlags().BoolP("verbose", "v", false, "Show debug-level output")
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress non-error output")
	root.PersistentFlags().Bool("tree", false, "Print the full command tree and exit")
	root.PersistentFlags().BoolP("version", "V", false, "Print version and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if yes, _ := cmd.Flags().GetBool("yes"); yes {
			prompt.AssumeYes = true //nolint:reassign // CLI flag override, set once at startup
		}
		return nil
	}

	root.AddCommand(buildRunCmd())
	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildBuildCmd())
	root.AddCommand(buildListCmd())
	root.AddCommand(buildShellCmd())
	root.AddCommand(buildConfigCmd())
	root.AddCommand(buildImageCmd())
	root.AddCommand(buildVolumeCmd())
	root.AddCommand(buildSandboxCmd())

	return root
}

func isKnownSubcommand(arg string, root *cobra.Command) bool {
	switch arg {
	case "help", "--help", "-h", "--tree", "--version", "-V":
		return true
	default:
	}

	for _, cmd := range root.Commands() {
		if cmd.Name() == arg {
			return true
		}
		for _, alias := range cmd.Aliases {
			if alias == arg {
				return true
			}
		}
	}
	return false
}

func printTree(w io.Writer, cmd *cobra.Command, prefix string) {
	subs := cmd.Commands()
	for i, sub := range subs {
		isLast := i == len(subs)-1
		var connector, childPrefix string
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		} else {
			connector = "├── "
			childPrefix = prefix + "│   "
		}
		fmt.Fprintf(w, "%s%s%s", prefix, connector, sub.Name())
		if len(sub.Aliases) > 0 {
			fmt.Fprintf(w, " (aliases: %s)", strings.Join(sub.Aliases, ", "))
		}
		fmt.Fprintln(w)
		printTree(w, sub, childPrefix)
	}
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := newLogger(cmd)
			if !sandbox.CheckAll(cmd.Context(), logger) {
				return errors.New("preflight failed")
			}
			fmt.Fprintln(os.Stderr, "doctor: all checks passed")
			return nil
		},
	}
}

func buildBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build or rebuild the runner image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("rebuild")
			logger := newLogger(cmd)
			return sandbox.BuildImage(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("rebuild", "r", false, "Force a clean rebuild")
	return cmd
}

func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := sandbox.RunOptions{Args: args, Auto: true}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			if noAuto, _ := cmd.Flags().GetBool("no-auto"); noAuto {
				opts.Auto = false
			}

			cfg := newConfig()
			logger := newLogger(cmd)

			err := sandbox.Run(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().StringP("branch", "b", "", "Run in an isolated git clone for the given branch")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Validate setup without running opencode")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:      filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir: filepath.Join(home, ".config", "opencode-msb", "opencode"),
	}
}

func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes for this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sandboxes, err := sandbox.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			if len(sandboxes) == 0 {
				fmt.Fprintln(os.Stderr, "No sandboxes found.")
				return nil
			}
			for _, s := range sandboxes {
				fmt.Fprintf(os.Stdout, "%-40s %s\n", s.Name, s.Status)
			}
			return nil
		},
	}
	return cmd
}

func buildShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [flags]",
		Short: "Start sandbox and open a shell (debug)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := sandbox.RunOptions{Auto: false}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")

			cfg := newConfig()
			logger := newLogger(cmd)

			err := sandbox.Shell(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().StringP("branch", "b", "", "Run in an isolated git clone for the given branch")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit (default: 4G)")

	return cmd
}

func buildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect opencode configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print merged opencode config with source paths",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := newConfig()
			projectConfigDir := ""
			if _, statErr := os.Stat(".opencode-msb/opencode"); statErr == nil {
				projectConfigDir = ".opencode-msb/opencode"
			}
			providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
			if err != nil {
				return fmt.Errorf("load provider config: %w", err)
			}

			descs, err := config.DescribeConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}
			files, err := config.BuildMergedConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}

			for _, desc := range descs {
				fmt.Fprintf(os.Stdout, "=== %s ===\n", desc.Name)
				for _, src := range desc.Sources {
					fmt.Fprintf(os.Stdout, "  source: %s\n", src)
				}
				if data, ok := files[desc.Name]; ok {
					fmt.Fprintln(os.Stdout, "  merged content:")
					for _, line := range strings.Split(string(data), "\n") {
						fmt.Fprintf(os.Stdout, "    %s\n", line)
					}
				}
				fmt.Fprintln(os.Stdout)
			}
			return nil
		},
	})
	return cmd
}

func buildImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage runner images",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			images, err := sandbox.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			if len(images) == 0 {
				fmt.Fprintln(os.Stderr, "No images found.")
				return nil
			}
			for _, img := range images {
				fmt.Fprintf(os.Stdout, "%-50s %s\n", img.Reference, img.Digest)
			}
			return nil
		},
	})
	cmd.AddCommand(buildBuildCmd())
	return cmd
}

func buildVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := sandbox.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			if len(volumes) == 0 {
				fmt.Fprintln(os.Stderr, "No volumes found.")
				return nil
			}
			for _, vol := range volumes {
				fmt.Fprintf(os.Stdout, "%-50s %s\n", vol.Name, vol.Path)
			}
			return nil
		},
	})
	return cmd
}

func buildSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd())
	cmd.AddCommand(buildShellCmd())
	return cmd
}

func newLogger(cmd *cobra.Command) *log.Logger {
	verbose, _ := cmd.Flags().GetBool("verbose")
	quiet, _ := cmd.Flags().GetBool("quiet")

	level := log.LevelNormal
	if quiet {
		level = log.LevelQuiet
	} else if verbose {
		level = log.LevelVerbose
	}

	return log.NewWithLevel(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())), level)
}
