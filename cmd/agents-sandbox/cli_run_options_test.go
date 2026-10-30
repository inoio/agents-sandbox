package main

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

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

func TestRunAndShellHaveDockerFlags(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDocker); flag == nil {
			t.Errorf("%s command must have --docker flag", name)
		}
		if flag := foundCmd.Flags().Lookup(flagDind); flag == nil {
			t.Errorf("%s command must keep the deprecated --dind alias", name)
		}
	}
}

func TestRunAndShellHaveDNSFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		if flag := foundCmd.Flags().Lookup(flagDNSServers); flag == nil {
			t.Errorf("%s command must have --dns flag", name)
		}
	}
}

func TestRunAndShellHaveComposableNetworkFlag(t *testing.T) {
	for _, name := range []string{cmdRun, cmdShell} {
		cmd, _ := setupCommandFixtures(t, name, "--help")
		foundCmd, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatalf("Find %q: %v", name, err)
		}
		flag := foundCmd.Flags().Lookup(flagNetwork)
		if flag == nil {
			t.Fatalf("%s command must have --network flag", name)
		}
		if flag.Value.Type() != "stringSlice" {
			t.Errorf("%s --network type = %q, want stringSlice (comma-separated or repeated)", name, flag.Value.Type())
		}
	}
}

// noDaemonAgent is a minimal agent.Agent that intentionally does not implement
// DaemonProvider, so agent-support checks can be exercised without polluting
// the global agent registry.
type noDaemonAgent struct{ name string }

func (a noDaemonAgent) Name() string             { return a.name }
func (noDaemonAgent) ConfigDirName() string      { return "nod" }
func (noDaemonAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

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

func TestBuildRunOptionsProvisionHostConfigOptIn(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildCommandWithLauncherConfig(ui, launcherconfig.Config{ProvisionHostConfig: true})

	opts, err := runoptions.Build(resolverFromContext(cmd.Context()).Config(), cmd, ui)
	if err != nil {
		t.Fatalf("BuildRunOptions: %v", err)
	}
	if opts.ProvisionHostConfig == nil || !*opts.ProvisionHostConfig {
		t.Errorf("ProvisionHostConfig = %v; want pointer to true", opts.ProvisionHostConfig)
	}
}

func TestRefreshRunOptionsAfterMigrationReloadsNetworkAndPreservesArgs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}),
	))
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.yaml",
		"network:\n  egress-allow:\n    - litellm.inoio.de\n",
	)
	opts, _, err := refreshRunOptionsAfterMigration(cmd, []string{"ping", "--verbose"}, &termio.Mock{})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Network.EgressAllow) != 1 || opts.Network.EgressAllow[0] != "litellm.inoio.de" {
		t.Fatalf("refreshed opts.Network = %+v", opts.Network)
	}
	if len(opts.Args) != 2 || opts.Args[0] != "ping" {
		t.Fatalf("args = %v", opts.Args)
	}
}

func TestRefreshRunOptionsAfterMigrationPreservesNetworkFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagNetwork, "public"); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}),
	))
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "network:\n  profile: private\n")
	opts, _, err := refreshRunOptionsAfterMigration(cmd, nil, &termio.Mock{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(opts.Network.Profiles, network.ProfilePublic) {
		t.Fatalf("network profiles = %v, want to contain public", opts.Network.Profiles)
	}
}

func TestRefreshRunOptionsAfterMigrationResolverError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}),
	))
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "home: [")
	if _, _, err := refreshRunOptionsAfterMigration(cmd, nil, &termio.Mock{}); err == nil {
		t.Fatal("expected resolver error")
	}
}

func TestRefreshRunOptionsAfterMigrationExtractError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagNetwork, "invalid-profile"); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}),
	))
	if _, _, err := refreshRunOptionsAfterMigration(cmd, nil, &termio.Mock{}); err == nil {
		t.Fatal("expected options refresh error")
	}
}
