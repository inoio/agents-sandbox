package vm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestResolveServeHostPortNotServeOnly(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{}, 0); got != 0 {
		t.Errorf("resolveServeHostPort(not serve-only) = %d, want 0", got)
	}
}

func TestResolveServeHostPortPassthrough(t *testing.T) {
	if got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 4096); got != 4096 {
		t.Errorf("resolveServeHostPort(planned 4096) = %d, want 4096", got)
	}
}

func TestResolveServeHostPortProbesWhenZero(t *testing.T) {
	got := resolveServeHostPort(options.RunOptions{ServeOnly: true}, 0)
	if got == 0 || got < options.ServeOnlyBasePort {
		t.Errorf("resolveServeHostPort(planned 0) = %d, want a probed port >= %d", got, options.ServeOnlyBasePort)
	}
}

func symlinkedDir(t *testing.T) (realDir, linkDir string) {
	t.Helper()
	realDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	linkDir = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	return realDir, linkDir
}

func TestResolveWorkspaceDirResolvesSymlinkInCwd(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)
	t.Chdir(linkDir)

	got, err := resolveWorkspaceDir()
	if err != nil {
		t.Fatalf("resolveWorkspaceDir() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspaceDir() = %q, want %q", got, realDir)
	}
}

func TestResolveWorkspacePathResolvesSymlink(t *testing.T) {
	realDir, linkDir := symlinkedDir(t)

	got, err := resolveWorkspacePath(linkDir)
	if err != nil {
		t.Fatalf("resolveWorkspacePath() error = %v", err)
	}
	if got != realDir {
		t.Errorf("resolveWorkspacePath() = %q, want %q", got, realDir)
	}
}

func TestResolveWorkspacePathError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := resolveWorkspacePath(missing); err == nil {
		t.Fatal("expected error when the path does not exist")
	}
}

// TestResolveWorkspaceDirGetwdError covers the branch where the current
// working directory no longer exists (e.g. it was deleted while running), so
// os.Getwd itself fails.
func TestResolveWorkspaceDirGetwdError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	if _, err := resolveWorkspaceDir(); err == nil {
		t.Fatal("expected error when the current directory no longer exists")
	}
}

// TestPrepareSandboxResolveWorkspaceDirError covers the propagation of a
// workspace-directory resolution failure through PrepareSandbox.
func TestPrepareSandboxResolveWorkspaceDirError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove cwd: %v", err)
	}

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the workspace directory cannot be resolved")
	}
}
