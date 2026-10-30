package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// branchWorktreeAgent is a fake whose daemon keeps a branch on a new worktree:
// it implements DetachedWorktreeReporter but reports false, so the detached-HEAD
// note must stay empty.
type branchWorktreeAgent struct{ fakeAgent }

func (branchWorktreeAgent) WorktreeCreatesDetachedHead() bool { return false }

// createErrorSandbox fails the worktree create shell call while letting the
// resolve and list calls through, so ResolveTarget's create-error wrap can be
// exercised independently of the other shell calls.
type createErrorSandbox struct {
	*msb.MockSandbox

	err error
}

func (s createErrorSandbox) Shell(
	ctx context.Context, command string, opts ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	if command == "create" {
		return nil, s.err
	}
	return s.MockSandbox.Shell(ctx, command, opts...)
}

// TestWorktreeDetachedNoteEmptyForBranchReporter pins that a reporter whose
// daemon keeps a branch is silent: the note is only for a detached HEAD.
func TestWorktreeDetachedNoteEmptyForBranchReporter(t *testing.T) {
	if note := worktreeDetachedNote(branchWorktreeAgent{}); note != "" {
		t.Errorf("worktreeDetachedNote = %q, want empty for a branch-keeping reporter", note)
	}
}

// TestResolveTargetWrapsResolveShellError pins the resolve-error wrap: a shell
// failure resolving the daemon project preserves its cause.
func TestResolveTargetWrapsResolveShellError(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sentinel := errors.New("resolve shell boom")
	sb := &msb.MockSandbox{ShellErr: sentinel, ShellCalls: &[]string{}}

	_, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a resolve shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the resolve cause", err)
	}
}

// TestResolveTargetWrapsListShellError pins the list-error wrap: a v1 agent has
// no resolve command, so its first shell call is the list, whose failure must
// preserve its cause.
func TestResolveTargetWrapsListShellError(t *testing.T) {
	ui := &termio.Mock{}
	sentinel := errors.New("list shell boom")
	sb := &msb.MockSandbox{ShellErr: sentinel, ShellCalls: &[]string{}}

	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a list shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the list cause", err)
	}
}

// TestResolveTargetAbortsWhenListExitNonZero pins that a non-zero list exit
// aborts before create rather than reading as "no worktrees".
func TestResolveTargetAbortsWhenListExitNonZero(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
			"list":    msb.NewTestResult(false, 3, "", "boom", nil),
		},
		ShellCalls: &[]string{},
	}

	_, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a non-zero list exit to abort")
	}
	if !strings.Contains(err.Error(), `list worktrees for "feat-x" failed (exit 3)`) {
		t.Errorf("error = %v, want the list-failure detail", err)
	}
	if slices.Contains(*sb.ShellCalls, "create") {
		t.Errorf("must not create after a failed list; calls: %v", *sb.ShellCalls)
	}
}

// TestResolveTargetWrapsCreateShellError pins the create-error wrap: a shell
// failure creating the worktree preserves its cause.
func TestResolveTargetWrapsCreateShellError(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sentinel := errors.New("create shell boom")
	sb := createErrorSandbox{
		MockSandbox: &msb.MockSandbox{ShellOut: map[string]msb.ShellResult{
			"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
			"list":    msb.NewTestResult(true, 0, `[]`, "", nil),
		}},
		err: sentinel,
	}

	_, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a create shell failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the create cause", err)
	}
}

// TestResolveTargetAbortsWhenCreateExitNonZero pins the create-failure detail: a
// non-zero create exit names the exit and the timed-out-request caveat.
func TestResolveTargetAbortsWhenCreateExitNonZero(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
			"list":    msb.NewTestResult(true, 0, `[]`, "", nil),
			"create":  msb.NewTestResult(false, 4, "", "boom", nil),
		},
	}

	_, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a non-zero create exit to abort")
	}
	for _, want := range []string{`create worktree "feat-x" failed (exit 4)`, "may still have created it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

// TestResolveTargetWrapsBaseValidationError pins the base-validation wrap: an
// exec failure checking the base preserves its cause through both layers.
func TestResolveTargetWrapsBaseValidationError(t *testing.T) {
	ui := &termio.Mock{}
	a := &recordingWorktreeAgent{}
	sentinel := errors.New("exec boom")
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
			"list":    msb.NewTestResult(true, 0, `[]`, "", nil),
		},
		ExecErr: sentinel,
	}

	_, err := ResolveTarget(
		context.Background(),
		a,
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "develop"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err == nil {
		t.Fatal("expected a base-validation exec failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the exec cause", err)
	}
}

// assertVerboseContains fails unless some verbose line contains want.
func assertVerboseContains(t *testing.T, calls []string, want string) {
	t.Helper()
	if !slices.ContainsFunc(calls, func(call string) bool { return strings.Contains(call, want) }) {
		t.Errorf("expected a verbose %q note; got %v", want, calls)
	}
}

var _ agent.Agent = branchWorktreeAgent{}
