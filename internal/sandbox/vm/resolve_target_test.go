package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestResolveTargetListError covers the worktree-list failure branch of
// ResolveTarget: a shell error listing worktrees is propagated.
func TestResolveTargetListError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("list failed")}
	_, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err == nil {
		t.Fatal("expected error when listing worktrees fails")
	}
}

// TestResolveTargetCreateNonSuccess covers the non-success create-result branch
// of ResolveTarget.
func TestResolveTargetCreateNonSuccess(t *testing.T) {
	ui := &termio.Mock{}
	provider := opencodeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd:                  msb.NewTestResult(false, 1, "", "failed", nil),
		},
	}
	_, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err == nil {
		t.Fatal("expected error when the create command exits non-zero")
	}
}

// TestResolveTargetParseError covers the parse-response failure branch of
// ResolveTarget: the create command succeeds but returns non-JSON output.
func TestResolveTargetParseError(t *testing.T) {
	ui := &termio.Mock{}
	provider := opencodeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd:                  msb.NewTestResult(true, 0, "not-json", "", nil),
		},
	}
	_, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err == nil {
		t.Fatal("expected error when parsing the create response fails")
	}
}
