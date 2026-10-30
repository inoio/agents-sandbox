package runoptions

import (
	"errors"
	"slices"
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
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// wantNetworkProfiles asserts the effective network policy's profile set.
func wantNetworkProfiles(t *testing.T, policy network.Policy, want ...network.Profile) {
	t.Helper()
	if got := policy.Effective().Profiles; !slices.Equal(got, want) {
		t.Fatalf("network profiles = %v, want %v", got, want)
	}
}

// registerRunFlags registers the flags Build reads directly (via the
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
	cmd.Flags().StringSlice(FlagNetwork, nil, "network")
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
func mustResolver(t *testing.T, cmd *cobra.Command) *viperconfig.Resolver {
	t.Helper()
	r, err := viperconfig.NewResolver(cmd, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

// A zero-value Config produces the expected defaults.
func TestBuildDefaults(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{}) // zero value
	cmd := newRunCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	expectedPolicy := options.ReapPolicy{
		AutoStopOnActiveSessions: false,
		MaxSessionRetries:        10,
	}
	if opts.ReapPolicy != expectedPolicy {
		t.Errorf("ReapPolicy = %+v; want %+v", opts.ReapPolicy, expectedPolicy)
	}
	if opts.IdleTimeout != 0 {
		t.Errorf("IdleTimeout = %v; want 0 (the VM layer applies options.DefaultVMIdleTimeout)", opts.IdleTimeout)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfilePublic)
}

// AutoStopOnActiveSessions: true propagates to ReapPolicy.
func TestBuildAutoStopOnActiveSessions(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{AutoStopOnActiveSessions: true})
	cmd := newRunCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Errorf("ReapPolicy.AutoStopOnActiveSessions = false; want true")
	}
}

// Custom AutoStopMaxSessionRetries propagates to ReapPolicy.
func TestBuildCustomMaxSessionRetries(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{AutoStopMaxSessionRetries: 5})
	cmd := newRunCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if opts.ReapPolicy.MaxSessionRetries != 5 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 5", opts.ReapPolicy.MaxSessionRetries)
	}
}

func TestBuildAutoStopTimeout(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{AutoStopTimeout: 30 * time.Second})
	cmd := newRunCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if opts.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", opts.IdleTimeout)
	}
}

// TestBuildOpenLinksFromConfig covers the config branch: with a
// resolver, open-links comes from the launcher config.
func TestBuildOpenLinksFromConfig(t *testing.T) {
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{OpenLinks: true})
	opts, err := Build(r.Config(), newRunCommand(), &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.OpenLinks {
		t.Fatal("opts.OpenLinks = false, want true from the config")
	}
}

// TestBuildOpenLinksFromFlagWithoutConfig covers the nil-resolver
// branch: without config, open-links comes from the flag.
func TestBuildOpenLinksFromFlagWithoutConfig(t *testing.T) {
	cmd := newRunCommand()
	cmd.Flags().Bool(FlagOpenLinks, true, "open-links")
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.OpenLinks {
		t.Fatal("opts.OpenLinks = false, want true from the flag")
	}
}

func TestBuildNilResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := newRunCommand()

	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
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

func TestBuildShellCommand(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{
		AutoStopOnActiveSessions:  true,
		AutoStopMaxSessionRetries: 7,
		AutoStopTimeout:           25 * time.Second,
	})
	cmd := newShellCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
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

func TestBuildServeOnly(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.ServeOnly {
		t.Errorf("expected ServeOnly=true when --serve-only passed, got false")
	}

	cmd2 := newRunCommand()
	opts2, err := Build(r.Config(), cmd2, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts2.ServeOnly {
		t.Errorf("expected ServeOnly=false by default, got true")
	}
}

func TestBuildRootFlag(t *testing.T) {
	shellCmd := newShellCommand()
	if err := shellCmd.Flags().Set(FlagRoot, "true"); err != nil {
		t.Fatalf("set root: %v", err)
	}
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), shellCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Root {
		t.Errorf("expected Root=true when --root passed, got false")
	}
}

func TestBuildRootFlagNotSet(t *testing.T) {
	runCmd := newRunCommand()
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), runCmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Root {
		t.Errorf("expected Root=false when --root not registered, got true")
	}
}

func TestBuildInvalidTmpSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("tmp-size", "bogus"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --tmp-size")
	}
	if want := "invalid --tmp-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildInvalidDiskSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("disk-size", "bogus"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --disk-size")
	}
	if want := "invalid --disk-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildOutOfRangeTmpSize(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("tmp-size", "4294967296"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for a tmp-size above the uint32 MiB range")
	}
	if want := "invalid --tmp-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildValidSizeFlags(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("tmp-size", "4G"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	if err := cmd.Flags().Set("disk-size", "16G"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
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

func TestBuildInvalidWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("workspace-quota", "bogus"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	r := mustResolver(t, cmd)
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --workspace-quota")
	}
	if want := "invalid --workspace-quota"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildWorkspaceQuota(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set("workspace-quota", "32G"); err != nil {
		t.Fatalf("set workspace-quota: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for valid workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "32G" {
		t.Errorf("WorkspaceQuota = %q, want 32G", opts.WorkspaceQuota)
	}
}

func TestBuildWorkspaceQuotaDefault(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for default workspace-quota, got: %v", err)
	}
	if opts.WorkspaceQuota != "16G" {
		t.Errorf("WorkspaceQuota = %q, want 16G default", opts.WorkspaceQuota)
	}
}

func TestBuildPropagatesDocker(t *testing.T) {
	cmd := newRunCommand()
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Docker: true})
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Docker.Enabled {
		t.Error("opts.Docker.Enabled = false, want true from resolver")
	}
}

// A user config `docker: false` is an explicit source: the setting must reach the
// boundary as Explicit, so it overrides any project-Dockerfile content signal.
func TestResolveDockerSettingFromConfigExplicitOff(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"docker": false})

	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if want := (options.DockerSetting{Enabled: false, Explicit: true}); opts.Docker != want {
		t.Errorf("opts.Docker = %+v, want %+v", opts.Docker, want)
	}
}

func TestBuildDeprecatedDindFlag(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	opts, err := Build(viperconfig.NewResolverWithConfig(viperconfig.Config{}).Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Docker.Enabled {
		t.Error("opts.Docker.Enabled = false, want true from the deprecated --dind flag")
	}
}

func TestBuildDockerFlagWinsOverDind(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	if err := cmd.Flags().Set("docker", "false"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	opts, err := Build(viperconfig.NewResolverWithConfig(viperconfig.Config{}).Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Docker.Enabled {
		t.Error("opts.Docker.Enabled = true, want --docker to win over --dind")
	}
}

// The deprecation policy's "replacement wins" rule holds across layers: the
// canonical docker config key beats the deprecated --dind flag, so the run path
// resolves dind only when no docker source is set at all.
func TestBuildDindFlagLosesToDockerConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"docker": false})

	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if want := (options.DockerSetting{Enabled: false, Explicit: true}); opts.Docker != want {
		t.Errorf("opts.Docker = %+v, want %+v", opts.Docker, want)
	}
}

// AGENTS_SANDBOX_DOCKER is the canonical env spelling, so it beats a
// deprecated --dind flag in either direction.
func TestBuildDindFlagLosesToDockerEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_DOCKER", "true")

	cmd := newRunCommand()
	if err := cmd.Flags().Set("dind", "false"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if want := (options.DockerSetting{Enabled: true, Explicit: true}); opts.Docker != want {
		t.Errorf("opts.Docker = %+v, want %+v", opts.Docker, want)
	}
}

func TestBuildAgentVersionFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgentVersion, "1.2.3"); err != nil {
		t.Fatalf("set agent-version: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.AgentVersion != "1.2.3" {
		t.Fatalf("AgentVersion = %q, want 1.2.3", opts.AgentVersion)
	}
}

func TestBuildNetworkFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "none"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfileNone)
}

func TestBuildNetworkFlagCombinesProfiles(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "public,host"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfilePublic, network.ProfileHost)
}

func TestBuildNetworkFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "private")
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfilePrivate)
}

func TestBuildNetworkSpacedFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_PROFILE", "public, host")
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfilePublic, network.ProfileHost)
}

func TestBuildNetworkInvalid(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "bogus"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	r := mustResolver(t, cmd)
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for invalid --network profile")
	}
}

func TestBuildDNSServersFlag(t *testing.T) {
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
			var r *viperconfig.Resolver
			opts, err := Build(r.Config(), cmd, &termio.Mock{})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if len(opts.Network.DNSServers) != 2 ||
				opts.Network.DNSServers[0] != "1.1.1.1" || opts.Network.DNSServers[1] != "8.8.8.8" {
				t.Fatalf("Network.DNSServers = %v, want [1.1.1.1 8.8.8.8]", opts.Network.DNSServers)
			}
		})
	}
}

func TestBuildDNSCombinedWithNetworkNone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNetwork, "none"); err != nil {
		t.Fatalf("set network: %v", err)
	}
	if err := cmd.Flags().Set(FlagDNSServers, "1.1.1.1"); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantNetworkProfiles(t, opts.Network, network.ProfileNone)
	if len(opts.Network.DNSServers) != 1 || opts.Network.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1]", opts.Network.DNSServers)
	}
}

func TestBuildDNSFlagOverridesConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", "9.9.9.9")
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagDNSServers, "1.1.1.1"); err != nil {
		t.Fatalf("set dns: %v", err)
	}
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(opts.Network.DNSServers) != 1 || opts.Network.DNSServers[0] != "1.1.1.1" {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1] (flag overrides env)", opts.Network.DNSServers)
	}
}

func TestBuildDNSFromResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_NETWORK_DNS_SERVERS", "1.1.1.1,8.8.8.8")
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(opts.Network.DNSServers) != 2 {
		t.Fatalf("Network.DNSServers = %v, want [1.1.1.1 8.8.8.8] from resolver", opts.Network.DNSServers)
	}
}

// Configured mounts are resolved into RunOptions, keyed by guest path.
func TestBuildResolvesMounts(t *testing.T) {
	ui := &termio.Mock{}
	source := t.TempDir()
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Mounts: mounts.Mounts{
		"/home/dev/.m2": {Source: source},
	}})
	cmd := newRunCommand()

	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
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
func TestBuildRejectsInvalidMount(t *testing.T) {
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
			r := viperconfig.NewResolverWithConfig(viperconfig.Config{Mounts: tc.mount})
			cmd := newRunCommand()

			if _, err := Build(r.Config(), cmd, ui); err == nil {
				t.Fatal("expected Build to fail")
			}
		})
	}
}

func TestBuildAgentDefault(t *testing.T) {
	cmd := newRunCommand()
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != defaultAgentName {
		t.Errorf("Agent = %q; want %q", opts.Agent, defaultAgentName)
	}
}

func TestBuildUnknownAgent(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "bogus"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *viperconfig.Resolver
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for unknown --agent")
	}
	if want := "unknown agent"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

// The agent can be selected via the AGENTS_SANDBOX_AGENT env var.
func TestBuildAgentFromEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_AGENT", defaultAgentName)
	cmd := newRunCommand()
	r := mustResolver(t, cmd)
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != defaultAgentName {
		t.Errorf("Agent = %q, want %s from env", opts.Agent, defaultAgentName)
	}
}

func TestBuildAgentPI(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != "pi" {
		t.Errorf("Agent = %q; want pi", opts.Agent)
	}
}

func TestBuildAgentClaudeCode(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != "claude-code" {
		t.Errorf("Agent = %q; want claude-code", opts.Agent)
	}
}

func TestBuildAgentOpencode2(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "opencode2"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *viperconfig.Resolver
	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != "opencode2" {
		t.Errorf("Agent = %q; want opencode2", opts.Agent)
	}
}

func TestBuildAcceptsWorktreeAndServeOnlyForOpencode2(t *testing.T) {
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
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err != nil {
		t.Errorf("expected opencode2 to support --worktree and --serve-only, got %v", err)
	}
}

func TestBuildRejectsWorktreeForPI(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *viperconfig.Resolver
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for pi with --worktree")
	}
	if want := `not supported by agent "pi"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRejectsServeOnlyForClaudeCode(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *viperconfig.Resolver
	_, err := Build(r.Config(), cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for claude-code with --serve-only")
	}
	if want := `not supported by agent "claude-code"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestBuildRejectsNonDaemonWorktree(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "claude-code"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagWorktree, "feat"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for non-daemon agent with --worktree")
	}
}

func TestBuildRejectsNonDaemonServeOnly(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for non-daemon agent with --serve-only")
	}
}

func TestBuildAllowsNonDaemonPlainRun(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err != nil {
		t.Fatalf("unexpected error for non-daemon agent plain run: %v", err)
	}
}

func TestBuildAllowsOpencodeWithDaemonFlags(t *testing.T) {
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
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err != nil {
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

func TestBuildNotifyDefaultOff(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})
	cmd := newRunCommand()
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Active() {
		t.Errorf("default notify should be inactive, got %+v", opts.Notify)
	}
}

func TestBuildNotifyFromConfig(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Notify: viperconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("notify from config = %+v, want desktop+system", opts.Notify)
	}
}

func TestBuildNotifyEnvOverridesConfig(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Notify: viperconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("env override audio: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildNotifyFlagOverridesEnv(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	ui := &termio.Mock{}
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNotify, "desktop"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Notify.Desktop || opts.Notify.Audio != notify.AudioOff {
		t.Errorf("flag override desktop: got %+v, want desktop on audio off", opts.Notify)
	}
}

func TestBuildNotifyInvalidValue(t *testing.T) {
	t.Setenv(notifyEnvVar, "loud")
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})
	cmd := newRunCommand()
	if _, err := Build(r.Config(), cmd, ui); err == nil {
		t.Fatal("expected error for invalid AGENTS_SANDBOX_NOTIFY value")
	}
}

func TestBuildNotifyLegacyEnvOverridesConfig(t *testing.T) {
	t.Setenv(legacyNotifyEnvVar, "audio")
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Notify: viperconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioSystem,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("legacy env override audio: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildNotifyAgentsEnvTakesPrecedenceOverLegacy(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	t.Setenv(legacyNotifyEnvVar, "desktop")
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})
	cmd := newRunCommand()
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Desktop || opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("AGENTS_ should win: got %+v, want desktop off audio system", opts.Notify)
	}
}

func TestBuildNotifyRejectedForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	if err := cmd.Flags().Set(FlagNotify, "on"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})
	if _, err := Build(r.Config(), cmd, ui); err == nil {
		t.Fatal("expected error: --notify is not supported by interactive agent pi")
	}
}

func TestBuildNotifyConfigOnlyWarnsForInteractiveAgent(t *testing.T) {
	ui := &termio.Mock{}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Notify: viperconfig.NotifyConfig{
		Desktop: true,
		Audio:   notify.AudioOff,
		OnInput: true, OnDone: true, OnError: true,
	}})
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	opts, err := Build(r.Config(), cmd, ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
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

func TestBuildLeavesDryRunToTheMode(t *testing.T) {
	// Build no longer carries DryRun/DryRunVM; the mode is resolved separately.
	cmd := &cobra.Command{}
	cmd.Flags().Bool(FlagDryRun, false, "")
	cmd.Flags().Bool(FlagDryRunVM, false, "")
	_ = cmd.Flags().Set(FlagDryRun, "true")
	ui := termio.NewTestMock(t)
	opts, err := Build(nil, cmd, &ui)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_ = opts // RunOptions has no dry-run fields left to assert; this pins that Build no longer reads them
	if got := ResolveMode(cmd); got != options.ModePreview {
		t.Errorf("ResolveMode() = %v, want ModePreview", got)
	}
}

// An invalid --worktree slug fails at the earliest resolveFlags step.
func TestBuildRejectsInvalidWorktreeSpec(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagWorktree, "Not A Slug"); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for a non-slug --worktree value")
	} else if !strings.Contains(err.Error(), "not a valid slug") {
		t.Errorf("error = %q; want to mention not a valid slug", err)
	}
}

// An invalid --notify flag value fails in resolveNotifyConfig.
func TestBuildNotifyFlagInvalidValue(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNotify, "bogus"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *viperconfig.Resolver
	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for invalid --notify value")
	} else if !strings.Contains(err.Error(), "invalid --notify value") {
		t.Errorf("error = %q; want to mention invalid --notify value", err)
	}
}

// notifyEnvOverride reports the variable name alongside its value.
func TestNotifyEnvOverrideReturnsSourceName(t *testing.T) {
	t.Setenv(notifyEnvVar, "desktop")

	value, name := notifyEnvOverride()
	if value != "desktop" || name != notifyEnvVar {
		t.Fatalf("notifyEnvOverride() = %q, %q; want %q, %q", value, name, "desktop", notifyEnvVar)
	}
}

// Config-derived resource limits reach the resolved options.
func TestBuildPropagatesConfigResourceLimits(t *testing.T) {
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{
		CPUs:                6,
		Memory:              "8G",
		ProvisionHostConfig: true,
	})

	opts, err := Build(r.Config(), newRunCommand(), &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 from config", opts.CPUs)
	}
	if opts.Memory != "8G" {
		t.Errorf("Memory = %q; want 8G from config", opts.Memory)
	}
	if opts.ProvisionHostConfig == nil || !*opts.ProvisionHostConfig {
		t.Errorf("ProvisionHostConfig = %v; want a pointer to true from config", opts.ProvisionHostConfig)
	}
}

// ResolveDockerSetting tolerates a command that never registered --dind.
func TestResolveDockerSettingWithoutDindFlag(t *testing.T) {
	got := ResolveDockerSetting(&cobra.Command{Use: "run"}, nil)

	if got != (options.DockerSetting{}) {
		t.Errorf("ResolveDockerSetting = %+v; want the zero setting", got)
	}
}

// ResolveDockerSetting tolerates a command that registered --dind but not the
// canonical --docker flag.
func TestResolveDockerSettingWithoutDockerFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().Bool("dind", false, "")
	if err := cmd.Flags().Set("dind", "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}

	got := ResolveDockerSetting(cmd, nil)

	if !got.Enabled || !got.Explicit {
		t.Errorf("ResolveDockerSetting = %+v; want enabled and explicit from the deprecated --dind flag", got)
	}
}

func TestBuildDockerSettingFromFlagsWithoutConfig(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set("docker", "true"); err != nil {
		t.Fatal(err)
	}
	opts, err := Build(nil, cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !opts.Docker.Enabled || !opts.Docker.Explicit {
		t.Errorf("got %+v, want enabled and explicit from --docker with nil config", opts.Docker)
	}
}

// A config-provided agent name wins over the flag default.
func TestBuildAgentFromConfig(t *testing.T) {
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Agent: "pi"})

	opts, err := Build(r.Config(), newRunCommand(), &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != "pi" {
		t.Fatalf("Agent = %q; want pi from config", opts.Agent)
	}
}

// An explicitly empty --agent value is ignored rather than selecting an empty
// (unknown) agent.
func TestBuildEmptyAgentFlagIgnored(t *testing.T) {
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, ""); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Agent: "pi"})

	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Agent != "pi" {
		t.Fatalf("Agent = %q; want pi (the empty flag is ignored)", opts.Agent)
	}
}

// The notify env override is rejected for an agent without a daemon.
func TestBuildNotifyEnvRejectedForInteractiveAgent(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagAgent, "pi"); err != nil {
		t.Fatalf("set agent: %v", err)
	}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})

	if _, err := Build(r.Config(), cmd, &termio.Mock{}); err == nil {
		t.Fatal("expected error for the notify env override on an interactive agent")
	}
}

// A config notify section with an empty audio mode resolves to audio off.
func TestBuildNotifyConfigEmptyAudioDefaultsOff(t *testing.T) {
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{Notify: viperconfig.NotifyConfig{
		Desktop: true,
	}})

	opts, err := Build(r.Config(), newRunCommand(), &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Audio != notify.AudioOff {
		t.Errorf("Notify.Audio = %q; want off for an unset config audio mode", opts.Notify.Audio)
	}
}

// A malformed notify env override error wraps its cause so callers can inspect
// it.
func TestBuildNotifyEnvErrorWrapsCause(t *testing.T) {
	t.Setenv(notifyEnvVar, "loud")
	var r *viperconfig.Resolver

	_, err := Build(r.Config(), newRunCommand(), &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid AGENTS_SANDBOX_NOTIFY value")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("error %q does not wrap its cause", err)
	}
}

// An explicitly empty --notify value is ignored, leaving the env override in
// effect rather than erroring.
func TestBuildNotifyEmptyFlagIgnored(t *testing.T) {
	t.Setenv(notifyEnvVar, "audio")
	cmd := newRunCommand()
	if err := cmd.Flags().Set(FlagNotify, ""); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	r := viperconfig.NewResolverWithConfig(viperconfig.Config{})

	opts, err := Build(r.Config(), cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if opts.Notify.Audio != notify.AudioSystem {
		t.Errorf("Notify.Audio = %q; want the env override to survive an empty flag", opts.Notify.Audio)
	}
}

// ResolveMode maps the dry-run flag pair to the execution mode, with --dry-run
// subsuming --dry-run-vm.
func TestResolveModeFromFlags(t *testing.T) {
	cases := []struct {
		name     string
		dryRun   bool
		dryRunVM bool
		want     options.Mode
	}{
		{"full", false, false, options.ModeRun},
		{"skip vm", false, true, options.ModeSkipVM},
		{"preview subsumes skip vm", true, true, options.ModePreview},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Bool(FlagDryRun, false, "")
			cmd.Flags().Bool(FlagDryRunVM, false, "")
			if tc.dryRun {
				_ = cmd.Flags().Set(FlagDryRun, "true")
			}
			if tc.dryRunVM {
				_ = cmd.Flags().Set(FlagDryRunVM, "true")
			}
			if got := ResolveMode(cmd); got != tc.want {
				t.Errorf("ResolveMode() = %v, want %v", got, tc.want)
			}
		})
	}
}
