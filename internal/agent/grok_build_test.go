package agent_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestGrokBuildLookup(t *testing.T) {
	a, ok := agent.Lookup("grok-build")
	if !ok {
		t.Fatal("Lookup(\"grok-build\") returned not-ok")
	}
	if a.Name() != "grok-build" {
		t.Errorf("Name() = %q, want grok-build", a.Name())
	}
	if a.ConfigDirName() != "grok-build" {
		t.Errorf("ConfigDirName = %q, want grok-build", a.ConfigDirName())
	}
	if !a.Experimental() {
		t.Error("Experimental() = false, want true")
	}
	if !slices.Contains(agent.Names(), "grok-build") {
		t.Errorf("Names() = %v, want to include grok-build", agent.Names())
	}
}

func TestGrokBuildImageSpec(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	spec := a.ImageSpec()
	if spec.VersionArg != "GROK_BUILD_VERSION" {
		t.Errorf("VersionArg = %q, want GROK_BUILD_VERSION", spec.VersionArg)
	}
	for _, want := range []string{
		`https://x.ai/cli`,
		`https://storage.googleapis.com/grok-build-public-artifacts/cli`,
		`grok-${GROK_BUILD_VERSION}-linux-$grok_arch.gz`,
		`install -m 0755 "$grok_dir/grok" /opt/agents-sandbox/bin/grok`,
		`/opt/agents-sandbox/bin/grok --version`,
	} {
		if !strings.Contains(spec.InstallCommand, want) {
			t.Errorf("InstallCommand = %q, want it to contain %q", spec.InstallCommand, want)
		}
	}
	if want := `'[cli]' 'auto_update = false' > /etc/grok/requirements.toml`; !strings.Contains(
		spec.RunnerCommand,
		want,
	) {
		t.Errorf("RunnerCommand = %q, want it to contain %q", spec.RunnerCommand, want)
	}
	if strings.Contains(spec.InstallCommand, "requirements.toml") {
		t.Errorf("InstallCommand = %q must not write the runner-stage requirements pin", spec.InstallCommand)
	}
	if got := spec.AgentEnv["GROK_DISABLE_AUTOUPDATER"]; got != "1" || len(spec.AgentEnv) != 1 {
		t.Errorf("AgentEnv = %v, want only GROK_DISABLE_AUTOUPDATER=1", spec.AgentEnv)
	}
	if spec.RequiresNode || spec.OwnsToolTree {
		t.Errorf("RequiresNode = %v, OwnsToolTree = %v, want both false", spec.RequiresNode, spec.OwnsToolTree)
	}
	if len(spec.RuntimeLibs) != 0 {
		t.Errorf("RuntimeLibs = %v, want none (the binary is static)", spec.RuntimeLibs)
	}
}

// fakeGrokBuildRelease runs the install command's resolve-and-download part with
// curl and uname replaced by shell functions, so the architecture mapping and
// the fallback to the GCS bucket are observable without a network or root.
// machine is what `uname -m` reports; primaryFails makes every x.ai download
// fail. The command is cut before it installs into the tool tree, and
// "downloaded <url>" records each successful fetch.
func fakeGrokBuildRelease(t *testing.T, machine string, primaryFails bool) (string, error) {
	t.Helper()
	a, _ := agent.Lookup("grok-build")
	install := a.ImageSpec().InstallCommand
	install = strings.ReplaceAll(install, "/tmp/grok-build", t.TempDir())
	install, _, found := strings.Cut(install, " && gzip -dc")
	if !found {
		t.Fatalf("InstallCommand = %q has no decompress step", a.ImageSpec().InstallCommand)
	}
	stubs := `uname() { echo '` + machine + `'; }; ` +
		`curl() { while [ "$1" != -o ]; do url="$1"; shift; done; ` +
		`case "$url" in https://x.ai/*) [ "$PRIMARY_FAILS" = 1 ] && return 22 ;; esac; ` +
		`echo "downloaded $url"; printf 'binary' > "$2"; }; `
	cmd := exec.Command("sh", "-c", stubs+install)
	primary := "0"
	if primaryFails {
		primary = "1"
	}
	cmd.Env = append(cmd.Environ(), "PRIMARY_FAILS="+primary, "GROK_BUILD_VERSION=1.0.89")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestGrokBuildInstallMapsArchitecture(t *testing.T) {
	for machine, arch := range map[string]string{
		"x86_64":  "x86_64",
		"amd64":   "x86_64",
		"aarch64": "aarch64",
		"arm64":   "aarch64",
	} {
		t.Run(machine, func(t *testing.T) {
			output, err := fakeGrokBuildRelease(t, machine, false)
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, output)
			}
			if want := "downloaded https://x.ai/cli/grok-1.0.89-linux-" + arch + ".gz"; !strings.Contains(
				output,
				want,
			) {
				t.Errorf("output = %q, want %q", output, want)
			}
		})
	}
}

func TestGrokBuildInstallFallsBackToTheBucket(t *testing.T) {
	output, err := fakeGrokBuildRelease(t, "x86_64", true)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}
	want := "downloaded https://storage.googleapis.com/grok-build-public-artifacts/cli/grok-1.0.89-linux-x86_64.gz"
	if !strings.Contains(output, want) {
		t.Errorf("output = %q, want the bucket fallback %q", output, want)
	}
}

func TestGrokBuildInstallRejectsUnsupportedArchitecture(t *testing.T) {
	output, err := fakeGrokBuildRelease(t, "riscv64", false)
	if err == nil {
		t.Fatalf("install succeeded on riscv64, want a build failure:\n%s", output)
	}
	if !strings.Contains(output, "grok-build: unsupported architecture riscv64") ||
		strings.Contains(output, "downloaded") {
		t.Errorf("output = %q, want an unsupported-architecture error and no download", output)
	}
}

func TestGrokBuildAttachCommandQuotesArgs(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	runner, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("grok-build should implement AttachRunner")
	}
	if got, want := runner.AttachCommand("/workspace", []string{"--model", "x"}), "grok '--model' 'x'"; got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestGrokBuildAttachCommandPreservesShellSpecialPrompts(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	runner, _ := agent.AsAttachRunner(a)
	prompts := []string{"Explain this project's tests", "cost in $HOME; `id` && echo hi", ""}
	command := runner.AttachCommand("/workspace", prompts)
	// printf stands in for grok so the shell's word splitting is observable.
	script := "grok() { printf '[%s]' \"$@\"; }; " + command
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	if want := "[Explain this project's tests][cost in $HOME; `id` && echo hi][]"; string(output) != want {
		t.Errorf("forwarded args = %s, want %s", output, want)
	}
}

func TestGrokBuildConfigMerger(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	merger, ok := agent.AsConfigMerger(a)
	if !ok {
		t.Fatal("grok-build should implement ConfigMerger")
	}
	if merger.SnippetPattern() != "config*.toml" {
		t.Errorf("SnippetPattern = %q, want config*.toml", merger.SnippetPattern())
	}
	if got := merger.VMConfigPath("/home/dev"); got != filepath.Join("/home/dev", ".grok", "config.toml") {
		t.Errorf("VMConfigPath = %q", got)
	}
	if names := merger.ConfigFileNames(); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("ConfigFileNames = %v, want [config.toml]", names)
	}
}

// TestGrokBuildProvisionRulesCopyOnlyCredentialFreeFiles pins the drop-in copy
// to ~/.grok's user-authored, credential-free files: the global rules, skills,
// sandbox profiles, and workflows. config.toml (a model's api_key and MCP
// headers sit beside the settings), the login, hooks and plugins (host
// commands, MCP configs), the cloud-synced policy cache, the trust stores, and
// runtime state stay on the host.
func TestGrokBuildProvisionRulesCopyOnlyCredentialFreeFiles(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	provisioner, ok := agent.AsProvisioner(a)
	if !ok {
		t.Fatal("grok-build should implement Provisioner")
	}
	copied := []string{
		"AGENTS.md",
		"Agents.md",
		"AGENT.md",
		"CLAUDE.md",
		"Claude.md",
		"skills/review/SKILL.md",
		"sandbox.toml",
		"workflows/release.rhai",
	}
	notCopied := []string{
		"config.toml",
		"hooks/guard.json",
		"plugins/local/plugin.json",
		"sessions/abc/AGENTS.md",
		"auth.json",
		"requirements.toml",
		"managed_config.toml",
		"trusted_folders.toml",
		"trusted-plugins",
		"sessions/abc/events.jsonl",
	}
	hostHome := t.TempDir()
	for _, rel := range append(slices.Clone(copied), notCopied...) {
		path := filepath.Join(hostHome, ".grok", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vmHome := "/home/dev"
	var got []string
	_, err := agent.EvalProvisionRules(provisioner.ProvisionRules(), hostHome, vmHome,
		func(dst string, _ []byte, _ os.FileMode) error {
			rel, _ := filepath.Rel(filepath.Join(vmHome, ".grok"), dst)
			got = append(got, filepath.ToSlash(rel))
			return nil
		})
	if err != nil {
		t.Fatalf("EvalProvisionRules: %v", err)
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(copied))
	if !slices.Equal(got, want) {
		t.Errorf("copied %v, want %v", got, want)
	}
}

func TestGrokBuildVersion(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatal("grok-build should implement VersionProvider")
	}
	if provider.VersionCmd() != "grok --version" {
		t.Errorf("VersionCmd = %q, want grok --version", provider.VersionCmd())
	}
	got, err := provider.ParseVersion("grok 1.0.89\n")
	if err != nil || got != "1.0.89" {
		t.Errorf("ParseVersion = %q, %v, want 1.0.89", got, err)
	}
}

func TestGrokBuildUpgradeChecker(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		t.Fatal("grok-build should implement UpgradeChecker")
	}
	newer, err := checker.NewerThan("1.0.90", "1.0.89")
	if err != nil || !newer {
		t.Errorf("NewerThan(1.0.90, 1.0.89) = %v, %v, want true", newer, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checker.LatestVersion(ctx); err == nil {
		t.Error("LatestVersion with cancelled ctx should error")
	}
}

func TestGrokBuildHasNoDaemonOrWorktree(t *testing.T) {
	a, _ := agent.Lookup("grok-build")
	if _, ok := agent.AsDaemonProvider(a); ok {
		t.Error("grok-build must not implement DaemonProvider")
	}
	if _, ok := agent.AsWorktreeProvider(a); ok {
		t.Error("grok-build must not implement WorktreeProvider")
	}
	if _, ok := agent.AsEventStreamProvider(a); ok {
		t.Error("grok-build must not implement EventStreamProvider")
	}
	if _, ok := agent.AsGuestToolRequirer(a); ok {
		t.Error("grok-build must not implement GuestToolRequirer (no daemon tools)")
	}
}
