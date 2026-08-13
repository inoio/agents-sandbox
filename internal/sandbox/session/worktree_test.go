package session

import (
	"context"
	"strings"
	"testing"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

func TestResolveTargetNoWorktreeReturnsWorkspace(t *testing.T) {
	got := resolveTargetNoBranch()
	if got != "/workspace" {
		t.Errorf("expected /workspace, got %q", got)
	}
}

func TestResolveTargetEmptySpecReturnsWorkspace(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{ShellCalls: &[]string{}}
	dir, err := ResolveTarget(context.Background(), sb, options.WorktreeSpec{}, ui)
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

func TestResolveTargetReusesExistingWorktree(t *testing.T) {
	ui := &termio.Mock{}
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			buildWorktreeListCmd(): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(context.Background(), sb, options.WorktreeSpec{Name: "bugfix-exit-zero"}, ui)
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
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			buildWorktreeListCmd(): msb.NewTestResult(true, 0,
				`["/home/dev/.local/share/opencode/worktree/abc/foo"]`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	_, err := ResolveTarget(context.Background(), sb, options.WorktreeSpec{Name: "foo", Base: "main"}, ui)
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
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			buildWorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			buildWorktreeCreateCmd(options.WorktreeSpec{Name: "feat-x"}): msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
		},
		ShellCalls: &[]string{},
	}
	dir, err := ResolveTarget(context.Background(), sb, options.WorktreeSpec{Name: "feat-x"}, ui)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "/workspace/worktrees/feat-x"; dir != want {
		t.Errorf("got dir %q, want %q", dir, want)
	}
}

func TestResolveTargetCreatesWithBaseValidatesAndSendsStartCommand(t *testing.T) {
	ui := &termio.Mock{}
	createCmd := buildWorktreeCreateCmd(options.WorktreeSpec{Name: "feat-x", Base: "main"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			buildWorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd: msb.NewTestResult(true, 0,
				`{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
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
	dir, err := ResolveTarget(context.Background(), sb, options.WorktreeSpec{Name: "feat-x", Base: "main"}, ui)
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
	createCmd := buildWorktreeCreateCmd(options.WorktreeSpec{Name: "feat-x", Base: "nope"})
	sb := &msb.MockSandbox{
		ShellOut: map[string]msb.ShellResult{
			buildWorktreeListCmd(): msb.NewTestResult(true, 0, `[]`, "", nil),
			createCmd:              msb.NewTestResult(true, 0, `{"directory":"/workspace/worktrees/feat-x"}`, "", nil),
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
		sb,
		options.WorktreeSpec{Name: "feat-x", Base: "nope"},
		ui,
	); err == nil {
		t.Error("expected error for unresolvable base")
	}
}

func TestParseWorktreeResponse(t *testing.T) {
	resp := `{"directory": "/home/dev/.local/share/opencode/worktree/abc123/feat-x"}`
	got, err := parseWorktreeResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/home/dev/.local/share/opencode/worktree/abc123/feat-x"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestParseWorktreeResponseInvalidJSON(t *testing.T) {
	_, err := parseWorktreeResponse("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseWorktreeResponseMissingDirectory(t *testing.T) {
	_, err := parseWorktreeResponse(`{"name": "feat-x"}`)
	if err == nil {
		t.Error("expected error when directory field is missing")
	}
}

func TestParseWorktreeResponseEmptyDirectory(t *testing.T) {
	_, err := parseWorktreeResponse(`{"directory": ""}`)
	if err == nil {
		t.Error("expected error when directory field is empty")
	}
}

func TestBuildWorktreeCreateBodyNameOnly(t *testing.T) {
	got := buildWorktreeCreateBody(options.WorktreeSpec{Name: "feat-x"})
	if !strings.Contains(got, `"name":"feat-x"`) {
		t.Errorf("expected name field, got %q", got)
	}
	if strings.Contains(got, "startCommand") {
		t.Errorf("expected no startCommand without a base, got %q", got)
	}
}

func TestBuildWorktreeCreateBodyWithBase(t *testing.T) {
	got := buildWorktreeCreateBody(options.WorktreeSpec{Name: "feat-x", Base: "main"})
	if !strings.Contains(got, `"name":"feat-x"`) {
		t.Errorf("expected name field, got %q", got)
	}
	if !strings.Contains(got, `"startCommand":"git reset --hard main"`) {
		t.Errorf("expected startCommand reset, got %q", got)
	}
}

func TestBuildWorktreeCreateCmd(t *testing.T) {
	cmd := buildWorktreeCreateCmd(options.WorktreeSpec{Name: "feat-x"})
	if !strings.Contains(cmd, "POST") {
		t.Errorf("expected POST in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "/experimental/worktree") {
		t.Errorf("expected API path in command, got %q", cmd)
	}
	if !strings.Contains(cmd, `'{"name":"feat-x"}'`) {
		t.Errorf("expected create body in command, got %q", cmd)
	}
}

func TestBuildWorktreeListCmd(t *testing.T) {
	cmd := buildWorktreeListCmd()
	// GET is the default HTTP method for curl, so no -X flag needed
	if !strings.Contains(cmd, "curl -sf ") {
		t.Errorf("expected curl -sf in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "/experimental/worktree") {
		t.Errorf("expected API path in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "127.0.0.1:4096") {
		t.Errorf("expected daemon address in command, got %q", cmd)
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
