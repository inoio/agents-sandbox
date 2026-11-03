package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// synthesizeBareWorktree builds a bare checkout whose single worktree lives
// inside the bare root, by hand: <root>/.git is a bare git dir (core.bare) and
// <root>/main/.git is a link file pointing into it. It pins the host home and
// XDG directories, installs the real path resolver, and chdirs into the
// worktree. The worktree-level .agents-sandbox is created only when
// withWorktreeConfig is set.
func synthesizeBareWorktree(t *testing.T, withWorktreeConfig bool) (worktree string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	configpaths.WithBareCheckoutLocator(t, git.BareCheckoutRoots)

	root := t.TempDir()
	worktree = filepath.Join(root, "main")
	writeFileAt(t, filepath.Join(root, ".git", "config"), "[core]\n\tbare = true\n")
	// A real linked worktree's registration carries a commondir marker; the
	// identity resolver requires it to tell a worktree from a --separate-git-dir
	// checkout, so the synthetic layout must include it.
	writeFileAt(t, filepath.Join(root, ".git", "worktrees", "main", "commondir"), "../..\n")
	writeFileAt(t, filepath.Join(worktree, ".git"), "gitdir: ../.git/worktrees/main\n")
	if withWorktreeConfig {
		if err := os.MkdirAll(filepath.Join(worktree, ".agents-sandbox"), 0o750); err != nil {
			t.Fatalf("MkdirAll(worktree config): %v", err)
		}
	}
	t.Chdir(worktree)
	return worktree
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
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

// TestWarnIgnoredProjectConfigWarnsForBareWorktreeConfig asserts the warning
// names the worktree-level .agents-sandbox a bare checkout ignores.
func TestWarnIgnoredProjectConfigWarnsForBareWorktreeConfig(t *testing.T) {
	worktree := synthesizeBareWorktree(t, true)
	ignored := physicalPath(t, filepath.Join(worktree, ".agents-sandbox"))
	bareBase := filepath.Join(physicalPath(t, filepath.Dir(worktree)), ".agents-sandbox")

	ui := &termio.Mock{}
	WarnIgnoredProjectConfig(configpaths.Get(), ui)

	if len(ui.WarnCalls) != 1 {
		t.Fatalf("WarnCalls = %v, want exactly one warning", ui.WarnCalls)
	}
	warning := ui.WarnCalls[0]
	if !strings.Contains(warning, ignored) {
		t.Errorf("warning %q does not name the ignored directory %q", warning, ignored)
	}
	if !strings.Contains(warning, "read from the bare root, not the worktree") {
		t.Errorf("warning %q does not state that project config is read from the bare root, not the worktree", warning)
	}
	if strings.Contains(warning, bareBase) {
		t.Errorf("warning %q names the bare base %q; it must name the ignored worktree copy instead", warning, bareBase)
	}
}

// TestWarnIgnoredProjectConfigSilentWithoutWorktreeConfig asserts no warning
// when the worktree has no .agents-sandbox to ignore.
func TestWarnIgnoredProjectConfigSilentWithoutWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, false)

	ui := &termio.Mock{}
	WarnIgnoredProjectConfig(configpaths.Get(), ui)

	if len(ui.WarnCalls) != 0 {
		t.Errorf("WarnCalls = %v, want no warning", ui.WarnCalls)
	}
}

// TestWarnIgnoredProjectConfigWarnsFromWorktreeSubdirectory pins that a launch
// from a directory inside the worktree still warns about the worktree-root copy.
func TestWarnIgnoredProjectConfigWarnsFromWorktreeSubdirectory(t *testing.T) {
	worktree := synthesizeBareWorktree(t, true)
	sub := filepath.Join(worktree, "internal")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", sub, err)
	}
	t.Chdir(sub)

	ui := &termio.Mock{}
	WarnIgnoredProjectConfig(configpaths.Get(), ui)

	ignored := physicalPath(t, filepath.Join(worktree, ".agents-sandbox"))
	if len(ui.WarnCalls) != 1 {
		t.Fatalf("WarnCalls = %v, want exactly one warning", ui.WarnCalls)
	}
	if !strings.Contains(ui.WarnCalls[0], ignored) {
		t.Errorf("warning %q does not name the ignored directory %q", ui.WarnCalls[0], ignored)
	}
}

// assertWarnsIgnoredWorktreeConfig fails unless ui recorded the bare-checkout
// warning, pinning that an exported entry point still runs the warning after the
// path resolver moved from a package global onto an injected argument.
func assertWarnsIgnoredWorktreeConfig(t *testing.T, ui *termio.Mock) {
	t.Helper()
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "read from the bare root, not the worktree") {
		t.Errorf("WarnCalls = %v, want the ignored-worktree-config warning", ui.WarnCalls)
	}
}

// TestEnsureImageWarnsIgnoredWorktreeConfig pins that the exported EnsureImage
// entry still warns about an ignored worktree-level .agents-sandbox. The warning
// runs before any docker work, so a docker failure is immaterial to the assert.
func TestEnsureImageWarnsIgnoredWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, true)
	docker.WithDefaultErrorDockerMock(t)
	ui := &termio.Mock{}

	_, _ = EnsureImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		agentOpencode(t),
		"slug",
		BuildOptions{AgentVersion: testAgentVersion},
		ui,
	)

	assertWarnsIgnoredWorktreeConfig(t, ui)
}

// TestBuildWarnsIgnoredWorktreeConfig pins the same warning on the Build entry.
func TestBuildWarnsIgnoredWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, true)
	docker.WithDefaultErrorDockerMock(t)
	ui := &termio.Mock{}

	_, _ = Build(
		context.Background(),
		docker.Get(),
		&msb.MockMsbClient{},
		configpaths.Get(),
		agentOpencode(t),
		"slug",
		BuildOptions{AgentVersion: testAgentVersion},
		ui,
	)

	assertWarnsIgnoredWorktreeConfig(t, ui)
}

// TestPreviewImageWarnsIgnoredWorktreeConfig pins the same warning on the
// PreviewImage entry.
func TestPreviewImageWarnsIgnoredWorktreeConfig(t *testing.T) {
	synthesizeBareWorktree(t, true)
	docker.WithDefaultErrorDockerMock(t)
	ui := &termio.Mock{}

	_, _ = PreviewImage(
		context.Background(),
		docker.Get(),
		configpaths.Get(),
		agentOpencode(t),
		"slug",
		BuildOptions{AgentVersion: testAgentVersion},
		ui,
	)

	assertWarnsIgnoredWorktreeConfig(t, ui)
}
