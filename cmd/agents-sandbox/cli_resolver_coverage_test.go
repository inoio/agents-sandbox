package main

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/termio"
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

func TestResolveBuildDockerDefault(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if resolveBuildDocker(cmd) {
		t.Error("resolveBuildDocker = true, want false (no flag, no resolver)")
	}
}

func TestResolveBuildDockerFlag(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDocker, "true"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	if !resolveBuildDocker(cmd) {
		t.Error("resolveBuildDocker = false, want true (flag set)")
	}
}

func TestResolveBuildDeprecatedDindFlag(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDind, "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	if !resolveBuildDocker(cmd) {
		t.Error("resolveBuildDocker = false, want true (deprecated --dind flag set)")
	}
}

func TestResolveBuildDockerFlagWinsOverDind(t *testing.T) {
	cmd := buildDockerfileCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDind, "true"); err != nil {
		t.Fatalf("set dind: %v", err)
	}
	if err := cmd.Flags().Set(flagDocker, "false"); err != nil {
		t.Fatalf("set docker: %v", err)
	}
	if resolveBuildDocker(cmd) {
		t.Error("resolveBuildDocker = true, want --docker to win over --dind")
	}
}

func TestResolveBuildDockerFromResolver(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildDockerfileCmd(ui)
	ctx := context.WithValue(context.Background(), (*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{Docker: true}))
	cmd.SetContext(ctx)
	if !resolveBuildDocker(cmd) {
		t.Error("resolveBuildDocker = false, want true (from resolver)")
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

// TestBuildRunOptionsInvalidNotifyFlag exercises the --notify flag override
// error branch in BuildRunOptions (an invalid value passed directly on the
// flag, not via the env var), with a nil resolver.
func TestBuildRunOptionsInvalidNotifyFlag(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildRunCmd(ui)
	if err := cmd.Flags().Set(flagNotify, "loud"); err != nil {
		t.Fatalf("set notify: %v", err)
	}
	var r *launcherconfig.Resolver
	if _, err := r.BuildRunOptions(cmd, ui); err == nil {
		t.Fatal("expected error for invalid --notify flag value")
	}
}
