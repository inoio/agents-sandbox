package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// writeInsideBareCheckout builds a bare checkout whose worktree lives inside the
// bare root: root/.git is a bare clone and root/main is a worktree of it.
func writeInsideBareCheckout(t *testing.T) string {
	t.Helper()
	origin := testutil.InitRepo(t)
	root := mustResolvePath(t, t.TempDir())
	testutil.RunGit(t, root, "clone", "--bare", origin, ".git")
	testutil.RunGit(t, filepath.Join(root, ".git"), "worktree", "add", filepath.Join(root, "main"))
	return root
}

func TestBareCheckoutRootsInsideBareCheckout(t *testing.T) {
	root := writeInsideBareCheckout(t)
	worktree := filepath.Join(root, "main")
	subdirectory := filepath.Join(worktree, "internal")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", subdirectory, err)
	}

	tests := []struct {
		name             string
		cwd              string
		wantWorktreeRoot string
	}{
		{name: "bare root", cwd: root, wantWorktreeRoot: root},
		{name: "worktree root", cwd: worktree, wantWorktreeRoot: worktree},
		{name: "worktree subdirectory", cwd: subdirectory, wantWorktreeRoot: worktree},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bareRoot, worktreeRoot, inside := BareCheckoutRoots(tt.cwd)

			if !inside {
				t.Fatalf("BareCheckoutRoots(%q) reported not inside a bare checkout", tt.cwd)
			}
			if bareRoot != root {
				t.Errorf("bareRoot = %q, want %q", bareRoot, root)
			}
			if worktreeRoot != tt.wantWorktreeRoot {
				t.Errorf("worktreeRoot = %q, want %q", worktreeRoot, tt.wantWorktreeRoot)
			}
		})
	}
}

func TestBareCheckoutRootsOutsideBareCheckout(t *testing.T) {
	origin := testutil.InitRepo(t)
	outsideBareRoot := t.TempDir()
	testutil.RunGit(t, outsideBareRoot, "clone", "--bare", origin, ".git")
	outsideWorktree := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, filepath.Join(outsideBareRoot, ".git"), "worktree", "add", outsideWorktree)

	tests := []struct {
		name string
		cwd  string
	}{
		{name: "non-bare repository", cwd: testutil.InitRepo(t)},
		{name: "directory outside any repository", cwd: t.TempDir()},
		{name: "worktree outside its bare checkout", cwd: outsideWorktree},
		{name: "unresolvable directory", cwd: filepath.Join(t.TempDir(), "does-not-exist")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bareRoot, worktreeRoot, inside := BareCheckoutRoots(tt.cwd)

			if inside || bareRoot != "" || worktreeRoot != "" {
				t.Errorf("BareCheckoutRoots(%q) = (%q, %q, %v), want (\"\", \"\", false)",
					tt.cwd, bareRoot, worktreeRoot, inside)
			}
		})
	}
}
