package prepare

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
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

func TestSessionGuestCWD(t *testing.T) {
	s := &Session{guestCWD: "/workspace/sub"}
	if got := s.GuestCWD(); got != "/workspace/sub" {
		t.Errorf("GuestCWD() = %q, want %q", got, "/workspace/sub")
	}
}
