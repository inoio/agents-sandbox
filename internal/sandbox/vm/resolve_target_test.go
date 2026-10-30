package vm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestResolveTargetListError covers the worktree-list failure branch of
// ResolveTarget: a shell error listing worktrees is propagated.
func TestResolveTargetListError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("list failed")}
	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected error when listing worktrees fails")
	}
}

// TestResolveTargetResolveError covers the project-resolve transport failure
// branch of ResolveTarget: a shell error resolving the project is propagated
// before any list call.
func TestResolveTargetResolveError(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellErr: errors.New("resolve failed"), ShellCalls: &[]string{}}
	_, err := ResolveTarget(
		context.Background(),
		opencode2Agent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected error when resolving the project fails")
	}
	if len(*sb.ShellCalls) != 1 {
		t.Errorf("resolve failure must abort before the list; calls: %v", *sb.ShellCalls)
	}
}

// TestResolveTargetListNonSuccessDoesNotCreate pins that a non-success worktree
// list fails the resolution instead of falling through to create. A transport
// failure or a non-array payload must never be read as "no worktrees": that
// would silently abandon an existing worktree for a new server-suffixed one and
// hide a future route or field rename. v1's list returns a successful JSON array
// (empty when the project has no worktrees), so a non-success result is always a
// genuine failure and the strictness is safe to apply to both providers.
func TestResolveTargetListNonSuccessDoesNotCreate(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{})
	calls := &[]string{}
	sb := &msb.MockSandbox{
		ShellCalls: calls,
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(
				false, 22, "", "curl: (22) The requested URL returned error: 404", nil),
			createCmd: msb.NewTestResult(true, 0, `{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err == nil {
		t.Fatal("expected error when the worktree list exits non-zero")
	}
	for _, call := range *calls {
		if strings.Contains(call, "POST") {
			t.Errorf("must not create a worktree after a failed list; calls: %v", *calls)
		}
	}
}

// TestResolveTargetCreateNonSuccess covers the non-success create-result branch
// of ResolveTarget.
func TestResolveTargetCreateNonSuccess(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(false, 1, "", "failed", nil),
		},
	}
	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected error when the create command exits non-zero")
	}
}

// TestResolveTargetParseError covers the parse-response failure branch of
// ResolveTarget: the create command succeeds but returns non-JSON output.
func TestResolveTargetParseError(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(true, 0, "not-json", "", nil),
		},
	}
	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected error when parsing the create response fails")
	}
}
