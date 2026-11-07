package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestTree(t *testing.T) {
	t.Run("root name printed as first line", func(t *testing.T) {
		testUI := termio.NewTestMock(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		if len(testUI.InfoCalls) == 0 {
			t.Fatal("expected at least one InfoCall in tree output")
		}
		if testUI.InfoCalls[0] != root.Name() {
			t.Errorf("expected tree root name %q, got %q", root.Name(), testUI.InfoCalls[0])
		}
	})

	t.Run("lists all subcommands", func(t *testing.T) {
		testUI := termio.NewTestMock(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		out := strings.Join(testUI.InfoCalls, "\n")
		expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume", "stop", "kill"}
		for _, cmd := range expected {
			if !strings.Contains(out, cmd) {
				t.Errorf("expected tree to contain command %q, output:\n%s", cmd, out)
			}
		}
	})

	t.Run("shows flag descriptions from persistent and local flags", func(t *testing.T) {
		testUI := termio.NewTestMock(t)
		root := buildRootCmd(&testUI)
		printTree(root, &testUI)
		out := strings.Join(testUI.InfoCalls, "\n")
		descs := []string{
			"Assume yes to all prompts",
			"Minimum log level to show (error, warning, info, verbose)",
			"(opencode, opencode2) Run in an isolated worktree named <name>, optionally starting from the local base ref <name>:<base>",
			"Rebuild the runner image before starting",
			"Dry run without starting anything",
			"Number of CPUs (default: all)",
			"Memory limit",
			"Size of the /tmp tmpfs in the sandbox",
			"Size of the project VM root disk (e.g. 16G)",
		}
		for _, d := range descs {
			if !strings.Contains(out, d) {
				t.Errorf("expected flag description %q in tree output:\n%s", d, out)
			}
		}
	})
}

// buildTree sets up a test UI, builds the root command, renders the tree,
// and returns the UI for use in test assertions.
func buildTree(t *testing.T) *termio.Mock {
	t.Helper()
	ui := termio.NewTestMock(t)
	root := buildRootCmd(&ui)
	printTree(root, &ui)
	return &ui
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeDocumentsImplicitRun(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	if !strings.Contains(out, "When invoked without a subcommand, the \"run\" command is implied.") {
		t.Errorf("expected implicit run note in tree output:\n%s", out)
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeContainsCommandDescriptions(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	descs := []string{
		"Run coding agents in a microsandbox VM",
		"Check prerequisites",
		"Build or rebuild the runner image",
		"List sandboxes for this host",
		"Start sandbox and open a shell (debug)",
		"Inspect agent and home configuration",
		"Manage runner images",
		"Manage home volumes",
		"Manage sandboxes",
		"Show the merged agent config and the host files provisioned into the VM",
		"List cached runner images",
		"List managed volumes",
		"Stop the project VM",
		"Force-kill the project VM",
	}
	for _, d := range descs {
		if !strings.Contains(out, d) {
			t.Errorf("expected description %q in tree output:\n%s", d, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeStringFlagsHaveValuePlaceholders(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	expected := []string{
		"--worktree <WORKTREE>",
		"--cpus <CPUS>",
		"--memory <MEMORY>",
		"--tmp-size <TMP_SIZE>",
		"--disk-size <DISK_SIZE>",
	}
	for _, s := range expected {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in tree output:\n%s", s, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeBoolFlagsHaveNoValuePlaceholders(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	notExpected := []string{
		"--yes <YES>",
		"--tree <TREE>",
		"--version <VERSION>",
		"--rebuild <REBUILD>",
		"--dry-run <DRY_RUN>",
	}
	for _, s := range notExpected {
		if strings.Contains(out, s) {
			t.Errorf("did not expect %q in tree output:\n%s", s, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeFlagShortcuts(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	expected := []string{
		"-y, --yes",
		"-q, --quiet",
		"-l, --log-level <LOG_LEVEL>",
		"-w, --worktree <WORKTREE>",
		"-r, --rebuild",
		"-n, --dry-run",
		"-c, --cpus <CPUS>",
		"-m, --memory <MEMORY>",
	}
	for _, s := range expected {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in tree output:\n%s", s, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeDescriptionsGloballyAligned(t *testing.T) {
	testUI := buildTree(t)
	lines := testUI.InfoCalls

	colCounts := map[int]int{}
	for _, line := range lines[1:] {
		col := descriptionStartCol(line)
		if col > 0 {
			colCounts[col]++
		}
	}
	if len(colCounts) == 0 {
		t.Fatal("expected at least some lines with descriptions in tree output")
	}
	if len(colCounts) > 1 {
		t.Errorf("expected all descriptions at the same column, got: %v\n%s", colCounts, strings.Join(lines, "\n"))
	}
}

func TestPrintTreeContainsAliases(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	if !strings.Contains(out, "list (aliases: ls)") {
		t.Errorf("expected alias annotation in tree output:\n%s", out)
	}
}

func TestPrintTreeShowsSandboxShorthands(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	shortcuts := []string{
		"run (also: sandbox run)",
		"list (aliases: ls, also: sandbox list)",
		"shell (aliases: sh, also: sandbox shell)",
	}
	for _, s := range shortcuts {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in tree output:\n%s", s, out)
		}
	}
}

func TestPrintTreeContainsStorageCommands(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	for _, s := range []string{
		"df",
		"storage",
		"prune (also: storage prune)",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in tree output:\n%s", s, out)
		}
	}
}

// TestPrintTreeContainsConfigPaths pins that the tree renders the config paths
// subcommand and its description under config.
func TestPrintTreeContainsConfigPaths(t *testing.T) {
	out := strings.Join(buildTree(t).InfoCalls, "\n")
	// Assert the subcommand's own row, not a bare "paths" substring: "host
	// paths" in the storage --verbose description would satisfy that.
	row := "── " + cmdPaths + "  "
	if !strings.Contains(out, row) {
		t.Fatalf("expected a %q row in tree output:\n%s", row, out)
	}
	if !strings.Contains(out, "Print the config layers in precedence order") {
		t.Errorf("expected the config paths description in tree output:\n%s", out)
	}
}

func TestPrintTreePositionalArgsHaveDescription(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "[ARGS...]") {
			desc := strings.TrimSpace(extractDescription(line))
			if desc == "" {
				t.Errorf("expected [ARGS...] to have a description, got empty in line:\n%s", line)
			}
			if !strings.Contains(desc, "--") {
				t.Errorf("expected [ARGS...] description to mention --, got:\n%s", desc)
			}
			if !strings.Contains(desc, "opencode") {
				t.Errorf("expected [ARGS...] description to mention opencode, got:\n%s", desc)
			}
		}
	}
}

func extractDescription(line string) string {
	runes := []rune(line)
	i := 0
	for i < len(runes) && isTreeChar(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return ""
	}
	for ; i < len(runes)-1; i++ {
		if runes[i] == ' ' && runes[i+1] == ' ' {
			j := i
			for j < len(runes) && runes[j] == ' ' {
				j++
			}
			return string(runes[j:])
		}
	}
	return ""
}

func descriptionStartCol(line string) int {
	line = strings.TrimRight(line, " ")
	runes := []rune(line)

	i := 0
	for i < len(runes) && isTreeChar(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return -1
	}

	for ; i < len(runes)-1; i++ {
		if runes[i] == ' ' && runes[i+1] == ' ' {
			j := i
			for j < len(runes) && runes[j] == ' ' {
				j++
			}
			if j < len(runes) {
				return j
			}
			return -1
		}
	}
	return -1
}

func isTreeChar(r rune) bool {
	return r == '│' || r == '├' || r == '└' || r == '─' || r == ' '
}

// TestPrintTreeConnectorLayout pins the exact tree rendering for a synthetic
// command: the connector choice (├── / └──), the child-prefix continuation, the
// description padding constant, and the trailing blank line. Comparing the full
// slice keeps every branch and the alignment arithmetic load-bearing.
func TestPrintTreeConnectorLayout(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	alpha := &cobra.Command{Use: "alpha", Short: "first"}
	alpha.AddCommand(&cobra.Command{Use: "inner", Short: "nested"})
	beta := &cobra.Command{Use: "beta", Short: "second"}
	// The last sibling carries a child so the isLast child-prefix branch is
	// load-bearing.
	beta.AddCommand(&cobra.Command{Use: "inner2", Short: "nested2"})
	root.AddCommand(alpha, beta)

	ui := &termio.Mock{}
	printTree(root, ui)

	want := []string{
		"root",
		"├── alpha       first",
		"│   └── inner   nested",
		"└── beta        second",
		"    └── inner2  nested2",
		"",
		"When invoked without a subcommand, the \"run\" command is implied.",
	}
	if len(ui.InfoCalls) != len(want) {
		t.Fatalf("InfoCalls = %#v, want %#v", ui.InfoCalls, want)
	}
	for i := range want {
		if ui.InfoCalls[i] != want[i] {
			t.Errorf("InfoCalls[%d] = %q, want %q", i, ui.InfoCalls[i], want[i])
		}
	}
}

// TestCollectTreeEntriesIncludesEveryAnnotationArg pins that all positional-arg
// annotations become entries: a range break or a dropped append after the first
// would hide the later ones.
func TestCollectTreeEntriesIncludesEveryAnnotationArg(t *testing.T) {
	cmd := &cobra.Command{
		Use: "synthetic",
		Annotations: map[string]string{
			annotationArgs: `[{"name":"[ONE]","help":"one"},{"name":"[TWO]","help":"two"}]`,
		},
	}
	var entries []treeEntry
	collectTreeEntries(&entries, cmd, cmd, "")

	var names []string
	for _, e := range entries {
		names = append(names, e.name)
	}
	joined := strings.Join(names, "\n")
	if !strings.Contains(joined, "[ONE]") || !strings.Contains(joined, "[TWO]") {
		t.Errorf("entries = %v, want both [ONE] and [TWO]", names)
	}
}

// TestArgsFromAnnotationsPartialSliceReturnsNil pins that an array whose first
// element decodes before a later type error is discarded: json.Unmarshal
// pre-validates syntax, so only a type error leaves a partially populated slice,
// which the guard must drop rather than return.
func TestArgsFromAnnotationsPartialSliceReturnsNil(t *testing.T) {
	cmd := &cobra.Command{
		Annotations: map[string]string{annotationArgs: `[{"name":"[ONE]","help":"one"},{"name":123}]`},
	}
	if got := argsFromAnnotations(cmd); got != nil {
		t.Errorf("expected nil for a partially decoded array, got %v", got)
	}
}

// TestCollectTreeEntriesTreatsDirectChildrenAsTopLevel pins that a direct child
// is passed isTopLevel=true, so its also-as annotation is rendered.
func TestCollectTreeEntriesTreatsDirectChildrenAsTopLevel(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	child := &cobra.Command{Use: "child", Annotations: map[string]string{annotationAlsoAs: "alias"}}
	root.AddCommand(child)

	var entries []treeEntry
	collectTreeEntries(&entries, root, root, "")

	var names []string
	for _, e := range entries {
		names = append(names, e.name)
	}
	if !strings.Contains(strings.Join(names, "\n"), "child (also: alias)") {
		t.Errorf("direct child entry = %v, want it to carry the also-as annotation", names)
	}
}

// TestFormatCommandNameAlsoAs pins the also-as annotation rules: it is shown
// only when the command has the annotation and is top level.
func TestFormatCommandNameAlsoAs(t *testing.T) {
	tests := []struct {
		name          string
		aliases       []string
		alsoAs        string
		hasAnnotation bool
		isTopLevel    bool
		want          string
	}{
		{"no aliases, top level annotation", nil, "sandbox run", true, true, "run (also: sandbox run)"},
		{"no aliases, nested annotation omitted", nil, "sandbox run", true, false, "run"},
		{"no aliases, top level no annotation", nil, "", false, true, "run"},
		{"aliases, top level no annotation", []string{"ls"}, "", false, true, "run (aliases: ls)"},
		{"aliases, nested annotation omitted", []string{"ls"}, "sandbox list", true, false, "run (aliases: ls)"},
		{
			"aliases, top level annotation",
			[]string{"ls"},
			"sandbox list",
			true,
			true,
			"run (aliases: ls, also: sandbox list)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "run", Aliases: tc.aliases}
			if tc.hasAnnotation {
				cmd.Annotations = map[string]string{annotationAlsoAs: tc.alsoAs}
			}
			if got := formatCommandName(cmd, tc.isTopLevel); got != tc.want {
				t.Errorf("formatCommandName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrintTreeContainsSandboxCompact(t *testing.T) {
	out := strings.Join(buildTree(t).InfoCalls, "\n")
	if !strings.Contains(out, "Merge the copy-on-write disk layers of project sandbox VMs") {
		t.Errorf("expected the sandbox compact row in tree output:\n%s", out)
	}
}
