package agent_test

import (
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
)

func TestOpencodeAttachCommand(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	cmd, ok := agent.AsAttachRunner(a)
	if !ok {
		t.Fatal("opencode should implement AttachRunner")
	}
	got := cmd.AttachCommand("/workspace", []string{"--print-logs"})
	want := "opencode attach http://127.0.0.1:4096 --dir /workspace --print-logs"
	if got != want {
		t.Errorf("AttachCommand = %q, want %q", got, want)
	}
}

func TestOpencodeWorktreeCreateCmdUsesName(t *testing.T) {
	a, _ := agent.Lookup("opencode")
	daemon, ok := agent.AsDaemonProvider(a)
	if !ok {
		t.Fatal("opencode should implement DaemonProvider")
	}
	got := daemon.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feature/x"})
	want := "curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"feature/x\"}'"
	if got != want {
		t.Errorf("WorktreeCreateCmd = %q, want %q", got, want)
	}
}
