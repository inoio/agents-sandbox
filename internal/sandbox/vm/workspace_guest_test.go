package vm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestGuestWorkspacePath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindRoot string
		cwd      string
		want     string
	}{
		{"cwd is bind root", "/workspace", "/workspace", "/workspace"},
		{"nested directory", "/repo", "/repo/internal/foo", "/workspace/internal/foo"},
		{"nested worktree root", "/root/wt", "/root/wt", "/workspace"},
		{"rel error falls back to mount path", "relative/bind", "/repo", mounts.WorkspaceMountPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestWorkspacePath(tc.bindRoot, tc.cwd); got != tc.want {
				t.Errorf("guestWorkspacePath(%q, %q) = %q, want %q", tc.bindRoot, tc.cwd, got, tc.want)
			}
		})
	}
}

func TestResolveBindLayoutOutsideWorktreeWarns(t *testing.T) {
	repo := testutil.InitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testutil.RunGit(t, repo, "worktree", "add", wt)

	ui := &termio.Mock{}
	layout, guestCWD := resolveBindLayout(wt, ui)

	if guestCWD != mounts.WorkspaceMountPath {
		t.Errorf("guestCWD = %q, want %q", guestCWD, mounts.WorkspaceMountPath)
	}
	if layout.BindRoot != wt {
		t.Errorf("BindRoot = %q, want %q", layout.BindRoot, wt)
	}
	if len(ui.WarnCalls) != 1 {
		t.Fatalf("WarnCalls = %d, want 1", len(ui.WarnCalls))
	}
	if !strings.Contains(ui.WarnCalls[0], "outside its checkout") {
		t.Errorf("warning %q missing %q", ui.WarnCalls[0], "outside its checkout")
	}
	if !strings.Contains(ui.WarnCalls[0], repo) {
		t.Errorf("warning %q missing checkout root %q", ui.WarnCalls[0], repo)
	}
}

func TestSessionGuestCWD(t *testing.T) {
	s := &Session{guestCWD: "/workspace/sub"}
	if got := s.GuestCWD(); got != "/workspace/sub" {
		t.Errorf("GuestCWD() = %q, want %q", got, "/workspace/sub")
	}
}

func TestSetUpSandboxReturnsGuestCWD(t *testing.T) {
	origDaemon := SetDaemonShellFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonShellFunc(origDaemon) })

	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(cp.UserAgentConfigDir(opencodeAgent(t)), "opencode-x.json5")
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeAgent(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	target, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{},
		cfs,
		ui,
		false,
		vmBootStarted,
		"/workspace/sub",
	)
	if err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}
	if target != "/workspace/sub" {
		t.Errorf("target = %q, want %q", target, "/workspace/sub")
	}
}
