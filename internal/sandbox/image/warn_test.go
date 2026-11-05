package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

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
