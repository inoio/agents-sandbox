package main

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

func TestResolveConfigAgentNameFromResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildConfigAgentCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{Agent: "pi"}))
	cmd.SetContext(ctx)

	name, err := resolveConfigAgentName(cmd, nil)
	if err != nil {
		t.Fatalf("resolveConfigAgentName: %v", err)
	}
	if name != "pi" {
		t.Errorf("resolveConfigAgentName = %q, want pi (from resolver)", name)
	}
}

func TestResolveConfigAgentNameResolverEmptyAgent(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildConfigAgentCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}))
	cmd.SetContext(ctx)

	name, err := resolveConfigAgentName(cmd, nil)
	if err != nil {
		t.Fatalf("resolveConfigAgentName: %v", err)
	}
	if name != defaultAgentName {
		t.Errorf("resolveConfigAgentName = %q, want %q", name, defaultAgentName)
	}
}

func TestResolveBuildDockerSettingDefault(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if resolveBuildDockerSetting(cmd).Enabled {
		t.Error("resolveBuildDockerSetting = true, want false (no flag, no resolver)")
	}
}

func TestResolveBuildDockerSettingFlag(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDocker, "true"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	got := resolveBuildDockerSetting(cmd)
	if !got.Enabled || !got.Explicit {
		t.Errorf("resolveBuildDockerSetting = %+v, want enabled and explicit (flag set)", got)
	}
}

func TestResolveBuildDeprecatedDindFlagSetting(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDind, "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	got := resolveBuildDockerSetting(cmd)
	if !got.Enabled || !got.Explicit {
		t.Errorf("resolveBuildDockerSetting = %+v, want enabled and explicit (deprecated --dind set)", got)
	}
}

func TestResolveBuildDockerSettingFlagWinsOverDind(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDind, "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	if err := cmd.Flags().Set(flagDocker, "false"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	if resolveBuildDockerSetting(cmd).Enabled {
		t.Error("resolveBuildDockerSetting = true, want --docker to win over --dind")
	}
}

func TestResolveBuildDockerSettingFromResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildDockerfileCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{Docker: true}))
	cmd.SetContext(ctx)
	if !resolveBuildDockerSetting(cmd).Enabled {
		t.Error("resolveBuildDockerSetting = false, want true (from resolver)")
	}
}

// The build path resolves the deprecated --dind flag through the same shared
// precedence as the run path, so a canonical docker config beats it here too.
func TestResolveBuildDindFlagLosesToDockerConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"docker": false})

	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDind, "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	cmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd)))

	got := resolveBuildDockerSetting(cmd)
	if want := (options.DockerSetting{Enabled: false, Explicit: true}); got != want {
		t.Errorf("resolveBuildDockerSetting = %+v, want %+v", got, want)
	}
}

// A user config `docker: false` is an explicit source: the build path must carry
// the explicitness through, so it overrides any project-Dockerfile content signal.
func TestResolveBuildDockerSettingFromResolverExplicitOff(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WriteYAML(t, configpaths.Get().UserConfigDir(), "config.yaml", map[string]any{"docker": false})

	cmd := buildDockerfileCmd(&termio.Mock{})
	cmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, cmd)))

	got := resolveBuildDockerSetting(cmd)
	if want := (options.DockerSetting{Enabled: false, Explicit: true}); got != want {
		t.Errorf("resolveBuildDockerSetting = %+v, want %+v", got, want)
	}
}

func TestReservedHomeConfigTargetsNonMerger(t *testing.T) {
	got := reservedHomeConfigTargets(noDaemonAgent{name: "nod"}, "/home/dev")
	if got != nil {
		t.Errorf("reservedHomeConfigTargets = %v, want nil for non-merger agent", got)
	}
}

// relErrConfigMerger is an Agent that implements ConfigMerger but whose
// VMConfigPath returns a relative path, so filepath.Rel fails against an
// absolute home.
type relErrConfigMerger struct{ noDaemonAgent }

func (relErrConfigMerger) SnippetPattern() string       { return "" }
func (relErrConfigMerger) VMConfigPath(_ string) string { return "relative/path" }
func (relErrConfigMerger) ConfigFileNames() []string    { return nil }

func TestReservedHomeConfigTargetsRelError(t *testing.T) {
	got := reservedHomeConfigTargets(relErrConfigMerger{name: "x"}, "/different/home")
	if got != nil {
		t.Errorf("reservedHomeConfigTargets = %v, want nil on filepath.Rel error", got)
	}
}

func TestReservedHomeConfigTargetsOK(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	got := reservedHomeConfigTargets(a, "/home/dev")
	if len(got) != 1 {
		t.Fatalf("reservedHomeConfigTargets = %v, want one reserved target", got)
	}
}

// TestBuildInvalidNotifyFlag exercises the --notify flag override
// error branch in runoptions.Build (an invalid value passed directly on the
// flag, not via the env var), with a nil resolver.
func TestBuildInvalidNotifyFlag(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagNotify, "loud"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *launcherconfig.Resolver
	if _, err := runoptions.Build(r.Config(), cmd, ui); err == nil {
		t.Fatal("expected error for invalid --notify flag value")
	}
}
