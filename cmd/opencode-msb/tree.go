package main

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

type treeEntry struct {
	prefix string
	name   string
	desc   string
}

func printTree(rootCmd *cobra.Command, ui termio.UI) {
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

	for _, arg := range argsFromAnnotations(cmd) {
		items = append(items, item{name: arg.Name, desc: arg.Help, sub: nil})
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

func argsFromAnnotations(cmd *cobra.Command) []NamedArg {
	raw, ok := cmd.Annotations[annotationArgs]
	if !ok || raw == "" {
		return nil
	}
	var args []NamedArg
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
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
