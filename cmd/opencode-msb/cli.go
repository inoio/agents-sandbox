package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/config"
	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/output"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

const (
	flagTree    = "--tree"
	flagVersion = "--version"

	cmdRun    = "run"
	cmdDoctor = "doctor"
	cmdBuild  = "build"
	cmdList   = "list"
	cmdConfig = "config"
	cmdImage  = "image"
	cmdVolume = "volume"
	cmdStop   = "stop"
	cmdKill   = "kill"

	flagYes     = "yes"
	flagVerbose = "verbose"
	flagQuiet   = "quiet"
	flagRebuild = "rebuild"
	flagCpus    = "cpus"
	flagMemory  = "memory"
	flagTmpSize = "tmp-size"

	annotationArgsDesc = "opencode-msb/args-description"
	annotationAlsoAs   = "opencode-msb/also-as"
)

var version = "dev"

func Execute() error {
	root := buildRootCmd()

	args := os.Args[1:]

	for _, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case flagTree:
			printTree(os.Stdout, root)
			return nil
		case flagVersion, "-V":
			fmt.Fprintf(os.Stdout, "opencode-msb version %s\n", version)
			return nil
		}
	}

	if len(args) == 0 || !isKnownSubcommand(args[0], root) {
		args = append([]string{cmdRun}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "opencode-msb",
		Short: "Run opencode inside an ephemeral microsandbox VM",
		Long: "Run opencode inside an ephemeral microsandbox VM.\n\n" +
			"When invoked without a subcommand, the \"run\" command is implied.",
	}

	root.PersistentFlags().BoolP("yes", "y", false, "Assume yes to all prompts")
	root.PersistentFlags().BoolP("verbose", "v", false, "Show debug-level output")
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress non-error output")
	root.PersistentFlags().Bool("tree", false, "Print the full command tree and exit")
	root.PersistentFlags().BoolP("version", "V", false, "Print version and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cfg := newConfig()
		lc, keys, err := launcherconfig.Load(cfg.UserLauncherDir, projectLauncherDir)
		if err != nil {
			return err
		}
		if err := applyLauncherConfig(cmd, lc, keys); err != nil {
			return err
		}
		if yes, _ := cmd.Flags().GetBool("yes"); yes {
			prompt.AssumeYes = true //nolint:reassign // CLI flag override, set once at startup
		}
		verbose, _ := cmd.Flags().GetBool("verbose")
		quiet, _ := cmd.Flags().GetBool("quiet")
		level := output.LevelNormal
		if quiet {
			level = output.LevelQuiet
		} else if verbose {
			level = output.LevelVerbose
		}
		logger := output.NewPrinterWithLevel(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())), level)
		sandbox.AutoPrune(cmd.Context(), 0, logger)
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
	root.AddCommand(buildStopCmd())
	root.AddCommand(buildKillCmd())
	root.AddCommand(buildPruneCmd())

	return root
}

func isKnownSubcommand(arg string, root *cobra.Command) bool {
	switch arg {
	case "help", "--help", "-h", flagTree, flagVersion, "-V", "completion":
		return true
	default:
	}

	for _, cmd := range root.Commands() {
		if cmd.Name() == arg {
			return true
		}
		if slices.Contains(cmd.Aliases, arg) {
			return true
		}
	}
	return false
}

type treeEntry struct {
	prefix string
	name   string
	desc   string
}

func printTree(w io.Writer, root *cobra.Command) {
	var entries []treeEntry
	collectTreeEntries(&entries, root, root, "")

	maxWidth := 0
	for _, e := range entries {
		width := utf8.RuneCountInString(e.prefix) + utf8.RuneCountInString(e.name)
		if width > maxWidth {
			maxWidth = width
		}
	}

	fmt.Fprintln(w, root.Name())
	const descPadding = 2
	descCol := maxWidth + descPadding
	for _, e := range entries {
		nameWidth := utf8.RuneCountInString(e.prefix) + utf8.RuneCountInString(e.name)
		padding := descCol - nameWidth
		fmt.Fprintf(w, "%s%s%s%s\n", e.prefix, e.name, strings.Repeat(" ", padding), e.desc)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "When invoked without a subcommand, the \"run\" command is implied.")
}

func collectTreeEntries(entries *[]treeEntry, root *cobra.Command, cmd *cobra.Command, prefix string) {
	type item struct {
		name string
		desc string
		sub  *cobra.Command
	}

	var items []item

	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		items = append(items, item{name: formatFlagName(f), desc: f.Usage, sub: nil})
	})

	for _, sub := range cmd.Commands() {
		items = append(items, item{name: formatCommandName(sub, sub.Parent() == root), desc: sub.Short, sub: sub})
	}

	argsDesc := cmd.Annotations[annotationArgsDesc]
	for _, arg := range positionalArgsFromUse(cmd.Use) {
		items = append(items, item{name: arg, desc: argsDesc, sub: nil})
	}

	for i, it := range items {
		isLast := i == len(items)-1
		var connector, childPrefix string
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		} else {
			connector = "├── "
			childPrefix = prefix + "│   "
		}
		*entries = append(*entries, treeEntry{
			prefix: prefix + connector,
			name:   it.name,
			desc:   it.desc,
		})
		if it.sub != nil {
			collectTreeEntries(entries, root, it.sub, childPrefix)
		}
	}
}

func formatFlagName(f *pflag.Flag) string {
	var b strings.Builder
	if f.Shorthand != "" {
		b.WriteString("-")
		b.WriteString(f.Shorthand)
		b.WriteString(", --")
	} else {
		b.WriteString("--")
	}
	b.WriteString(f.Name)
	if f.Value.Type() != "bool" {
		b.WriteString(" <")
		b.WriteString(strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_")))
		b.WriteString(">")
	}
	return b.String()
}

func positionalArgsFromUse(use string) []string {
	parts := strings.Fields(use)
	if len(parts) <= 1 {
		return nil
	}
	var args []string
	for _, p := range parts[1:] {
		if p == "[flags]" {
			continue
		}
		args = append(args, p)
	}
	return args
}

func formatCommandName(cmd *cobra.Command, isTopLevel bool) string {
	name := cmd.Name()
	if len(cmd.Aliases) > 0 {
		name += " (aliases: " + strings.Join(cmd.Aliases, ", ")
		if alsoAs, ok := cmd.Annotations[annotationAlsoAs]; ok && isTopLevel {
			name += ", also: " + alsoAs
		}
		name += ")"
	} else if alsoAs, ok := cmd.Annotations[annotationAlsoAs]; ok && isTopLevel {
		name += " (also: " + alsoAs + ")"
	}
	return name
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   cmdDoctor,
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := newLogger(cmd)
			if !sandbox.CheckAll(cmd.Context(), logger) {
				return errors.New("preflight failed")
			}
			logger.Infof("doctor: all checks passed")
			return nil
		},
	}
}

func buildBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdBuild,
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
		Annotations: map[string]string{
			annotationArgsDesc: "Arguments forwarded to opencode (use -- to separate from launcher flags)",
			annotationAlsoAs:   "sandbox run",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := sandbox.RunOptions{Args: args, Auto: true}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.TmpSize, _ = cmd.Flags().GetString("tmp-size")
			opts.User, _ = cmd.Flags().GetString("user")
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

	cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Validate setup without running opencode")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit")
	cmd.Flags().String("tmp-size", "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().
		StringP("user", "u", "", "Username or UID for the runtime user (format: <name|uid>[:<group|gid>])")
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:        filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir:   filepath.Join(home, ".config", "opencode-msb", "opencode"),
		UserLauncherDir: filepath.Join(home, ".config", "opencode-msb"),
	}
}

const projectLauncherDir = ".opencode-msb"

func applyLauncherConfig(cmd *cobra.Command, lc launcherconfig.Config, keys map[string]bool) error {
	apply := []struct {
		key string
		fn  func() error
	}{
		{flagYes, func() error { return setBoolFlag(cmd, flagYes, lc.Yes) }},
		{flagVerbose, func() error { return setBoolFlag(cmd, flagVerbose, lc.Verbose) }},
		{flagQuiet, func() error { return setBoolFlag(cmd, flagQuiet, lc.Quiet) }},
		{flagRebuild, func() error { return setBoolFlag(cmd, flagRebuild, lc.Rebuild) }},
		{flagCpus, func() error { return setUint8Flag(cmd, flagCpus, lc.CPUs) }},
		{flagMemory, func() error { return setStringFlag(cmd, flagMemory, lc.Memory) }},
		{flagTmpSize, func() error { return setStringFlag(cmd, flagTmpSize, lc.TmpSize) }},
		{"manual-prune-age", func() error { return setDurationFlag(cmd, "age", lc.ManualPruneAge) }},
	}
	for _, item := range apply {
		if keys[item.key] {
			if err := item.fn(); err != nil {
				return fmt.Errorf("apply launcher config %q: %w", item.key, err)
			}
		}
	}
	return nil
}

func setBoolFlag(cmd *cobra.Command, name string, val bool) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatBool(val))
}

func setUint8Flag(cmd *cobra.Command, name string, val uint8) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatUint(uint64(val), 10))
}

func setStringFlag(cmd *cobra.Command, name string, val string) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed || val == "" {
		return nil
	}
	return f.Value.Set(val)
}

func setDurationFlag(cmd *cobra.Command, name string, val time.Duration) error {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		f = cmd.InheritedFlags().Lookup(name)
	}
	if f == nil || f.Changed || val == 0 {
		return nil
	}
	return f.Value.Set(val.String())
}

func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List sandboxes for this host",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox list",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sandboxes, err := sandbox.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			if len(sandboxes) == 0 {
				logger := newLogger(cmd)
				logger.Infof("No sandboxes found.")
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
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox shell",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := sandbox.RunOptions{Auto: false}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			opts.TmpSize, _ = cmd.Flags().GetString("tmp-size")
			opts.User, _ = cmd.Flags().GetString("user")

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

	cmd.Flags().StringP("branch", "b", "", "Run in an opencode worktree for the given branch name")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit")
	cmd.Flags().String("tmp-size", "2G", "Size of the /tmp tmpfs in the sandbox")
	cmd.Flags().StringP("user", "u", "", "Username or UID to use inside the sandbox (format: <name|uid>[:<group|gid>])")

	return cmd
}

func buildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdConfig,
		Short: "Inspect opencode configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print merged opencode config with source paths",
		RunE: func(_ *cobra.Command, _ []string) error {
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
					for line := range strings.SplitSeq(string(data), "\n") {
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
		Use:   cmdImage,
		Short: "Manage runner images",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			images, err := sandbox.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			if len(images) == 0 {
				logger := newLogger(cmd)
				logger.Infof("No images found.")
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
		Use:   cmdVolume,
		Short: "Manage volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     cmdList,
		Aliases: []string{"ls"},
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := sandbox.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			if len(volumes) == 0 {
				logger := newLogger(cmd)
				logger.Infof("No volumes found.")
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
	cmd.AddCommand(buildRunCmd())
	cmd.AddCommand(buildStopCmd())
	cmd.AddCommand(buildKillCmd())
	return cmd
}

func buildStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdStop,
		Short: "Stop the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox stop",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			logger := newLogger(cmd)
			return sandbox.StopProjectVM(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after stopping")
	return cmd
}

func buildKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   cmdKill,
		Short: "Force-kill the project VM",
		Annotations: map[string]string{
			annotationAlsoAs: "sandbox kill",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			logger := newLogger(cmd)
			return sandbox.KillProjectVM(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Remove the VM's persisted state after killing")
	return cmd
}

func newLogger(cmd *cobra.Command) *output.Printer {
	verbose, _ := cmd.Flags().GetBool("verbose")
	quiet, _ := cmd.Flags().GetBool("quiet")

	level := output.LevelNormal
	if quiet {
		level = output.LevelQuiet
	} else if verbose {
		level = output.LevelVerbose
	}

	return output.NewPrinterWithLevel(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())), level)
}

func buildPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [flags]",
		Short: "Prune stale VMs, volumes, and images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ageStr, _ := cmd.Flags().GetString("age")
			var age time.Duration
			if ageStr != "" {
				if d, ok := launcherconfig.ParseHumanDuration(ageStr); ok {
					age = d
				}
			}
			if age == 0 {
				age = 7 * 24 * time.Hour
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			force, _ := cmd.Flags().GetBool("force")
			logger := newLogger(cmd)
			report, err := sandbox.Prune(cmd.Context(), age, dryRun, force, logger)
			if err != nil {
				return err
			}
			if report != nil {
				printPruneSummary(report, dryRun)
			}
			return nil
		},
	}
	cmd.Flags().StringP("age", "a", "", "Prune threshold (default: manualPruneAge from config)")
	cmd.Flags().BoolP("dry-run", "n", false, "Show what would be pruned without deleting")
	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")
	return cmd
}

func printPruneSummary(report *sandbox.StaleReport, dryRun bool) {
	action := "Pruned"
	if dryRun {
		action = "Would prune"
	}

	parts := []string{
		action,
		fmt.Sprintf("%d VMs", report.PrunedVMs),
		fmt.Sprintf("%d home volumes", report.PrunedVolumes),
		fmt.Sprintf("%d docker images", report.PrunedDockerImages),
		fmt.Sprintf("%d msb images", report.PrunedMSBImages),
		fmt.Sprintf("%d task sandboxes", report.PrunedTaskSandboxes),
		fmt.Sprintf("%d clone volumes", report.PrunedCloneVolumes),
	}

	fmt.Println(strings.Join(parts, ", "))
}
