package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

const (
	pFlagYes     = "yes"
	pFlagVerbose = "verbose"
	pFlagQuiet   = "quiet"

	cmdRun     = "run"
	cmdDoctor  = "doctor"
	cmdBuild   = "build"
	cmdList    = "list"
	cmdTree    = "tree"
	cmdVersion = "version"
	cmdConfig  = "config"
	cmdImage   = "image"
	cmdVolume  = "volume"
	cmdStop    = "stop"
	cmdKill    = "kill"

	flagRebuild = "rebuild"
	flagCpus    = "cpus"
	flagMemory  = "memory"
	flagTmpSize = "tmp-size"

	annotationArgsDesc = "opencode-msb/args-description"
	annotationAlsoAs   = "opencode-msb/also-as"
)

var version = "dev"

func Execute(args []string, ui stdio.UI) error {
	rootCmd := buildRootCmd(ui)
	/*if len(args) == 0 || !containsSubcommand(args, rootCmd) {
		ui.Verbose("adding implicit run command to args")
		args = append([]string{cmdRun}, args...)
	}*/
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

func getIOLevel(root *cobra.Command) stdio.Level {
	verbose, _ := root.Flags().GetBool("verbose")
	quiet, _ := root.Flags().GetBool("quiet")
	level := stdio.LevelNormal
	if quiet {
		level = stdio.LevelQuiet
	} else if verbose {
		level = stdio.LevelVerbose
	}
	return level
}

type treeEntry struct {
	prefix string
	name   string
	desc   string
}

func printTree(rootCmd *cobra.Command, ui stdio.UI) {
	var entries []treeEntry
	collectTreeEntries(&entries, rootCmd, rootCmd, "")

	maxWidth := 0
	for _, e := range entries {
		width := utf8.RuneCountInString(e.prefix) + utf8.RuneCountInString(e.name)
		if width > maxWidth {
			maxWidth = width
		}
	}

	ui.Info(rootCmd.Name())
	const descPadding = 2
	descCol := maxWidth + descPadding
	for _, e := range entries {
		nameWidth := utf8.RuneCountInString(e.prefix) + utf8.RuneCountInString(e.name)
		padding := descCol - nameWidth
		ui.Infof("%s%s%s%s", e.prefix, e.name, strings.Repeat(" ", padding), e.desc)
	}
	ui.Info("")
	ui.Info("When invoked without a subcommand, the \"run\" command is implied.")
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
		{pFlagYes, func() error { return setBoolFlag(cmd, pFlagYes, lc.Yes) }},
		{pFlagVerbose, func() error { return setBoolFlag(cmd, pFlagVerbose, lc.Verbose) }},
		{pFlagQuiet, func() error { return setBoolFlag(cmd, pFlagQuiet, lc.Quiet) }},
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

func newUI(args []string) stdio.UI {
	minimalCmd := buildMinimalRootFlagsCmd()
	// We don't care about errors, just parse the minimal flags for UI initialization
	_ = minimalCmd.ParseFlags(args)
	yes, _ := minimalCmd.Flags().GetBool("yes")
	level := getIOLevel(minimalCmd)

	return stdio.New(os.Stdin, os.Stdout, os.Stderr,
		term.IsTerminal(int(os.Stderr.Fd())), level, yes)
}

func printPruneSummary(ui stdio.UI, report *sandbox.StaleReport, dryRun bool) {
	action := "Pruned"
	if dryRun {
		action = "dry-run: Would prune"
	}

	ui.Outf(
		"%s %d VMs, %d home volumes, %d docker images, %d msb images, %d task sandboxes, %d clone volumes",
		action,
		report.PrunedVMs,
		report.PrunedVolumes,
		report.PrunedDockerImages,
		report.PrunedMSBImages,
		report.PrunedTaskSandboxes,
		report.PrunedCloneVolumes,
	)
}
