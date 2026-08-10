package main

import (
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
	"gitlab.inoio.de/inoio/opencode-msb/internal/testutil"
	launcherconfig "gitlab.inoio.de/inoio/opencode-msb/internal/viperconfig"
)

// buildTree sets up a test UI, builds the root command, renders the tree,
// and returns the UI for use in test assertions.
func buildTree(t *testing.T) *termio.Mock {
	t.Helper()
	ui := testutil.TermUIMock(t)
	root := buildRootCmd(&ui)
	printTree(root, &ui)
	return &ui
}

func TestPrintTreeStartsWithRootCommandName(t *testing.T) {
	testUI := buildTree(t)
	lines := testUI.InfoCalls
	if len(lines) == 0 {
		t.Fatal("expected at least one line in tree output")
	}
	if lines[0] != "opencode-msb" {
		t.Errorf("expected first line to be %q, got %q", "opencode-msb", lines[0])
	}
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
func TestPrintTreeContainsAllCommands(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume", "stop", "kill"}
	for _, cmd := range expected {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected tree to contain %q, got:\n%s", cmd, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeContainsCommandDescriptions(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	descs := []string{
		"Run opencode in a microsandbox VM",
		"Check prerequisites",
		"Build or rebuild the runner image",
		"List sandboxes for this host",
		"Start sandbox and open a shell (debug)",
		"Inspect opencode configuration",
		"Manage runner images",
		"Manage home volumes",
		"Manage sandboxes",
		"Print merged opencode config with source paths",
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
func TestPrintTreeContainsFlagDescriptions(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	flagDescs := []string{
		"Assume yes to all prompts",
		"Show debug-level output",
		"Suppress non-error output",
		"Run in an opencode worktree for the given branch name",
		"Rebuild the runner image before starting",
		"Dry run without starting anything",
		"Number of CPUs (default: all)",
		"Memory limit",
		"Size of the /tmp tmpfs in the sandbox",
		"Size of the project VM root disk (e.g. 16G)",
		"Do not pass --auto to opencode",
	}
	for _, d := range flagDescs {
		if !strings.Contains(out, d) {
			t.Errorf("expected flag description %q in tree output:\n%s", d, out)
		}
	}
}

// skipcheck: canonical tree/version tests moved to cli_tree_test.go
func TestPrintTreeStringFlagsHaveValuePlaceholders(t *testing.T) {
	testUI := buildTree(t)
	out := strings.Join(testUI.InfoCalls, "\n")
	expected := []string{
		"--branch <BRANCH>",
		"--cpus <CPUS>",
		"--memory <MEMORY>",
		"--tmp-size <TMP_SIZE>",
		"--disk-size <DISK_SIZE>",
		"--user <USER>",
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
		"--verbose <VERBOSE>",
		"--quiet <QUIET>",
		"--tree <TREE>",
		"--version <VERSION>",
		"--rebuild <REBUILD>",
		"--dry-run <DRY_RUN>",
		"--no-auto <NO_AUTO>",
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
		"-v, --verbose",
		"-q, --quiet",
		"-b, --branch <BRANCH>",
		"-r, --rebuild",
		"-n, --dry-run",
		"-c, --cpus <CPUS>",
		"-m, --memory <MEMORY>",
		"-u, --user <USER>",
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

func TestRootHasGlobalFlags(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	flags := []string{"yes", "verbose", "quiet"}
	for _, f := range flags {
		if root.PersistentFlags().Lookup(f) == nil {
			t.Errorf("expected persistent flag --%s on root", f)
		}
	}
}

func TestRunCommandHasExpectedFlags(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	flags := []string{"branch", "cpus", "memory", "tmp-size", "disk-size", "rebuild", "dry-run", "no-auto"}
	for _, f := range flags {
		if runCmd.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s on run command", f)
		}
	}
}

func TestRunCommandFlagShortcuts(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	shortcuts := map[string]string{
		"b": "branch", "c": "cpus", "m": "memory",
		"r": "rebuild", "n": "dry-run", "y": "yes",
		"v": "verbose", "q": "quiet",
	}
	for short, long := range shortcuts {
		f := runCmd.Flags().ShorthandLookup(short)
		if f == nil {
			f = root.PersistentFlags().ShorthandLookup(short)
		}
		if f == nil {
			t.Errorf("expected shorthand -%s for --%s", short, long)
		}
	}
}

func TestImageBuildNounFormExists(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	imageCmd, _, _ := root.Find([]string{"image"})
	if imageCmd == nil {
		t.Fatal("expected image command")
	}
	buildCmd, _, _ := imageCmd.Find([]string{"build"})
	if buildCmd == nil {
		t.Fatal("expected image build subcommand")
	}
}

func TestApplyLauncherConfigSetsUnsetFlags(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	lc := launcherconfig.Config{CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G", Yes: true, Verbose: true}
	keys := map[string]bool{
		"cpus":      true,
		"memory":    true,
		"tmp-size":  true,
		"disk-size": true,
		"yes":       true,
		"verbose":   true,
	}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8(flagCpus)
	if cpus != 4 {
		t.Errorf("expected cpus 4, got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString(flagMemory)
	if mem != "8G" {
		t.Errorf("expected memory 8G, got %q", mem)
	}
	tmp, _ := runCmd.Flags().GetString(flagTmpSize)
	if tmp != "4G" {
		t.Errorf("expected tmp-size 4G, got %q", tmp)
	}
	disk, _ := runCmd.Flags().GetString(flagDiskSize)
	if disk != "32G" {
		t.Errorf("expected disk-size 32G, got %q", disk)
	}
	yes, _ := root.PersistentFlags().GetBool(pFlagYes)
	if !yes {
		t.Error("expected yes=true")
	}
	verbose, _ := root.PersistentFlags().GetBool(pFlagVerbose)
	if !verbose {
		t.Error("expected verbose=true")
	}
}

func TestCLICombinedShortFlagsActivateVerbose(t *testing.T) {
	for _, args := range [][]string{
		{"prune", "--age", "1m", "-nv"},
		{"prune", "--age", "1m", "-n", "-v"},
		{"prune", "--age", "1m", "--dry-run", "--verbose"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			sandbox.WithMockConfigPaths(t)
			ui := &termio.Mock{}
			mock := &sandbox.MockMsbClient{}
			sandbox.WithMsbMock(t, mock)
			docker.WithNoopDockerMock(t)

			root := buildRootCmd(ui)
			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ui.Level() != termio.LevelVerbose {
				t.Errorf("expected LevelVerbose, got %v", ui.Level())
			}
		})
	}
}

func TestCLIPersistentYesAffectsUIAfterSubcommand(t *testing.T) {
	sandbox.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sandbox.WithMsbMock(t, mock)
	docker.WithNoopDockerMock(t)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"prune", "--age", "1m", "-n", "-y"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ui.AssumeYes() {
		t.Error("expected AssumeYes=true for -y after subcommand")
	}
}

func TestApplyLauncherConfigRespectsCLIOverrides(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	if err := runCmd.ParseFlags(
		[]string{"--cpus", "2", "--memory", "1G", "--tmp-size", "512M", "--disk-size", "16G", "--yes=false"},
	); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	lc := launcherconfig.Config{CPUs: 8, Memory: "16G", TmpSize: "8G", DiskSize: "64G", Yes: true, Verbose: true}
	keys := map[string]bool{
		"cpus":      true,
		"memory":    true,
		"tmp-size":  true,
		"disk-size": true,
		"yes":       true,
		"verbose":   true,
	}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8(flagCpus)
	if cpus != 2 {
		t.Errorf("expected cpus 2 (CLI override), got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString(flagMemory)
	if mem != "1G" {
		t.Errorf("expected memory 1G (CLI override), got %q", mem)
	}
	tmp, _ := runCmd.Flags().GetString(flagTmpSize)
	if tmp != "512M" {
		t.Errorf("expected tmp-size 512M (CLI override), got %q", tmp)
	}
	disk, _ := runCmd.Flags().GetString(flagDiskSize)
	if disk != "16G" {
		t.Errorf("expected disk-size 16G (CLI override), got %q", disk)
	}
	yes, _ := runCmd.Flags().GetBool(pFlagYes)
	if yes {
		t.Error("expected yes=false (CLI override)")
	}
	verbose, _ := runCmd.Flags().GetBool(pFlagVerbose)
	if !verbose {
		t.Error("expected verbose=true from config")
	}
}

func TestNewConfigSetsUserDirs(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	sandbox.WithRealConfigPaths(t)
	cfg := newConfig()
	if cfg.UserStateDir() != "/testhome/.local/state/opencode-msb" {
		t.Errorf("unexpected state dir: %q", cfg.UserStateDir())
	}
	if cfg.UserConfigDir() != "/testhome/.config/opencode-msb" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir())
	}
	if cfg.UserOpencodeConfigDir() != "/testhome/.config/opencode-msb/opencode" {
		t.Errorf("unexpected opencode config dir: %q", cfg.UserOpencodeConfigDir())
	}
	if cfg.UserCacheDir() != "/testhome/.cache/opencode-msb" {
		t.Errorf("unexpected cache dir: %q", cfg.UserCacheDir())
	}
}

func TestNewConfigHonorsXdgEnv(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	sandbox.WithRealConfigPaths(t)
	cfg := newConfig()
	if cfg.UserStateDir() != "/xdg/state/opencode-msb" {
		t.Errorf("unexpected state dir: %q", cfg.UserStateDir())
	}
	if cfg.UserConfigDir() != "/xdg/config/opencode-msb" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir())
	}
	if cfg.UserOpencodeConfigDir() != "/xdg/config/opencode-msb/opencode" {
		t.Errorf("unexpected opencode config dir: %q", cfg.UserOpencodeConfigDir())
	}
	if cfg.UserCacheDir() != "/xdg/cache/opencode-msb" {
		t.Errorf("unexpected cache dir: %q", cfg.UserCacheDir())
	}
}

func TestRunAndGetShellUserFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"shell default empty", []string{}, ""},
		{"shell --user bob", []string{"--user", "bob"}, "bob"},
		{"shell -u bob", []string{"-u", "bob"}, "bob"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testUI := testutil.TermUIMock(t)
			root := buildRootCmd(&testUI)
			cmd, _, _ := root.Find([]string{"shell"})
			if cmd == nil {
				t.Fatal("expected command")
			}
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags failed: %v", err)
			}
			got, _ := cmd.Flags().GetString(flagUser)
			if got != tt.want {
				t.Errorf("user=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestStopCommandExists(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
}

func TestKillCommandExists(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	killCmd, _, _ := root.Find([]string{"kill"})
	if killCmd == nil {
		t.Fatal("expected kill command")
	}
}

func TestApplyLauncherConfigSetsDiskSize(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	keys := map[string]bool{"disk-size": true}
	lc := launcherconfig.Config{DiskSize: "32G"}
	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}
	got, _ := runCmd.Flags().GetString(flagDiskSize)
	if got != "32G" {
		t.Errorf("disk-size = %q, want 32G", got)
	}
}

func TestStopCommandHasForceFlag(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	stopCmd, _, _ := root.Find([]string{"stop"})
	if stopCmd == nil {
		t.Fatal("expected stop command")
	}
	if stopCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on stop command")
	}
}

func TestKillCommandHasForceFlag(t *testing.T) {
	testUI := testutil.TermUIMock(t)
	root := buildRootCmd(&testUI)
	killCmd, _, _ := root.Find([]string{"kill"})
	if killCmd == nil {
		t.Fatal("expected kill command")
	}
	if killCmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag on kill command")
	}
}
