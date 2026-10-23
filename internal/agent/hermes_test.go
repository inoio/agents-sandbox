package agent_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestHermesLookup(t *testing.T) {
	a, ok := agent.Lookup("hermes")
	if !ok {
		t.Fatal("Lookup(\"hermes\") returned not-ok")
	}
	if a.Name() != "hermes" {
		t.Errorf("Lookup(\"hermes\").Name() = %q, want hermes", a.Name())
	}
	if a.ConfigDirName() != "hermes" {
		t.Errorf("ConfigDirName = %q, want hermes", a.ConfigDirName())
	}
}

func TestHermesNamesIncludesHermes(t *testing.T) {
	if !slices.Contains(agent.Names(), "hermes") {
		t.Errorf("Names() = %v, want to include hermes", agent.Names())
	}
}

func TestHermesImageSpec(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	spec := a.ImageSpec()
	if spec.VersionArg != "HERMES_VERSION" {
		t.Errorf("VersionArg = %q, want HERMES_VERSION", spec.VersionArg)
	}
	if !strings.Contains(spec.InstallCommand, "https://hermes-agent.nousresearch.com/install.sh") {
		t.Errorf("InstallCommand = %q, want the official hermes installer", spec.InstallCommand)
	}
	if !strings.Contains(spec.InstallCommand, `"v$HERMES_VERSION"`) {
		t.Errorf("InstallCommand = %q, want the pinned release tag from HERMES_VERSION", spec.InstallCommand)
	}
	if !strings.Contains(spec.InstallCommand, "--non-interactive") {
		t.Errorf("InstallCommand = %q, want a non-interactive install (no setup wizard)", spec.InstallCommand)
	}
	// The store must stay outside /home/dev, which the sandbox home volume
	// covers at runtime, and must belong to the runtime user.
	for _, want := range []string{
		"--hermes-home /opt/agents-sandbox/hermes-home",
		"chown -R 1000:1000 /opt/agents-sandbox/hermes /opt/agents-sandbox/hermes-home",
		"> /opt/agents-sandbox/bin/hermes",
		`ln -s "/opt/agents-sandbox/hermes-home/$d" "$home/$d"`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if got := spec.AgentEnv["HERMES_HOME"]; got != "/home/dev/.hermes" {
		t.Errorf("AgentEnv[HERMES_HOME] = %q, want /home/dev/.hermes", got)
	}
	if spec.RequiresNode {
		t.Error("RequiresNode = true, want false (hermes provisions its own node for the TUI build)")
	}
}

func TestHermesAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("hermes should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--continue"})
	want := "hermes --continue"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestHermesConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("hermes should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.yaml" {
		t.Errorf("SnippetPattern = %q, want config*.yaml", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/user"); got != filepath.Join("/home/user", ".hermes", "config.yaml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); len(names) != 1 || names[0] != "config.yaml" {
		t.Errorf("ConfigFileNames = %v, want [config.yaml]", names)
	}
}

func TestHermesProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("hermes should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	found := false
	for _, r := range rules {
		if r.Dir == ".hermes" && slices.Contains(r.Patterns, "config.yaml") {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .hermes/config.yaml: %+v", rules)
	}
	for _, r := range rules {
		for _, p := range r.Patterns {
			if strings.Contains(p, ".env") || strings.Contains(p, "auth.json") {
				t.Errorf("ProvisionRules must not copy credentials to the VM: %+v", r)
			}
		}
	}
}

func TestHermesVersionProvider(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	p, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("hermes should implement VersionProvider")
	}
	if got := p.VersionCmd(); got != "hermes --version" {
		t.Errorf("VersionCmd() = %q, want hermes --version", got)
	}
	got, err := p.ParseVersion("Hermes Agent v0.22.5 (2026.9.24) · stable · local cf9d3fc537ba\n")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if got != "0.22.5" {
		t.Errorf("ParseVersion = %q, want 0.22.5", got)
	}
}

func TestHermesLatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("hermes should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestHermesNewerThan(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	checker, _ := agent.AsUpgradeChecker(a)
	gt, err := checker.NewerThan("v0.23.0", "0.22.5")
	if err != nil || !gt {
		t.Errorf("NewerThan(v0.23.0, 0.22.5) = %v, %v, want true, nil", gt, err)
	}
}

func TestHermesHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("hermes")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("hermes must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("hermes must not implement WorktreeProvider")
	}
	if _, ok := agent.AsSessionStatusProvider(a); ok {
		t.Error("hermes must not implement SessionStatusProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("hermes must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("hermes must not implement GuestToolRequirer (no daemon tools)")
	}
}
