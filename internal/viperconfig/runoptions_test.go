package viperconfig

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// registerRunFlags registers the flags BuildRunOptions reads directly (via the
// exported consts) plus the config-backed flags, mirroring production defaults.
// --notify gets NoOptDefVal="on" so a bare --notify means "on".
func registerRunFlags(cmd *cobra.Command) {
	cmd.Flags().String(FlagWorktree, "", "worktree")
	cmd.Flags().Bool(FlagRebuild, false, "rebuild")
	cmd.Flags().String(FlagAgentVersion, "", "agent-version")
	cmd.Flags().Bool(FlagDryRun, false, "dry-run")
	cmd.Flags().Bool(FlagDryRunVM, false, "dry-run-vm")
	cmd.Flags().Bool(FlagServeOnly, false, "serve-only")
	cmd.Flags().String(FlagAgent, defaultAgentName, "agent")
	cmd.Flags().String(FlagNetwork, "", "network")
	cmd.Flags().StringSlice(FlagDNSServers, nil, "dns")
	cmd.Flags().String(FlagNotify, "", "notify")
	cmd.Flags().Lookup(FlagNotify).NoOptDefVal = "on"

	cmd.Flags().Uint8("cpus", 0, "cpus")
	cmd.Flags().String("memory", "4G", "memory")
	cmd.Flags().String("tmp-size", "2G", "tmp-size")
	cmd.Flags().String("disk-size", "", "disk-size")
	cmd.Flags().String("workspace-quota", "16G", "workspace-quota")
	cmd.Flags().Bool("docker", false, "docker")
	cmd.Flags().Bool("dind", false, "dind")
}

// newRunCommand builds a "run"-style command (no --root flag).
func newRunCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "run"}
	registerRunFlags(cmd)
	return cmd
}

// newShellCommand builds a "shell"-style command, adding the --root flag.
func newShellCommand() *cobra.Command {
	cmd := newRunCommand()
	cmd.Flags().Bool(FlagRoot, false, "root")
	return cmd
}

// mustResolver builds a resolver from a real command, failing the test on error.
func mustResolver(t *testing.T, cmd *cobra.Command) *Resolver {
	t.Helper()
	r, err := NewResolver(cmd, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

// A zero-value Config produces the expected defaults.
func TestBuildRunOptionsDefaults(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{}) // zero value
	cmd := newRunCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
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
	if opts.Network.Profile != network.ProfileNone {
		t.Errorf("Network.Profile = %q; want %q by default", opts.Network.Profile, network.ProfileNone)
	}
}

// AutoStopOnActiveSessions: true propagates to ReapPolicy.
func TestBuildRunOptionsAutoStopOnActiveSessions(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{AutoStopOnActiveSessions: true})
	cmd := newRunCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}

	if !opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Errorf("ReapPolicy.AutoStopOnActiveSessions = false; want true")
	}
}

// Custom AutoStopMaxSessionRetries propagates to ReapPolicy.
func TestBuildRunOptionsCustomMaxSessionRetries(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{AutoStopMaxSessionRetries: 5})
	cmd := newRunCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}

	if opts.ReapPolicy.MaxSessionRetries != 5 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 5", opts.ReapPolicy.MaxSessionRetries)
	}
}

func TestBuildRunOptionsAutoStopTimeout(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{AutoStopTimeout: 30 * time.Second})
	cmd := newRunCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}

	if opts.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", opts.IdleTimeout)
	}
}

func TestBuildRunOptionsNilResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := newRunCommand()

	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
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

func TestBuildRunOptionsShellCommand(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{
		AutoStopOnActiveSessions:  true,
		AutoStopMaxSessionRetries: 7,
		AutoStopTimeout:           25 * time.Second,
	})
	cmd := newShellCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
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

func TestBuildRunOptionsServeOnly(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.ServeOnly {
		t.Errorf("expected ServeOnly=true when --serve-only passed, got false")
	}

	cmd2 := newRunCommand()
	opts2, err := r.BuildRunOptions(cmd2, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts2.ServeOnly {
		t.Errorf("expected ServeOnly=false by default, got true")
	}
}

func TestBuildRunOptionsRootFlag(t *testing.T) {
	shellCmd := newShellCommand()
	if err := shellCmd.Flags().Set(FlagRoot, "true"); err != nil {
		t.Fatalf("set root: %v", err)
	}
	var r *Resolver
	opts, err := r.BuildRunOptions(shellCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.Root {
		t.Errorf("expected Root=true when --root passed, got false")
	}
}

func TestBuildRunOptionsRootFlagNotSet(t *testing.T) {
	runCmd := newRunCommand()
	var r *Resolver
	opts, err := r.BuildRunOptions(runCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Root {
		t.Errorf("expected Root=false when --root not registered, got true")
	}
}

func TestBuildRunOptionsInvalidTmpSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("tmp-size", "bogus"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --tmp-size")
	}
	if want := "invalid --tmp-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRunOptionsInvalidDiskSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("disk-size", "bogus"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --disk-size")
	}
	if want := "invalid --disk-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRunOptionsValidSizeFlags(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("tmp-size", "4G"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	if err := cmd.Flags().Set("disk-size", "16G"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
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

func TestBuildRunOptionsInvalidWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("workspace-quota", "bogus"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --workspace-quota")
	}
	if want := "invalid --workspace-quota"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRunOptionsWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("workspace-quota", "32G"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for valid workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "32G" {
		t.Errorf("WorkspaceQuota = %q, want 32G", opts.WorkspaceQuota)
	}
}

func TestBuildRunOptionsWorkspaceQuotaDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for default workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "16G" {
		t.Errorf("WorkspaceQuota = %q, want 16G default", opts.WorkspaceQuota)
	}
}

func TestBuildRunOptionsPropagatesDocker(t *testing.T) {
	cmd := newRunCommand()
	r := NewResolverWithConfig(Config{Docker: true})
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.Docker {
		t.Error("opts.Docker = false, want true from resolver")
	}
}

func TestBuildRunOptionsDeprecatedDindFlag(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	opts, err := NewResolverWithConfig(Config{}).BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.Docker {
		t.Error("opts.Docker = false, want true from the deprecated --dind flag")
	}
}

func TestBuildRunOptionsDockerFlagWinsOverDind(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	if err := cmd.Flags().Set("docker", "false"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	opts, err := NewResolverWithConfig(Config{}).BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Docker {
		t.Error("opts.Docker = true, want --docker to win over --dind")
	}
}

func TestBuildRunOptionsAgentVersionFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgentVersion, "1.2.3"); err != nil {
		t.Fatalf("set agent-version: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.AgentVersion != "1.2.3" {
		t.Fatalf("AgentVersion = %q, want 1.2.3", opts.AgentVersion)
	}
}

func TestBuildRunOptionsNetworkFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "none"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Network.Profile != network.ProfileNone {
		t.Fatalf("Network.Profile = %q, want none", opts.Network.Profile)
	}
}

func TestBuildRunOptionsNetworkFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "private")
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Network.Profile != network.ProfilePrivate {
		t.Fatalf("Network.Profile = %q, want private (from resolver)", opts.Network.Profile)
	}
}

func TestBuildRunOptionsNetworkInvalid(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "bogus"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	r := mustResolver(t, cmd)
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for invalid --network profile")
	}
}

func TestBuildRunOptionsDNSServersFlag(t *testing.T) {
	for _, name := range []string{"run", "shell"} {
		t.Run(name, func(t *testing.T) {
			var cmd *cobra.Command
			if name == "run" {
				cmd = newRunCommand()
			} else {
				cmd = newShellCommand()
			}
			if err := cmd.Flags().Set(FlagDNSServers, "1.1.1.1,8.8.8.8"); err != nil {
				t.Fatalf("set dns: %v", err)
			}
			var r *Resolver
			opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
			if err != nil {
				t.Fatalf("BuildRunOptions: %v", err)
			}
			if len(opts.Network.DNSServers) != 2 ||
				opts.Network.DNSServers[0] != "1.1.1.1" || opts.Network.DNSServers[1] != "8.8.8.8" {
				t.Fatalf("Network.DNSServers = %v, want [1.1.1.1 8.8.8.8]", opts.Network.DNSServers)
			}
		})
	}
}

func TestBuildRunOptionsDNSCombinedWithNetworkNone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "none"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	if err := cmd.Flags().Set(FlagDNSServers, "1.1.1.1"); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Network.Profile != network.ProfileNone {
		t.Fatalf("Network.Profile = %q, want none", opts.Network.Profile)
	}
	if len(opts.Network.DNSServers) != 1 || opts.Network.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1]", opts.Network.DNSServers)
	}
}

func TestBuildRunOptionsDNSFlagOverridesConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", "9.9.9.9")
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagDNSServers, "1.1.1.1"); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if len(opts.Network.DNSServers) != 1 || opts.Network.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1] (flag overrides env)", opts.Network.DNSServers)
	}
}

func TestBuildRunOptionsDNSFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", "1.1.1.1,8.8.8.8")
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if len(opts.Network.DNSServers) != 2 {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1 8.8.8.8] from resolver", opts.Network.DNSServers)
	}
}

// Configured mounts are resolved into RunOptions, keyed by guest path.
func TestBuildRunOptionsResolvesMounts(t *testing.T) {
	ui := &termio.Mock{}
	source := t.TempDir()
	r := NewResolverWithConfig(Config{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: source},
	}})
	cmd := newRunCommand()

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
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
func TestBuildRunOptionsRejectsInvalidMount(t *testing.T) {
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
			r := NewResolverWithConfig(Config{Mounts: tc.mount})
			cmd := newRunCommand()

			if _, err := r.BuildRunOptions(cmd, ui); err == nil {
				t.Fatal("expected BuildRunOptions to fail")
			}
		})
	}
}

func TestBuildRunOptionsAgentDefault(t *testing.T) {
	cmd := newRunCommand()
	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Agent != defaultAgentName {
		t.Errorf("Agent = %q; want %q", opts.Agent, defaultAgentName)
	}
}

func TestBuildRunOptionsUnknownAgent(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "bogus"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *Resolver
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for unknown --agent")
	}
	if want := "unknown agent"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

// The agent can be selected via the AGENTS_SANDBOX_AGENT env var.
func TestBuildRunOptionsAgentFromEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_AGENT", defaultAgentName)
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Agent != defaultAgentName {
		t.Errorf("Agent = %q, want %s from env", opts.Agent, defaultAgentName)
	}
}

func TestBuildRunOptionsAgentPI(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Agent != "pi" {
		t.Errorf("Agent = %q; want pi", opts.Agent)
	}
}

func TestBuildRunOptionsAgentClaudeCode(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Agent != "claude-code" {
		t.Errorf("Agent = %q; want claude-code", opts.Agent)
	}
}

func TestBuildRunOptionsAgentOpencode2(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "opencode2"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *Resolver
	opts, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Agent != "opencode2" {
		t.Errorf("Agent = %q; want opencode2", opts.Agent)
	}
}

func TestBuildRunOptionsAcceptsWorktreeAndServeOnlyForOpencode2(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "opencode2"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err != nil {
		t.Errorf("expected opencode2 to support --worktree and --serve-only, got %v", err)
	}
}

func TestBuildRunOptionsRejectsWorktreeForPI(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *Resolver
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for pi with --worktree")
	}
	if want := `not supported by agent "pi"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRunOptionsRejectsServeOnlyForClaudeCode(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *Resolver
	_, err := r.BuildRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for claude-code with --serve-only")
	}
	if want := `not supported by agent "claude-code"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRunOptionsRejectsNonDaemonWorktree(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for non-daemon agent with --worktree")
	}
}

func TestBuildRunOptionsRejectsNonDaemonServeOnly(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for non-daemon agent with --serve-only")
	}
}

func TestBuildRunOptionsAllowsNonDaemonPlainRun(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error for non-daemon agent plain run: %v", err)
	}
}

func TestBuildRunOptionsAllowsOpencodeWithDaemonFlags(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "opencode"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "x"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error for opencode with --worktree/--serve-only: %v", err)
	}
}

// The bare --notify flag must resolve to "on" via NoOptDefVal once parsed.
func TestNotifyFlagBareMeansOn(t *testing.T) {
	cmd := newRunCommand()
	cmd.SetArgs([]string{"--notify"})
	if err := cmd.ParseFlags([]string{"--notify"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !cmd.Flags().Changed(FlagNotify) {
		t.Fatal("--notify should be marked changed after parse")
	}
	if got, _ := cmd.Flags().GetString(FlagNotify); got != "on" {
		t.Errorf("notify flag after parse = %q, want on (NoOptDefVal)", got)
	}
}

func TestBuildRunOptionsNotifyDefaultOff(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{})
	cmd := newRunCommand()
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Notify.Active() {
		t.Errorf("default notify should be inactive, got %+v", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyFromConfig(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{Notify: NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("notify from config = %+v, want desktop+system", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyEnvOverridesConfig(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{Notify: NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("env override audio: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyFlagOverridesEnv(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNotify, "desktop"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	r := NewResolverWithConfig(Config{})
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioOff {
		t.Errorf("flag override desktop: got %+v, want desktop on audio off", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyInvalidValue(t *testing.T) {
	t.Setenv(notifyEnvVar, "loud")
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{})
	cmd := newRunCommand()
	if _, err := r.BuildRunOptions(cmd, ui); err == nil {
		t.Fatal("expected error for invalid AGENTS_SANDBOX_NOTIFY value")
	}
}

func TestBuildRunOptionsNotifyLegacyEnvOverridesConfig(t *testing.T) {
	t.Setenv(legacyNotifyEnvVar, "audio")
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{Notify: NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("legacy env override audio: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyAgentsEnvTakesPrecedenceOverLegacy(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	t.Setenv(legacyNotifyEnvVar, "desktop")
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{})
	cmd := newRunCommand()
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("AGENTS_ should win: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildRunOptionsNotifyRejectedForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagNotify, "on"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	r := NewResolverWithConfig(Config{})
	if _, err := r.BuildRunOptions(cmd, ui); err == nil {
		t.Fatal("expected error: --notify is not supported by interactive agent pi")
	}
}

func TestBuildRunOptionsNotifyConfigOnlyWarnsForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{Notify: NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioOff,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
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

// --dry-run auto-enables --dry-run-vm so a dry run exercises the full VM boot.
func TestBuildRunOptionsDryRunAutoEnablesDryRunVM(t *testing.T) {
	ui := &termio.Mock{}
	r := NewResolverWithConfig(Config{})
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagDryRun, "true"); err != nil {
		t.Fatalf("set dry-run: %v", err)
	}

	opts, err := r.BuildRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if !opts.DryRun {
		t.Error("DryRun = false; want true")
	}
	if !opts.DryRunVM {
		t.Error("DryRunVM = false; want auto-enabled true by --dry-run")
	}
	found := false
	for _, v := range ui.VerboseCalls {
		if strings.Contains(v, "dry-run-vm: auto-enabled") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a verbose note about auto-enabling dry-run-vm, got %v", ui.VerboseCalls)
	}
}

// An invalid --worktree slug fails at the earliest resolveFlags step.
func TestBuildRunOptionsRejectsInvalidWorktreeSpec(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagWorktree, "Not A Slug"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for a non-slug --worktree value")
	} else if !strings.Contains(err.Error(), "not a valid slug") {
		t.Errorf("error = %q; want to mention not a valid slug", err)
	}
}

// An invalid --notify flag value fails in resolveNotifyConfig.
func TestBuildRunOptionsNotifyFlagInvalidValue(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNotify, "bogus"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *Resolver
	if _, err := r.BuildRunOptions(cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for invalid --notify value")
	} else if !strings.Contains(err.Error(), "invalid --notify value") {
		t.Errorf("error = %q; want to mention invalid --notify value", err)
	}
}
