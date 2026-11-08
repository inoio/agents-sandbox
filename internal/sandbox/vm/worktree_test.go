package vm

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestValidateWorktreeBase pins that a base must resolve to a commit in the
// local checkout (no fetch is performed), and that a check that cannot run
// keeps its cause.
func TestValidateWorktreeBase(t *testing.T) {
	const check = "git -C /w/feat rev-parse --verify %s^{commit}"
	execFailed := errors.New("exec failed")

	sb := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		fmt.Sprintf(check, "main"): msb.NewTestResult(true, 0, "abc123", "", nil),
		fmt.Sprintf(check, "nope"): msb.NewTestResult(false, 128, "", "unknown revision", nil),
	}}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "main"); err != nil {
		t.Errorf("resolvable base: %v", err)
	}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "nope"); err == nil ||
		!strings.Contains(err.Error(), `base "nope" does not resolve locally`) {
		t.Errorf("unresolvable base error = %v, want it refused", err)
	}
	err := validateWorktreeBase(context.Background(), &msb.MockSandbox{ExecErr: execFailed}, "/w/feat", "main")
	if !errors.Is(err, execFailed) {
		t.Errorf("failed check error = %v, want it to wrap %v", err, execFailed)
	}
}

// TestFindWorktreeDir pins that a worktree is found by its directory's base
// name, the first match winning, and that no match reports not found.
func TestFindWorktreeDir(t *testing.T) {
	const root = "/home/dev/.local/share/opencode/worktree/abc/"
	cases := []struct {
		name    string
		dirs    []string
		want    string
		wantHit bool
	}{
		{
			name:    "later entry matches",
			dirs:    []string{root + "review-test", root + "bugfix"},
			want:    root + "bugfix",
			wantHit: true,
		},
		{
			name:    "first match wins",
			dirs:    []string{root + "bugfix", "/other/bugfix"},
			want:    root + "bugfix",
			wantHit: true,
		},
		{name: "no match", dirs: []string{root + "other-topic"}},
		{name: "a name containing the slug is no match", dirs: []string{root + "bugfix-2"}},
		{name: "empty list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, ok := findWorktreeDir(tc.dirs, "bugfix")
			if dir != tc.want || ok != tc.wantHit {
				t.Errorf("findWorktreeDir = %q, %v; want %q, %v", dir, ok, tc.want, tc.wantHit)
			}
		})
	}
}

// TestWorktreeDetachedNoteEmptyForBranchReporter pins that a reporter whose
// daemon keeps a branch is silent: the note is only for a detached HEAD.
func TestWorktreeDetachedNoteEmptyForBranchReporter(t *testing.T) {
	if note := worktreeDetachedNote(agent.Resolve(branchWorktreeAgent{})); note != "" {
		t.Errorf("worktreeDetachedNote = %q, want empty for a branch-keeping reporter", note)
	}
}

// TestResolveTargetStopsAtTheFailingStep pins that each step of resolving a
// worktree (resolving the daemon project, listing its worktrees, validating the
// base, creating the worktree) fails the resolution with its cause and runs no
// later step. In particular a failed or unparsable list is never read as "no
// worktrees", which would abandon an existing worktree for a new
// server-suffixed one and hide a daemon route or field rename.
func TestResolveTargetStopsAtTheFailingStep(t *testing.T) {
	cause := errors.New("step failed")
	ok := func(stdout string) msb.ShellResult { return msb.NewTestResult(true, 0, stdout, "", nil) }
	cases := []struct {
		name     string
		agent    *recordingWorktreeAgent
		base     string
		outputs  map[string]msb.ShellResult
		failing  string // the command whose shell call fails with cause
		execErr  error
		wantText []string
		wantNot  []string // commands that must not run
	}{
		{
			name:     "resolve shell failure",
			failing:  "resolve",
			wantText: []string{`resolve project for "/workspace"`},
			wantNot:  []string{"list", "create"},
		},
		{
			name:     "resolve exits non-zero",
			outputs:  map[string]msb.ShellResult{"resolve": msb.NewTestResult(false, 1, "", "boom", nil)},
			wantText: []string{`resolve project for "/workspace" failed (exit 1): boom`},
			wantNot:  []string{"list", "create"},
		},
		{
			name:     "unparsable project",
			agent:    &recordingWorktreeAgent{badProject: true},
			wantText: []string{`resolve project for "/workspace" returned an unexpected payload`},
			wantNot:  []string{"list", "create"},
		},
		{
			name:     "list shell failure",
			failing:  "list",
			wantText: []string{`list worktrees for "feat-x"`},
			wantNot:  []string{"create"},
		},
		{
			name:     "list exits non-zero",
			outputs:  map[string]msb.ShellResult{"list": msb.NewTestResult(false, 3, "", "boom", nil)},
			wantText: []string{`list worktrees for "feat-x" failed (exit 3): boom`},
			wantNot:  []string{"create"},
		},
		{
			name:     "unparsable list",
			agent:    &recordingWorktreeAgent{badList: true},
			wantText: []string{`list worktrees for "feat-x" returned an unexpected payload`},
			wantNot:  []string{"create"},
		},
		{
			name:     "base cannot be checked",
			base:     "develop",
			execErr:  cause,
			wantText: []string{`worktree "feat-x"`},
			wantNot:  []string{"create"},
		},
		{
			name:     "create shell failure",
			failing:  "create",
			wantText: []string{`create worktree "feat-x"`},
		},
		{
			name:     "create exits non-zero",
			outputs:  map[string]msb.ShellResult{"create": msb.NewTestResult(false, 4, "", "boom", nil)},
			wantText: []string{`create worktree "feat-x" failed (exit 4): boom`, "may still have created it"},
		},
		{
			name:     "unparsable create response",
			agent:    &recordingWorktreeAgent{badDir: true},
			wantText: []string{`parse worktree response for "feat-x"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputs := map[string]msb.ShellResult{"resolve": ok(`{}`), "list": ok(`[]`), "create": ok(`{}`)}
			maps.Copy(outputs, tc.outputs)
			calls := &[]string{}
			mock := &msb.MockSandbox{ShellOut: outputs, ShellCalls: calls, ExecErr: tc.execErr}
			var sb msb.Sandbox = mock
			if tc.failing != "" {
				sb = failingCommandSandbox{MockSandbox: mock, command: tc.failing, err: cause}
			}
			a := tc.agent
			if a == nil {
				a = &recordingWorktreeAgent{}
			}

			_, err := ResolveTarget(context.Background(), agent.Resolve(a), sb,
				options.WorktreeSpec{Name: "feat-x", Base: tc.base}, mounts.WorkspaceMountPath, &termiotest.Mock{})

			if err == nil {
				t.Fatal("ResolveTarget succeeded, want the step's failure")
			}
			if (tc.failing != "" || tc.execErr != nil) && !errors.Is(err, cause) {
				t.Errorf("error = %v, want it to wrap %v", err, cause)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
			for _, command := range tc.wantNot {
				if slices.Contains(*calls, command) {
					t.Errorf("ran %q after the failing step; calls: %v", command, *calls)
				}
			}
		})
	}
}

func TestResolveTargetResolvesThenPassesProjectToProvider(t *testing.T) {
	ui := &termiotest.Mock{}
	a := &recordingWorktreeAgent{}
	sb := &msb.MockSandbox{ShellOut: map[string]msb.ShellResult{
		"resolve": msb.NewTestResult(true, 0, `{}`, "", nil),
		"list":    msb.NewTestResult(true, 0, `[]`, "", nil),
		"create":  msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
	}}
	dir, err := ResolveTarget(
		context.Background(),
		agent.Resolve(a),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/wt/feat-x" {
		t.Errorf("got dir %q, want /workspace/wt/feat-x", dir)
	}
	if a.resolveTarget != mounts.WorkspaceMountPath {
		t.Errorf("resolve target = %q, want %q", a.resolveTarget, mounts.WorkspaceMountPath)
	}
	if a.resolveCalls != 1 {
		t.Errorf("resolve called %d times, want exactly once (create must reuse the list's project)", a.resolveCalls)
	}
	want := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	if a.listProject != want || a.createProject != want {
		t.Errorf("projects = list %+v, create %+v; want %+v", a.listProject, a.createProject, want)
	}
	if a.createSpec.Name != "feat-x" {
		t.Errorf("create spec = %+v, want Name feat-x", a.createSpec)
	}
}

// TestResolveTargetV1SkipsResolveStep pins that an agent without a resolve
// command (v1) shells out only for the list and create.
func TestResolveTargetV1SkipsResolveStep(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(
				agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{},
			): msb.NewTestResult(true, 0, `{"directory":"/workspace/wt/feat-x"}`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*sb.ShellCalls) != 2 {
		t.Errorf("v1 should shell out exactly twice (list, create); calls: %v", *sb.ShellCalls)
	}
}

func TestResolveTargetEmptySpecReturnsWorkspace(t *testing.T) {
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace" {
		t.Errorf("expected /workspace, got %q", dir)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for empty spec, got %v", *sb.ShellCalls)
	}
}

func TestResolveTargetNoDaemonProvider(t *testing.T) {
	ui := &termiotest.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(
		context.Background(),
		agent.Resolve(&fakeAgent{}),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace" {
		t.Errorf("expected /workspace for an agent without a DaemonProvider, got %q", dir)
	}
	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

func TestResolveTargetReusesExistingWorktree(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "bugfix-exit-zero"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestResolveTargetReuseWarnsIgnoredBase(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/foo"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	_, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "foo", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "ignoring base") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning that base is ignored on reuse; got: %v", ui.WarnCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

// TestResolveTargetReuseIgnoresUnresolvableBase pins the ordering constraint:
// the base is validated only on the create path, never on reuse. The failing
// ExecOut entry is never consulted under the correct ordering, so the call
// succeeds; if validation were moved ahead of the reuse check it would fail.
func TestResolveTargetReuseIgnoresUnresolvableBase(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	existing := "/home/dev/.local/share/opencode/worktree/abc/foo"
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0,
				`["`+existing+`"]`, "", nil),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify main^{commit}": msb.NewTestResult(
				false,
				128,
				"",
				"unknown revision",
				nil,
			),
		},
		ShellCalls: &[]string{},
		ExecCalls:  &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "foo", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("reuse must ignore an unresolvable base, got error: %v", err)
	}
	if dir != existing {
		t.Errorf("got dir %q, want reused worktree %q", dir, existing)
	}
	if len(*sb.ExecCalls) != 0 {
		t.Errorf("reuse must not validate the base; exec calls: %v", *sb.ExecCalls)
	}
	found := false
	for _, w := range ui.WarnCalls {
		if strings.Contains(w, "ignoring base") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning that base is ignored on reuse; got: %v", ui.WarnCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestResolveTargetCreatesWithoutBase(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{}): msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestResolveTargetCreatesWithBaseValidatesAndSendsStartCommand(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "main"}, agent.WorktreeProject{})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify main^{commit}": msb.NewTestResult(
				true,
				0,
				"abc123",
				"",
				nil,
			),
		},
		ShellCalls: &[]string{},
		ExecCalls:  &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "main"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
	if !slices.Contains(*sb.ExecCalls, "git -C /workspace rev-parse --verify main^{commit}") {
		t.Errorf("expected base validated against the launch directory; exec calls: %v", *sb.ExecCalls)
	}
	found := false
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, `git reset --hard main`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected create body to carry startCommand reset; got: %v", *sb.ShellCalls)
	}
}

func TestResolveTargetCreateFailsOnUnresolvableBase(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "nope"}, agent.WorktreeProject{})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace rev-parse --verify nope^{commit}": msb.NewTestResult(
				false,
				128,
				"",
				"unknown revision",
				nil,
			),
		},
		ShellCalls: &[]string{},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "nope"},
		mounts.WorkspaceMountPath,
		ui,
	); err == nil {
		t.Error("expected error for unresolvable base")
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected no create request for an unresolvable base: %q", call)
		}
	}
}

// TestResolveTargetCreateReportsWorktree pins that a fresh create is announced
// at the default-visible level, naming the worktree and its directory.
func TestResolveTargetCreateReportsWorktree(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{}): msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "created worktree", "feat-x", "/workspace/worktrees/feat-x") {
		t.Errorf("expected a default-visible create message naming the directory; got: %v", ui.InfoCalls)
	}
	assertVerboseContains(t, ui.VerboseCalls, "checking for an existing worktree")
	assertVerboseContains(t, ui.VerboseCalls, "creating worktree")
}

// TestResolveTargetReuseReportsWorktree pins that reuse is announced at the
// default-visible level, naming the reused directory.
func TestResolveTargetReuseReportsWorktree(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	dir := "/home/dev/.local/share/opencode/worktree/abc/feat-x"
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0,
				`["`+dir+`"]`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "reusing existing worktree", "feat-x", dir) {
		t.Errorf("expected a default-visible reuse message naming the directory; got: %v", ui.InfoCalls)
	}
}

// TestResolveTargetOpencode2ReportsDetachedHead pins that opencode2's detached
// HEAD (v2 never leaves a worktree on a branch) is surfaced with the recovery.
func TestResolveTargetOpencode2ReportsDetachedHead(t *testing.T) {
	ui := &termiotest.Mock{}
	a := opencode2Agent(t)
	provider, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode2 does not implement WorktreeProvider")
	}
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeResolveCmd(mounts.WorkspaceMountPath): msb.NewTestResult(
				true, 0, `{"project":{"id":"abc","canonical":"/repo"}}`, "", nil),
			provider.WorktreeListCmd(project): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, project): msb.NewTestResult(true, 0,
				`{"directory":"/home/dev/.local/share/opencode/worktree/abc/feat-x"}`, "", nil),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		agent.Resolve(a),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !infoCallContains(ui.InfoCalls, "detached HEAD", "git switch -c <branch>") {
		t.Errorf("expected the create message to flag the detached HEAD and recovery; got: %v", ui.InfoCalls)
	}
}

// TestResolveTargetOpencode2ReuseDoesNotClaimDetachedHead pins that the
// detached-HEAD note stays on the create path. A reused worktree's checkout
// state is unknown — the user may already have run `git switch -c <branch>`
// after the create-time note — so reuse must not restate the claim.
func TestResolveTargetOpencode2ReuseDoesNotClaimDetachedHead(t *testing.T) {
	ui := &termiotest.Mock{}
	a := opencode2Agent(t)
	provider, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode2 does not implement WorktreeProvider")
	}
	dir := "/home/dev/.local/share/opencode/worktree/abc/feat-x"
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeResolveCmd(mounts.WorkspaceMountPath): msb.NewTestResult(
				true, 0, `{"project":{"id":"abc","canonical":"/repo"}}`, "", nil),
			provider.WorktreeListCmd(project): msb.NewTestResult(true, 0,
				`[{"directory":"`+dir+`"}]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	got, err := ResolveTarget(
		context.Background(),
		agent.Resolve(a),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got dir %q, want the reused %q", got, dir)
	}
	if !infoCallContains(ui.InfoCalls, "reusing existing worktree", "feat-x", dir) {
		t.Errorf("expected a default-visible reuse message naming the directory; got: %v", ui.InfoCalls)
	}
	for _, msg := range append(append([]string{}, ui.InfoCalls...), ui.WarnCalls...) {
		if strings.Contains(msg, "detached") {
			t.Errorf("reuse must not claim a detached HEAD: %q", msg)
		}
	}
}

// TestResolveTargetOpencodeDoesNotReportDetachedHead pins that v1 (which keeps a
// branch) never claims a detached HEAD.
func TestResolveTargetOpencodeDoesNotReportDetachedHead(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{}): msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
	}
	if _, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, msg := range append(append([]string{}, ui.InfoCalls...), ui.WarnCalls...) {
		if strings.Contains(msg, "detached") {
			t.Errorf("opencode v1 must not report a detached HEAD: %q", msg)
		}
	}
}

// TestResolveTargetWarnsOnRenamedWorktree pins that a daemon-returned directory
// whose base name differs from the requested name (the -2 auto-suffix) is
// surfaced, naming the directory actually used.
func TestResolveTargetWarnsOnRenamedWorktree(t *testing.T) {
	ui := &termiotest.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(agent.WorktreeProject{}): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}, agent.WorktreeProject{}): msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x-2"}`,
				"",
				nil,
			),
		},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeResolved(t),
		sb,
		options.WorktreeSpec{Name: "feat-x"},
		mounts.WorkspaceMountPath,
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/workspace/worktrees/feat-x-2" {
		t.Errorf("got dir %q, want the daemon-returned /workspace/worktrees/feat-x-2", dir)
	}
	if !infoCallContains(ui.WarnCalls, "feat-x", "/workspace/worktrees/feat-x-2") {
		t.Errorf("expected a warning naming the renamed directory; got: %v", ui.WarnCalls)
	}
}

// assertVerboseContains fails unless some verbose line contains want.
func assertVerboseContains(t *testing.T, calls []string, want string) {
	t.Helper()
	if !slices.ContainsFunc(calls, func(call string) bool { return strings.Contains(call, want) }) {
		t.Errorf("expected a verbose %q note; got %v", want, calls)
	}
}

// mustWorktreeProvider returns the opencode profile's WorktreeProvider so tests
// can build the exact worktree commands the production path shells out with.
func mustWorktreeProvider(t *testing.T) agent.WorktreeProvider {
	t.Helper()
	provider, ok := agent.AsWorktreeProvider(opencodeAgent(t))
	if !ok {
		t.Fatal("opencode agent does not implement WorktreeProvider")
	}
	return provider
}

// opencode2Agent returns the opencode2 profile for tests exercising v2's
// agent-specific worktree behavior.
func opencode2Agent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	return a
}

// infoCallContains reports whether any recorded info message contains every part.
func infoCallContains(calls []string, parts ...string) bool {
	for _, call := range calls {
		matched := true
		for _, part := range parts {
			if !strings.Contains(call, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
