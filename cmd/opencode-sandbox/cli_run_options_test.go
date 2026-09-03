package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/notify"
	"github.com/inoio/opencode-sandbox/internal/sandbox/mounts"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

// extractRunOptionsReapPolicy tests that extractRunOptions populates
// ReapPolicy and IdleTimeout from a launcherconfig.Config stored in
// the command's context (wired via PersistentPreRunE in production).

// A zero-value Config produces the expected defaults.
func TestExtractRunOptionsDefaults(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{} // zero value
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	expectedPolicy := options.ReapPolicy{
		AutoStopOnActiveSessions: false,
		MaxSessionRetries:        10,
	}
	if opts.ReapPolicy != expectedPolicy {
		t.Errorf("ReapPolicy = %+v; want %+v", opts.ReapPolicy, expectedPolicy)
	}
	if opts.IdleTimeout != 10*time.Second {
		t.Errorf("IdleTimeout = %v; want 10s", opts.IdleTimeout)
	}
}

// AutoStopOnActiveSessions: true propagates to ReapPolicy.
func TestExtractRunOptionsAutoStopOnActiveSessions(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopOnActiveSessions: true}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if !opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Errorf("ReapPolicy.AutoStopOnActiveSessions = false; want true")
	}
}

// Custom AutoStopMaxSessionRetries propagates to ReapPolicy.
func TestExtractRunOptionsCustomMaxSessionRetries(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopMaxSessionRetries: 5}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.ReapPolicy.MaxSessionRetries != 5 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 5", opts.ReapPolicy.MaxSessionRetries)
	}
}

func TestExtractRunOptionsAutoStopTimeout(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopTimeout: 30 * time.Second}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsWithoutConfig(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildCommandWithoutLauncherConfig(ui)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Error("unexpected ReapPolicy.AutoStopOnActiveSessions without config")
	}
	if opts.ReapPolicy.MaxSessionRetries != 0 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 0 (zero value)", opts.ReapPolicy.MaxSessionRetries)
	}
	if opts.IdleTimeout != 0 {
		t.Errorf("IdleTimeout = %v; want 0 (zero value)", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsShellCommand(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{
		AutoStopOnActiveSessions:  true,
		AutoStopMaxSessionRetries: 7,
		AutoStopTimeout:           25 * time.Second,
	}
	cmd := buildShellCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	expectedPolicy := options.ReapPolicy{
		AutoStopOnActiveSessions: true,
		MaxSessionRetries:        7,
	}
	if opts.ReapPolicy != expectedPolicy {
		t.Errorf("ReapPolicy = %+v; want %+v", opts.ReapPolicy, expectedPolicy)
	}
	if opts.IdleTimeout != 25*time.Second {
		t.Errorf("IdleTimeout = %v; want 25s", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsServeOnly(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.ServeOnly {
		t.Errorf("expected ServeOnly=true when --serve-only passed, got false")
	}

	cmd2 := buildRunCmd(&termio.Mock{})
	opts2, err := extractRunOptions(cmd2, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts2.ServeOnly {
		t.Errorf("expected ServeOnly=false by default, got true")
	}
}

func TestExtractRunOptionsRootFlag(t *testing.T) {
	shellCmd := buildShellCmd(&termio.Mock{})
	if err := shellCmd.Flags().Set(flagRoot, "true"); err != nil {
		t.Fatalf("set root: %v", err)
	}
	opts, err := extractRunOptions(shellCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.Root {
		t.Errorf("expected Root=true when --root passed, got false")
	}
}

func TestExtractRunOptionsRootFlagNotSet(t *testing.T) {
	runCmd := buildRunCmd(&termio.Mock{})
	opts, err := extractRunOptions(runCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Root {
		t.Errorf("expected Root=false when --root not registered, got true")
	}
}

// buildCommandWithLauncherConfig builds a "run" command and injects a
// viperconfig.Resolver built from lc into the context, mimicking what
// PersistentPreRunE does in production.
func buildCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	cmd := buildRunCmd(ui)
	rootCtx := context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(lc),
	)
	cmd.SetContext(rootCtx)
	return cmd
}

// buildShellCommandWithLauncherConfig builds a "shell" command and injects
// a resolver built from lc, verifying the shell path.
func buildShellCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	pet := buildShellCmd(ui)
	petCtx := context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(lc),
	)
	pet.SetContext(petCtx)
	return pet
}

// buildCommandWithoutLauncherConfig builds a "run" command with no resolver
// in its context, exercising the absent-config path.
func buildCommandWithoutLauncherConfig(ui termio.UI) *cobra.Command {
	cmd := buildRunCmd(ui)
	return cmd
}

func TestExtractRunOptionsInvalidTmpSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagTmpSize, "bogus"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --tmp-size")
	}
	if want := "invalid --tmp-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsInvalidDiskSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDiskSize, "bogus"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --disk-size")
	}
	if want := "invalid --disk-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsValidSizeFlags(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagTmpSize, "4G"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	if err := cmd.Flags().Set(flagDiskSize, "16G"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for valid flags, got: %v", err)
	}
	if opts.TmpSize != "4G" {
		t.Errorf("TmpSize = %q, want 4G", opts.TmpSize)
	}
	if opts.DiskSize != "16G" {
		t.Errorf("DiskSize = %q, want 16G", opts.DiskSize)
	}
}

func TestExtractRunOptionsInvalidWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagWorkspaceQuota, "bogus"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --workspace-quota")
	}
	if want := "invalid --workspace-quota"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagWorkspaceQuota, "32G"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for valid workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "32G" {
		t.Errorf("WorkspaceQuota = %q, want 32G", opts.WorkspaceQuota)
	}
}

func TestExtractRunOptionsWorkspaceQuotaDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for default workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "16G" {
		t.Errorf("WorkspaceQuota = %q, want 16G default", opts.WorkspaceQuota)
	}
}

func TestRunCommandHasNoOpenCodeVersionFlag(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdRun, "--help")
	foundCmd, _, err := cmd.Find([]string{cmdRun})
	if err != nil {
		t.Fatalf("Find %q: %v", cmdRun, err)
	}
	if flag := foundCmd.Flags().Lookup(flagOpenCodeVersion); flag != nil {
		t.Errorf("run command must NOT have --opencode-version flag, got %q", flag.Name)
	}
}

func TestRunAndShellHaveDindFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
			t.Errorf("%s command must have --dind flag", name)
		}
	}
}

func TestExtractRunOptionsPropagatesDind(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	rootCtx := context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{Dind: true}),
	)
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.Dind {
		t.Error("opts.Dind = false, want true from resolver")
	}
}

func TestExtractRunOptionsNetworkFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagNetwork, "none"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Network.Profile != network.ProfileNone {
		t.Fatalf("Network.Profile = %q, want none", opts.Network.Profile)
	}
}

func TestExtractRunOptionsNetworkFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_NETWORK_PROFILE", "private")
	cmd := buildRunCmd(&termio.Mock{})
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Network.Profile != network.ProfilePrivate {
		t.Fatalf("Network.Profile = %q, want private (from resolver)", opts.Network.Profile)
	}
}

func TestExtractRunOptionsNetworkInvalid(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagNetwork, "bogus"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	if _, err := extractRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for invalid --network profile")
	}
}

// Configured mounts are resolved into RunOptions, keyed by guest path.
func TestExtractRunOptionsResolvesMounts(t *testing.T) {
	ui := &termio.Mock{}
	source := t.TempDir()
	lc := launcherconfig.Config{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: source},
	}}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	mount, ok := opts.Mounts["/home/dev/.m2"]
	if !ok {
		t.Fatalf("Mounts = %+v; want a /home/dev/.m2 entry", opts.Mounts)
	}
	if mount.Source != source {
		t.Errorf("Source = %q; want %q", mount.Source, source)
	}
}

// An invalid mount must fail the command rather than silently starting a VM
// without the mount.
func TestExtractRunOptionsRejectsInvalidMount(t *testing.T) {
	cases := []struct {
		name  string
		mount mounts.Mounts
	}{
		{"missing source", mounts.Mounts{"/home/dev/.m2": {Source: "/definitely/not/here"}}},
		{"reserved target", mounts.Mounts{"/workspace": {Source: "/tmp"}}},
		{"relative target", mounts.Mounts{"relative": {Source: "/tmp"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &termio.Mock{}
			lc := launcherconfig.Config{Mounts: tc.mount}
			cmd := buildCommandWithLauncherConfig(ui, lc)

			if _, err := extractRunOptions(cmd, ui); err == nil {
				t.Fatal("expected extractRunOptions to fail")
			}
		})
	}
}

func TestExtractRunOptionsAgentDefault(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Agent != "opencode" {
		t.Errorf("Agent = %q; want %q", opts.Agent, "opencode")
	}
}

func TestExtractRunOptionsUnknownAgent(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagAgent, "bogus"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for unknown --agent")
	}
	if want := "unknown agent"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

// The agent can be selected via the OPENCODE_SANDBOX_AGENT env var.
func TestExtractRunOptionsAgentFromEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_AGENT", "opencode")
	cmd := buildRunCmd(&termio.Mock{})
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Agent != "opencode" {
		t.Errorf("Agent = %q, want opencode from env", opts.Agent)
	}
}

func TestExtractRunOptionsAgentPI(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Agent != "pi" {
		t.Errorf("Agent = %q; want pi", opts.Agent)
	}
}

func TestExtractRunOptionsAgentClaudeCode(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Agent != "claude-code" {
		t.Errorf("Agent = %q; want claude-code", opts.Agent)
	}
}

func TestExtractRunOptionsRejectsWorktreeForPI(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(flagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for pi with --worktree")
	}
	if want := `not supported by agent "pi"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsRejectsServeOnlyForClaudeCode(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for claude-code with --serve-only")
	}
	if want := `not supported by agent "claude-code"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

// noDaemonAgent is a minimal agent.Agent that intentionally does not implement
// DaemonProvider, so validateAgentFlags can be exercised without polluting the
// global agent registry.
type noDaemonAgent struct{ name string }

func (a noDaemonAgent) Name() string             { return a.name }
func (noDaemonAgent) ConfigDirName() string      { return "nod" }
func (noDaemonAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

func TestValidateAgentFlagsRejectsNonDaemonWorktree(t *testing.T) {
	a := noDaemonAgent{name: "nod"}
	err := validateAgentFlags(a, options.RunOptions{Worktree: options.WorktreeSpec{Name: "x"}})
	if err == nil {
		t.Fatal("expected error for non-daemon agent with --worktree")
	}
	if want := `not supported by agent "nod"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestValidateAgentFlagsRejectsNonDaemonServeOnly(t *testing.T) {
	a := noDaemonAgent{name: "nod"}
	err := validateAgentFlags(a, options.RunOptions{ServeOnly: true})
	if err == nil {
		t.Fatal("expected error for non-daemon agent with --serve-only")
	}
}

func TestValidateAgentFlagsAllowsNonDaemonPlainRun(t *testing.T) {
	a := noDaemonAgent{name: "nod"}
	if err := validateAgentFlags(a, options.RunOptions{}); err != nil {
		t.Fatalf("unexpected error for non-daemon agent plain run: %v", err)
	}
}

func TestValidateAgentFlagsAllowsOpencodeWithDaemonFlags(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not found")
	}
	opts := options.RunOptions{Worktree: options.WorktreeSpec{Name: "x"}, ServeOnly: true}
	if err := validateAgentFlags(a, opts); err != nil {
		t.Fatalf("unexpected error for opencode with --worktree/--serve-only: %v", err)
	}
}

// The bare --notify flag must resolve to "on" via NoOptDefVal once parsed.
func TestNotifyFlagBareMeansOn(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	cmd.SetArgs([]string{"--notify"})
	if err := cmd.ParseFlags([]string{"--notify"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !cmd.Flags().Changed(flagNotify) {
		t.Fatal("--notify should be marked changed after parse")
	}
	if got, _ := cmd.Flags().GetString(flagNotify); got != "on" {
		t.Errorf("notify flag after parse = %q, want on (NoOptDefVal)", got)
	}
}

func TestExtractRunOptionsNotifyDefaultOff(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildCommandWithLauncherConfig(ui, launcherconfig.Config{})
	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Notify.Active() {
		t.Errorf("default notify should be inactive, got %+v", opts.Notify)
	}
}

func TestExtractRunOptionsNotifyFromConfig(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{Notify: launcherconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}}
	cmd := buildCommandWithLauncherConfig(ui, lc)
	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("notify from config = %+v, want desktop+system", opts.Notify)
	}
}

func TestExtractRunOptionsNotifyEnvOverridesConfig(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	lc := launcherconfig.Config{Notify: launcherconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}}
	cmd := buildCommandWithLauncherConfig(ui, lc)
	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("env override audio: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestExtractRunOptionsNotifyFlagOverridesEnv(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagNotify, "desktop"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}))
	cmd.SetContext(rootCtx)
	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioOff {
		t.Errorf("flag override desktop: got %+v, want desktop on audio off", opts.Notify)
	}
}

func TestExtractRunOptionsNotifyInvalidValue(t *testing.T) {
	t.Setenv(notifyEnvVar, "loud")
	ui := &termio.Mock{}
	cmd := buildCommandWithLauncherConfig(ui, launcherconfig.Config{})
	if _, err := extractRunOptions(cmd, ui); err == nil {
		t.Fatal("expected error for invalid OPENCODE_SANDBOX_NOTIFY value")
	}
}

func TestExtractRunOptionsNotifyRejectedForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(flagNotify, "on"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}))
	cmd.SetContext(rootCtx)
	if _, err := extractRunOptions(cmd, ui); err == nil {
		t.Fatal("expected error: --notify is not supported by interactive agent pi")
	}
}

func TestExtractRunOptionsNotifyConfigOnlyWarnsForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{Notify: launcherconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioOff,
		OnInput: true, OnDone: true, OnError: true,
	}}
	cmd := buildCommandWithLauncherConfig(ui, lc)
	if err := cmd.Flags().Set(flagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.Notify.Active() {
		t.Errorf("notify should be disabled for interactive agent, got %+v", opts.Notify)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "notifications not supported") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warning about unsupported notifications, got %v", ui.WarnCalls)
	}
}
