package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/projectguard"
	"github.com/inoio/agents-sandbox/internal/projecttrust"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/upgrade"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// earlyActingProjectConfig sets every key that acts before PrepareSandbox runs.
const earlyActingProjectConfig = "yes: true\nquiet: true\nlog-level: error\n" +
	"upgrade:\n  mode: auto\n  channel: canary\nauto-prune-age: 1h\n"

// writeProjectConfig writes the project layer's launcher config file.
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	dir := configpaths.Get().ProjectConfigDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gateFixture runs the gate for the named command of a real root command.
type gateFixture struct {
	root *cobra.Command
	ui   *termiotest.Mock
}

func newGateFixture(t *testing.T) gateFixture {
	t.Helper()
	configpathstest.WithMockConfigPaths(t)
	ui := &termiotest.Mock{}
	return gateFixture{root: buildRootCmd(ui), ui: ui}
}

func (f gateFixture) gate(t *testing.T, args ...string) error {
	t.Helper()
	cmd, flags, err := f.root.Find(args)
	if err != nil {
		t.Fatalf("find %v: %v", args, err)
	}
	if err := cmd.ParseFlags(flags); err != nil {
		t.Fatalf("parse flags %v: %v", flags, err)
	}
	identity, err := resolveIdentity(".")
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return gateProjectConfig(cmd, f.ui, identity, configpaths.Get)
}

func projectLayerInEffect() bool {
	return configpaths.Get().ProjectConfigDir() != ""
}

func approvalRecorded(t *testing.T) bool {
	t.Helper()
	identity, err := resolveIdentity(".")
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := projecttrust.LoadApproval(identity.CheckoutSlug)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestCommandAppliesProjectConfigNamesTheRunAndBuildCommands walks the real
// command tree, so a new command is classified on purpose: only the commands
// that turn project input into VM or image state ask for approval.
func TestCommandAppliesProjectConfigNamesTheRunAndBuildCommands(t *testing.T) {
	want := []string{
		"agents-sandbox", "agents-sandbox build", "agents-sandbox image build", "agents-sandbox run",
		"agents-sandbox sandbox run", "agents-sandbox sandbox shell", "agents-sandbox shell",
	}
	var got []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if commandAppliesProjectConfig(cmd) {
			got = append(got, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(buildRootCmd(&termiotest.Mock{}))
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("commands asking for approval = %q, want %q", got, want)
	}
}

func TestGateUsesAnApprovedProjectLayerSilently(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")
	approveProjectConfig(t)
	f.ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		t.Fatal("prompted for an approved project config")
		return "", nil
	}

	if err := f.gate(t, cmdRun); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !projectLayerInEffect() || len(f.ui.WarnCalls) != 0 {
		t.Errorf("approved layer: in effect %v, warnings %q; want in effect and silent",
			projectLayerInEffect(), f.ui.WarnCalls)
	}
}

func TestGateApprovesAChangedLayerInteractively(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")
	f.ui.IsInteractiveResult = true
	f.ui.SetAssumeYes(true)
	prompted := false
	f.ui.SelectFn = func(message string, _ []termio.Choice, _ string) (string, error) {
		prompted = true
		if !strings.Contains(message, `+ memory: "8G"`) {
			t.Errorf("prompt %q does not show the change", message)
		}
		return "a", nil
	}

	if err := f.gate(t, cmdRun, "--yes"); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !prompted {
		t.Fatal("--yes answered the approval prompt")
	}
	if !projectLayerInEffect() || !approvalRecorded(t) {
		t.Error("an approved change is not in effect or not recorded")
	}
}

func TestGateDeclinedChangeAborts(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")
	f.ui.IsInteractiveResult = true
	f.ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "q", nil }

	err := f.gate(t, cmdShell)
	if !errors.Is(err, projecttrust.ErrDeclined) || !errors.Is(err, termio.ErrCancelled) {
		t.Fatalf("gate = %v, want a declined cancellation", err)
	}
	if approvalRecorded(t) {
		t.Error("a declined change was recorded")
	}
}

func TestGateRefusesAChangedLayerWithoutATerminal(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")

	for _, args := range [][]string{{cmdRun}, {cmdRun, "--serve-only"}, {cmdImage, cmdBuild}} {
		if err := f.gate(t, args...); !errors.Is(err, projecttrust.ErrApprovalRequired) {
			t.Errorf("gate %v = %v, want ErrApprovalRequired", args, err)
		}
	}
	if approvalRecorded(t) {
		t.Error("a refused change was recorded")
	}
}

// TestGateDryRunReportsWithoutPromptingOrRecording pins the preview: it shows
// that approval is needed, never asks or writes, and previews without the layer.
func TestGateDryRunReportsWithoutPromptingOrRecording(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")
	f.ui.IsInteractiveResult = true
	f.ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		t.Fatal("a dry run prompted")
		return "", nil
	}

	if err := f.gate(t, cmdRun, "--dry-run"); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if projectLayerInEffect() || approvalRecorded(t) {
		t.Error("a dry run used or recorded the unapproved layer")
	}
	if !strings.Contains(strings.Join(f.ui.WarnCalls, "\n"), `+ memory: "8G"`) {
		t.Errorf("warnings %q do not report the change", f.ui.WarnCalls)
	}
}

// TestGateOtherCommandsIgnoreAChangedLayer pins that a command that does not
// apply the project config runs without it after one warning, and that none of
// the keys acting before PrepareSandbox reach it.
func TestGateOtherCommandsIgnoreAChangedLayer(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, earlyActingProjectConfig)
	defaults, err := launcherconfig.NewResolver(nil, configpaths.WithoutProjectLayer(configpaths.Get()), "")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.gate(t, cmdConfig, cmdPaths); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if projectLayerInEffect() || len(f.ui.WarnCalls) != 1 {
		t.Fatalf("ignored layer: in effect %v, warnings %q; want excluded with one warning",
			projectLayerInEffect(), f.ui.WarnCalls)
	}
	r, err := launcherconfig.NewResolver(nil, configpaths.Get(), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Yes() || r.Quiet() || r.LogLevel() != defaults.LogLevel() ||
		r.UpgradeMode() != defaults.UpgradeMode() || r.UpgradeChannel() == upgrade.ChannelCanary ||
		r.AutoPruneAge() == time.Hour {
		t.Errorf("an unapproved project key took effect: %+v", r.Config())
	}
}

// TestGateConfigPathsWarningNamesTheConfigFileOnce pins F5: the flat warning a
// command that ignores an unapproved layer prints lists the project config file
// once, not once per projected role.
func TestGateConfigPathsWarningNamesTheConfigFileOnce(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")

	if err := f.gate(t, cmdConfig, cmdPaths); err != nil {
		t.Fatalf("gate: %v", err)
	}
	path := filepath.Join(configpaths.Get().ProjectConfigDir(), "config.yaml")
	warning := strings.Join(f.ui.WarnCalls, "\n")
	if got := strings.Count(warning, path); got != 1 {
		t.Errorf("warning names %q %d times, want once:\n%s", path, got, warning)
	}
}

// TestGateDecidesAfreshForEachCommand pins that an earlier exclusion does not
// outlive its command: after approval, the next command uses the layer.
func TestGateDecidesAfreshForEachCommand(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: 8G\n")
	if err := f.gate(t, cmdConfig, cmdPaths); err != nil || projectLayerInEffect() {
		t.Fatalf("first gate: err %v, in effect %v; want excluded", err, projectLayerInEffect())
	}
	approveProjectConfig(t)
	if err := f.gate(t, cmdConfig, cmdPaths); err != nil || !projectLayerInEffect() {
		t.Errorf("second gate: err %v, in effect %v; want the approved layer", err, projectLayerInEffect())
	}
}

func TestGateReportsAnUnreadableProjectConfig(t *testing.T) {
	f := newGateFixture(t)
	writeProjectConfig(t, "memory: [unclosed\n")
	if err := f.gate(t, cmdRun); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("gate = %v, want the project config's parse error", err)
	}
}

// TestExecuteRestoresThePathFactory pins that the gate's decision does not
// outlive the command it was made for.
func TestExecuteRestoresThePathFactory(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeProjectConfig(t, "memory: 8G\n")
	ui := &termiotest.Mock{}
	if err := execute([]string{cmdConfig, cmdPaths}, ui); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !projectLayerInEffect() {
		t.Error("the exclusion outlived the command")
	}
}

// TestExecuteStopsOnAGateError pins that a command does not run when the gate
// refuses: a run without a terminal never reaches the runtime.
func TestExecuteStopsOnAGateError(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdRun)
	writeProjectConfig(t, "memory: 8G\n")
	if err := cmd.Execute(); !errors.Is(err, projecttrust.ErrApprovalRequired) {
		t.Errorf("run = %v, want the gate's ErrApprovalRequired", err)
	}
}

// TestAllAgentConfigDirNamesCoversEveryAgent pins that the gate reads the
// project config dirs of every registered agent, not only the selected one.
func TestAllAgentConfigDirNamesCoversEveryAgent(t *testing.T) {
	names := allAgentConfigDirNames()
	for _, name := range agent.Names() {
		profile, _ := agent.Lookup(name)
		if !slices.Contains(names, profile.ConfigDirName()) {
			t.Errorf("config dir names %q miss agent %s's %q", names, name, profile.ConfigDirName())
		}
	}
}

// TestGateGuardsTheReadsOfAnApprovedLayer pins that an approved layer, whether
// recorded before or approved now, is read only as approved: a change or a
// deletion after the gate is refused by the command's own config read.
func TestGateGuardsTheReadsOfAnApprovedLayer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, gateFixture)
	}{
		{"recorded approval", func(t *testing.T, _ gateFixture) { approveProjectConfig(t) }},
		{"approved now", func(_ *testing.T, f gateFixture) {
			f.ui.IsInteractiveResult = true
			f.ui.SelectFn = func(string, []termio.Choice, string) (string, error) { return "a", nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGateFixture(t)
			t.Cleanup(projectguard.Install(nil, nil))
			writeProjectConfig(t, "memory: 8G\n")
			tc.prepare(t, f)
			if err := f.gate(t, cmdRun); err != nil {
				t.Fatalf("gate: %v", err)
			}
			writeProjectConfig(t, "memory: 8G\nyes: true\n")
			if _, err := launcherconfig.NewResolver(
				nil,
				configpaths.Get(),
				"",
			); !errors.Is(
				err,
				projectguard.ErrNotApproved,
			) {
				t.Errorf("NewResolver after a change = %v, want ErrNotApproved", err)
			}
			if err := os.Remove(filepath.Join(configpaths.Get().ProjectConfigDir(), "config.yaml")); err != nil {
				t.Fatal(err)
			}
			_, err := launcherconfig.NewResolver(nil, configpaths.Get(), "")
			if !errors.Is(err, projectguard.ErrNotApproved) {
				t.Errorf("NewResolver after a deletion = %v, want ErrNotApproved", err)
			}
		})
	}
}
