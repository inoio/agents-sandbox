package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestBuildAttachCommand(t *testing.T) {
	got := buildAttachCommand("/workspace", []string{"foo"})
	if !strings.Contains(got, "opencode attach") {
		t.Errorf("expected 'opencode attach' in command, got %q", got)
	}
	if !strings.Contains(got, "http://127.0.0.1:4096") {
		t.Errorf("expected daemon URL in command, got %q", got)
	}
	if !strings.Contains(got, "--dir /workspace") {
		t.Errorf("expected --dir /workspace in command, got %q", got)
	}
	if strings.Contains(got, "--auto") {
		t.Errorf("did not expect --auto flag (removed by human), got %q", got)
	}
	if !strings.Contains(got, "foo") {
		t.Errorf("expected forwarded args in command, got %q", got)
	}
}

func TestBuildAttachCommandWorktreeTarget(t *testing.T) {
	got := buildAttachCommand("/home/dev/.local/share/opencode/worktree/abc/feat", nil)
	if !strings.Contains(got, "--dir /home/dev/.local/share/opencode/worktree/abc/feat") {
		t.Errorf("expected worktree dir in command, got %q", got)
	}
}

func TestServeOnlyMessage(t *testing.T) {
	msg := serveOnlyMessage("127.0.0.1", "4096")
	for _, want := range []string{"Connect Opencode Desktop to", "http://127.0.0.1:4096", "OPENCODE_SERVER_PASSWORD", "Ctrl-D"} {
		if !strings.Contains(msg, want) {
			t.Errorf("serveOnlyMessage missing %q in:\n%s", want, msg)
		}
	}
}

func TestRunServeOnlyBlocksUntilCancel(t *testing.T) {
	sb := msb.NewMockSandbox(msb.SandboxOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	ui := &termio.Mock{}
	go func() { got <- runServeOnly(ctx, sb, ui) }()
	cancel()
	select {
	case err := <-got:
		if err == nil {
			t.Error("expected ctx.Err from completed runServeOnly")
		}
	case <-time.After(2 * time.Second):
		t.Error("runServeOnly did not return after ctx cancel")
	}
	if len(ui.InfoCalls) == 0 {
		t.Fatal("expected Infof call to print connect URL, got no info calls")
	}
	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "http://127.0.0.1:4096") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected connect URL 'http://127.0.0.1:4096' in info output, got %v", ui.InfoCalls)
	}
}

func TestRunAttachUsesAttachWithForRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease("roottest")
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "roottest", ui,
		options.RunOptions{Root: true, ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "root" {
		t.Errorf("AttachUser = %q, want %q", sb.AttachUser, "root")
	}
}

func TestRunAttachDefaultDoesNotUseRoot(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	ui := &termio.Mock{}
	sb := &msb.MockSandbox{AttachCode: 0}

	release, err := state.AcquireClientLease("defaulttest")
	if err != nil {
		t.Fatalf("state.AcquireClientLease: %v", err)
	}
	release()

	err = runAttach(context.Background(), sb, "defaulttest", ui,
		options.RunOptions{ReapPolicy: options.NewReapPolicy(false, 5)}, "-l")
	if err != nil {
		t.Fatalf("runAttach failed: %v", err)
	}
	if sb.AttachUser != "" {
		t.Errorf("AttachUser = %q, want empty for default (non-root) attach", sb.AttachUser)
	}
}
