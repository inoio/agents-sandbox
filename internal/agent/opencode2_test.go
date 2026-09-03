package agent_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestOpencode2LookupAndName(t *testing.T) {
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("Lookup(\"opencode2\") returned not-ok")
	}
	if a.Name() != "opencode2" {
		t.Errorf("Name() = %q, want opencode2", a.Name())
	}
	found := false
	for _, n := range agent.Names() {
		if n == "opencode2" {
			found = true
		}
	}
	if !found {
		t.Errorf("Names() = %v, want to include opencode2", agent.Names())
	}
}

func TestOpencode2ConfigDirName(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	if a.ConfigDirName() != "opencode" {
		t.Errorf("ConfigDirName = %q, want opencode (v2 shares the v1 config dir)", a.ConfigDirName())
	}
}

func TestOpencode2ImageSpec(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	spec := a.ImageSpec()
	if spec.VersionArg != "OPENCODE2_VERSION" {
		t.Errorf("VersionArg = %q, want OPENCODE2_VERSION", spec.VersionArg)
	}
	if _, ok := spec.AgentEnv["OPENCODE_DISABLE_AUTOUPDATE"]; !ok {
		t.Errorf("AgentEnv = %v, want OPENCODE_DISABLE_AUTOUPDATE key", spec.AgentEnv)
	}
	wantInstall := "npm install -g @opencode-ai/cli@$OPENCODE2_VERSION"
	if spec.InstallCommand != wantInstall {
		t.Errorf("InstallCommand = %q, want %q", spec.InstallCommand, wantInstall)
	}
}

func TestOpencode2AttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode2 should implement AttachRunner")
	}
	got := runner.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode2 --server http://127.0.0.1:4096 --dir /workspace --print-logs"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencode2DaemonStartCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	local := daemon.DaemonStartCmd(false)
	if !strings.Contains(local, "opencode2 serve --hostname 127.0.0.1 --port 4096") {
		t.Errorf("local DaemonStartCmd = %q, want opencode2 serve on 127.0.0.1:4096", local)
	}
	if !strings.Contains(local, "/tmp/opencode2-serve.log") {
		t.Errorf("local DaemonStartCmd = %q, want opencode2-serve.log", local)
	}
	serve := daemon.DaemonStartCmd(true)
	if !strings.Contains(serve, "--hostname 0.0.0.0") {
		t.Errorf("serve DaemonStartCmd = %q, want hostname 0.0.0.0", serve)
	}
}

func TestOpencode2DaemonKillAndHealthCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	if !strings.Contains(daemon.DaemonKillCmd(), "pkill -f 'opencode2 serve'") {
		t.Errorf("DaemonKillCmd = %q, want pkill opencode2 serve", daemon.DaemonKillCmd())
	}
	if !strings.Contains(daemon.DaemonHealthCmd(), "127.0.0.1:4096/api/health") {
		t.Errorf("DaemonHealthCmd = %q, want v2 /api/health endpoint", daemon.DaemonHealthCmd())
	}
}

func TestOpencode2DaemonHealthParse(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	daemon := mustDaemon(t, a)
	ok, err := daemon.DaemonHealthParse(`{"healthy":true,"version":"0.0.0-beta-18866","pid":1}`)
	if err != nil || !ok {
		t.Errorf("DaemonHealthParse(healthy) = %v, %v, want true, nil", ok, err)
	}
	notOK, err := daemon.DaemonHealthParse(`{"healthy":false,"version":"0.0.0-beta-18866"}`)
	if err != nil || notOK {
		t.Errorf("DaemonHealthParse(unhealthy) = %v, %v, want false, nil", notOK, err)
	}
	if _, err := daemon.DaemonHealthParse("not json"); err == nil {
		t.Error("DaemonHealthParse(invalid) should error")
	}
}

func TestOpencode2WorktreeListCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeListCmd()
	for _, part := range []string{"/api/project/current", "/api/worktree/", "jq -r .id"} {
		if !strings.Contains(got, part) {
			t.Errorf("WorktreeListCmd = %q, want to contain %q", got, part)
		}
	}
}

func TestOpencode2WorktreeCreateCmd(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"})
	want := "PID=$(curl -sf http://127.0.0.1:4096/api/project/current | jq -r .id); " +
		"curl -sf -X POST http://127.0.0.1:4096/api/worktree/$PID -H 'Content-Type: application/json' " +
		`-d "{\"strategy\":\"git_worktree\",\"name\":\"feature/x\",\"directory\":\"$HOME/.local/share/opencode/worktree/$PID/feature/x\"}"`
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}

func TestOpencode2WorktreeCreateCmdWithBase(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	got := worktree.WorktreeCreateCmd(agent.WorktreeSpec{Name: "f", Base: "main"})
	if !strings.Contains(got, `\"from\":\"main\"`) {
		t.Errorf("WorktreeCreateCmd(with base) = %q, want from:main", got)
	}
}

func TestOpencode2WorktreeParseDir(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	worktree := mustWorktree(t, a)
	dir, ok := worktree.WorktreeParseDir(`{"directory":"/home/dev/ws","strategy":"git_worktree"}`)
	if !ok || dir != "/home/dev/ws" {
		t.Errorf("WorktreeParseDir = %q, %v, want /home/dev/ws, true", dir, ok)
	}
	if _, ok := worktree.WorktreeParseDir("not json"); ok {
		t.Error("WorktreeParseDir(invalid) should return not-ok")
	}
	if _, ok := worktree.WorktreeParseDir(`{"directory":""}`); ok {
		t.Error("WorktreeParseDir(empty) should return not-ok")
	}
}

func TestOpencode2LatestVersionCancelledCtx(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("opencode2 should implement UpgradeChecker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestOpencode2NewerThanBetaBuilds(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker := mustChecker(t, a)
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "newer beta build", a: "0.0.0-beta-18866", b: "0.0.0-beta-17823", want: true},
		{name: "older beta build", a: "0.0.0-beta-17823", b: "0.0.0-beta-18866", want: false},
		{name: "equal beta build", a: "0.0.0-beta-18866", b: "0.0.0-beta-18866", want: false},
		{name: "cross digit length newer", a: "0.0.0-beta-10000", b: "0.0.0-beta-9999", want: true},
		{name: "cross digit length older", a: "0.0.0-beta-9999", b: "0.0.0-beta-10000", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checker.NewerThan(tc.a, tc.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NewerThan(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestOpencode2NewerThanInvalidVersion(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	checker := mustChecker(t, a)
	if _, err := checker.NewerThan("not-a-version", "0.0.0-beta-18866"); err == nil {
		t.Error("NewerThan(invalid) should error")
	}
}

func TestOpencode2ConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode2 should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "opencode-*.json*" {
		t.Errorf("SnippetPattern = %q", merger.SnippetPattern())
	}
	if merger.VMConfigPath("/home/user") != filepath.Join("/home/user", ".config", "opencode", "opencode.jsonc") {
		t.Errorf("VMConfigPath = %q", merger.VMConfigPath("/home/user"))
	}
}

func TestOpencode2ConfigFileNames(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("opencode2 should implement ConfigMerger")
	}
	names := merger.ConfigFileNames()
	want := []string{
		"config.json",
		"opencode.json",
		"opencode.jsonc",
		"opencode.json5",
		"opencode.yaml",
		"opencode.yml",
	}
	if !slices.Equal(names, want) {
		t.Errorf("ConfigFileNames = %v, want %v", names, want)
	}
}

func TestOpencode2ProvisionRules(t *testing.T) {
	a, _ := agent.Lookup("opencode2")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("opencode2 should implement Provisioner")
	}
	rules := provisioner.ProvisionRules()
	if len(rules) == 0 {
		t.Fatal("ProvisionRules must not be empty")
	}
	found := false
	for _, r := range rules {
		if r.Dir == ".config/opencode" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProvisionRules missing .config/opencode: %+v", rules)
	}
}
