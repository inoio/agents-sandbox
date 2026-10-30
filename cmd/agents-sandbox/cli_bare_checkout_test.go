package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// setupBareCheckoutFixtures builds a bare checkout whose single worktree lives
// inside the bare root, installs the standard run/shell command fixture inside
// that worktree, and returns the command, UI, and bare root. The bare layout
// needs the real path resolver, so HOME and every XDG directory are pinned to
// temp dirs to keep host state out of reach. Not parallel: t.Setenv/t.Chdir
// forbid it.
func setupBareCheckoutFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock, string) {
	t.Helper()
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	cmd, ui := setupRunMocks(t, &sandboxmsb.MockMsbClient{}, &sandboxmsb.MockSandbox{}, args...)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Override the command fixture's mock paths with the real resolver; cleanup
	// runs LIFO, so the mock (and then the fail-fast default) is restored.
	configpaths.WithRealConfigPaths(t)
	t.Chdir(filepath.Join(root, "main"))
	return cmd, ui, root
}

// TestBuildDockerfileCommandReadsBareRoot asserts that a bare checkout launched
// from a worktree prints the bare root's project Dockerfile, never the ignored
// worktree copy, and warns about that ignored directory.
func TestBuildDockerfileCommandReadsBareRoot(t *testing.T) {
	cmd, ui, root := setupBareCheckoutFixtures(t, cmdBuild, cmdDockerfile)
	bareConfigDir := filepath.Join(root, ".agents-sandbox")
	ignoredDir := filepath.Join(root, "main", ".agents-sandbox")
	for _, dir := range []string{bareConfigDir, ignoredDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	testutil.WritePath(t, filepath.Join(bareConfigDir, "Dockerfile"), "FROM bare-root\n")
	testutil.WritePath(t, filepath.Join(ignoredDir, "Dockerfile"), "FROM worktree\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := ui.StdOutBuffer.String()
	if !strings.Contains(out, "bare-root") {
		t.Errorf("dockerfile output missing the bare-root project Dockerfile; got:\n%s", out)
	}
	if strings.Contains(out, "worktree") {
		t.Errorf("dockerfile output must not contain the ignored worktree copy; got:\n%s", out)
	}
	assertWarnContains(t, ui, physicalPath(t, ignoredDir))
}

// TestBuildDockerfileWritesDiagnosticsToStderr asserts a near-miss marker is
// surfaced on the warn channel while stdout stays a clean, pasteable
// Dockerfile. The case variant is a near miss: enablement is case-sensitive, so
// it does not enable, and detection is case-insensitive, so it warns.
func TestBuildDockerfileWritesDiagnosticsToStderr(t *testing.T) {
	cmd, ui, root := setupBareCheckoutFixtures(t, cmdBuild, cmdDockerfile)
	bareConfigDir := filepath.Join(root, ".agents-sandbox")
	if err := os.MkdirAll(bareConfigDir, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", bareConfigDir, err)
	}
	testutil.WritePath(t, filepath.Join(bareConfigDir, "Dockerfile"),
		"FROM debian:trixie-slim\n# Agents-Sandbox:docker\nRUN echo hi\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := ui.StdOutBuffer.String()
	if !strings.Contains(out, "FROM debian:trixie-slim") {
		t.Errorf("stdout must carry the rendered Dockerfile; got:\n%s", out)
	}
	if strings.Contains(out, "near-miss") || strings.Contains(out, "docker: on") {
		t.Errorf("diagnostics must not pollute stdout; got:\n%s", out)
	}
	joined := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(joined, "near-miss docker marker") {
		t.Errorf("near-miss diagnostic must land on the warn channel; got %v", ui.WarnCalls)
	}
}

// TestRunCommandReadsBareRoot asserts the real run path resolves the bare root
// (reaching EnsureImage) by warning about the ignored worktree .agents-sandbox.
func TestRunCommandReadsBareRoot(t *testing.T) {
	cmd, ui, root := setupBareCheckoutFixtures(t, cmdRun, flagDryRunVM)
	ignoredDir := filepath.Join(root, "main", ".agents-sandbox")
	if err := os.MkdirAll(ignoredDir, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", ignoredDir, err)
	}

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertWarnContains(t, ui, physicalPath(t, ignoredDir))
}

// physicalPath resolves symlinks the way the resolver derives the bare base, so
// expectations hold even when the temp base contains a symlink.
func physicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

// setupBareCheckoutCommandFixtures is setupBareCheckoutFixtures for the config
// commands: the standard command fixtures plus the real path resolver, so config
// resolution reads the temp config tree. Not parallel: t.Setenv/t.Chdir forbid it.
func setupBareCheckoutCommandFixtures(t *testing.T, args ...string) (*cobra.Command, *termio.Mock) {
	t.Helper()
	origin := testutil.InitRepo(t)
	root := t.TempDir()
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))

	cmd, ui := setupCommandFixtures(t, args...)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Override the command fixture's mock paths with the real resolver.
	configpaths.WithRealConfigPaths(t)
	// The bare root itself records the legacy identity (a worktree inside it does
	// not), so the config-resolution tests run from the bare root.
	t.Chdir(root)
	return cmd, ui
}

// bareRootLegacyConfigDir returns the bare root's pre-split per-slug user config
// directory, the layer the resolver must read before adoption renames it onto the
// repo slug, failing the test if the fixture has no legacy slug.
func bareRootLegacyConfigDir(t *testing.T) (git.Identity, string) {
	t.Helper()
	identity := mustIdentity(t, ".")
	if len(identity.LegacyRepoSlugs) == 0 {
		t.Fatal("bare root must record a legacy config slug")
	}
	return identity, filepath.Join(configpaths.Get().UserConfigDir(), identity.LegacyRepoSlugs[0])
}

// TestBareRootReadsLegacyConfigBeforeMigration asserts the resolver reads a bare
// root's pre-split per-slug config: adoption renames that directory onto the repo
// slug only later in the run, so loading the repo-slug layer would use defaults
// instead of the user's saved settings (PR #244 review).
func TestBareRootReadsLegacyConfigBeforeMigration(t *testing.T) {
	cmd, ui := setupBareCheckoutCommandFixtures(t, cmdConfig, cmdAgent, "opencode")
	_, legacyDir := bareRootLegacyConfigDir(t)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteYAML(t, legacyDir, "config.yaml", map[string]any{"provision-host-config": false})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config agent: %v", err)
	}
	if joined := strings.Join(ui.OutCalls, "\n"); !strings.Contains(joined, "provision-host-config=false") {
		t.Errorf("bare root must read the legacy user config before migration, got:\n%s", joined)
	}
}

// TestBareRootConfigPathsShowsEffectiveLayer asserts config paths reports the
// legacy layer dir it actually reads while keeping the repo-slug label, so a
// bare root's pre-migration config is not presented as the repo slug's.
func TestBareRootConfigPathsShowsEffectiveLayer(t *testing.T) {
	cmd, ui := setupBareCheckoutCommandFixtures(t, cmdConfig, cmdPaths)
	identity, legacyDir := bareRootLegacyConfigDir(t)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteYAML(t, legacyDir, "config.yaml", map[string]any{"cpus": 2})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("config paths: %v", err)
	}
	out := strings.Join(ui.OutCalls, "\n")
	if !strings.Contains(out, legacyDir) {
		t.Errorf("config paths must list the effective legacy layer dir %q, got:\n%s", legacyDir, out)
	}
	if !strings.Contains(out, "repo slug:  "+identity.RepoSlug) {
		t.Errorf("config paths must still label the repo slug %q, got:\n%s", identity.RepoSlug, out)
	}
}
