package sandbox

import (
	"strings"
	"testing"
)

func TestResolveTargetNoBranchReturnsWorkspace(t *testing.T) {
	got := resolveTargetNoBranch()
	if got != "/workspace" {
		t.Errorf("expected /workspace, got %q", got)
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

func TestBuildWorktreeCreateBody(t *testing.T) {
	got := buildWorktreeCreateBody("feat-x")
	if !strings.Contains(got, `"name"`) {
		t.Errorf("expected body to contain 'name', got %q", got)
	}
	if !strings.Contains(got, "feat-x") {
		t.Errorf("expected body to contain branch name, got %q", got)
	}
}

func TestBuildWorktreeCreateCmd(t *testing.T) {
	cmd := buildWorktreeCreateCmd("feat-x")
	if !strings.Contains(cmd, "POST") {
		t.Errorf("expected POST in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "/experimental/worktree") {
		t.Errorf("expected API path in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "127.0.0.1:4096") {
		t.Errorf("expected daemon address in command, got %q", cmd)
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
