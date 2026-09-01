package vm

import (
	"context"
	"strings"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

func TestResolveTargetNoWorktreeReturnsWorkspace(t *testing.T) {
	got := resolveTargetNoBranch()
	if got != "/workspace" {
		t.Errorf("expected /workspace, got %q", got)
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

func TestResolveTargetEmptySpecReturnsWorkspace(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{}, ui)
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
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(context.Background(), &fakeAgent{}, sb, options.WorktreeSpec{Name: "feat-x"}, ui)
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
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "bugfix-exit-zero"},
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
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/foo"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	_, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "foo", Base: "main"},
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

func TestResolveTargetCreatesWithoutBase(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(context.Background(), opencodeAgent(t), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestResolveTargetCreatesWithBaseValidatesAndSendsStartCommand(t *testing.T) {
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "main"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace/worktrees/feat-x rev-parse --verify main^{commit}": msb.NewTestResult(
				true,
				0,
				"abc123",
				"",
				nil,
			),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(
		context.Background(),
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "main"},
		ui,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
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
	ui := &termio.Mock{}
	provider := mustWorktreeProvider(t)
	createCmd := provider.WorktreeCreateCmd(agent.WorktreeSpec{Name: "feat-x", Base: "nope"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			provider.WorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(
				true,
				0,
				`{"directory":"/workspace/worktrees/feat-x"}`,
				"",
				nil,
			),
		},
		ExecOut: map[string]msb.ShellResult{
			"git -C /workspace/worktrees/feat-x rev-parse --verify nope^{commit}": msb.NewTestResult(
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
		opencodeAgent(t),
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "nope"},
		ui,
	); err == nil {
		t.Error("expected error for unresolvable base")
	}
}

func TestSlugifyMatchesDaemonNaming(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"bugfix/exit-zero", "bugfix-exit-zero"},
		{"bugfix/reuse-branch", "bugfix-reuse-branch"},
		{"Feature/My-Topic", "feature-my-topic"},
		{"  spaces  ", "spaces"},
		{"/leading/dash", "leading-dash"},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFindWorktreeDirStringList(t *testing.T) {
	list := `[
		"/home/dev/.local/share/opencode/worktree/abc/review-test",
		"/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"
	]`
	dir, ok := findWorktreeDir(list, "bugfix-exit-zero")
	if !ok {
		t.Fatal("expected to find existing worktree for slug bugfix-exit-zero")
	}
	if want := "/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestFindWorktreeDirObjectList(t *testing.T) {
	list := `[
		{"name":"bugfix-exit-zero","branch":"opencode/bugfix-exit-zero","directory":"/root/bugfix-exit-zero"}
	]`
	dir, ok := findWorktreeDir(list, "bugfix-exit-zero")
	if !ok {
		t.Fatal("expected to find worktree from object list")
	}
	if want := "/root/bugfix-exit-zero"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestFindWorktreeDirNoMatch(t *testing.T) {
	list := `["/home/dev/.local/share/opencode/worktree/abc/other-topic"]`
	if _, ok := findWorktreeDir(list, "bugfix-exit-zero"); ok {
		t.Error("expected no match for a slug absent from the list")
	}
}

func TestFindWorktreeDirEmptyList(t *testing.T) {
	if _, ok := findWorktreeDir("", "bugfix-exit-zero"); ok {
		t.Error("expected no match for an empty list response")
	}
}

func TestResolveWorktreeSpecNameOnly(t *testing.T) {
	got, err := ResolveWorktreeSpec("bugfix-hello-world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bugfix-hello-world" || got.Base != "" {
		t.Errorf("unexpected spec: %+v", got)
	}
}

func TestResolveWorktreeSpecNameAndBase(t *testing.T) {
	got, err := ResolveWorktreeSpec("bugfix-hello-world:main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bugfix-hello-world" || got.Base != "main" {
		t.Errorf("unexpected spec: %+v", got)
	}
}

func TestResolveWorktreeSpecEmpty(t *testing.T) {
	got, err := ResolveWorktreeSpec("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "" || got.Base != "" {
		t.Errorf("expected zero spec, got %+v", got)
	}
}

func TestValidateWorktreeBaseOK(t *testing.T) {
	sb := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		"git -C /w/feat rev-parse --verify main^{commit}": msb.NewTestResult(true, 0, "abc123", "", nil),
	}}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "main"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorktreeBaseMissing(t *testing.T) {
	sb := &msb.MockSandbox{ExecOut: map[string]msb.ShellResult{
		"git -C /w/feat rev-parse --verify nope^{commit}": msb.NewTestResult(false, 128, "", "unknown revision", nil),
	}}
	if err := validateWorktreeBase(context.Background(), sb, "/w/feat", "nope"); err == nil {
		t.Error("expected error for unresolvable base")
	}
}

func TestResolveWorktreeSpecRejectsNonSlugName(t *testing.T) {
	for _, in := range []string{"feature/foo", "Feature bar", "a--b", "-lead", "trail-", ":main"} {
		if _, err := ResolveWorktreeSpec(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}
