package sandbox

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

const defaultTargetDir = "/workspace"

type worktreeResponse struct {
	Directory string `json:"directory"`
}

func resolveTargetNoBranch() string {
	return defaultTargetDir
}

func parseWorktreeResponse(stdout string) (string, error) {
	var resp worktreeResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return "", fmt.Errorf("parse worktree response: %w", err)
	}
	if resp.Directory == "" {
		return "", fmt.Errorf("worktree response missing directory field: %s", stdout)
	}
	return resp.Directory, nil
}

func buildWorktreeCreateBody(name string) string {
	return fmt.Sprintf(`{"name":%q}`, name)
}

func buildWorktreeCreateCmd(name string) string {
	return fmt.Sprintf(
		`curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '%s'`,
		buildWorktreeCreateBody(name),
	)
}

func buildWorktreeListCmd() string {
	return "curl -sf http://127.0.0.1:4096/experimental/worktree"
}

// ResolveTarget returns the --dir target for opencode attach. No branch →
// /workspace. With a branch → create or reuse an opencode worktree via the
// daemon's HTTP API and return its directory path.
func ResolveTarget(
	ctx context.Context,
	sb Sandbox,
	branch string,
	ui termio.UI,
) (string, error) {
	if branch == "" {
		return resolveTargetNoBranch(), nil
	}

	// Try to create the worktree. The API is idempotent enough: if it already
	// exists, the response returns the existing directory.
	ui.Verbosef("creating/reusing worktree for branch %q", branch)
	out, err := sb.Shell(ctx, buildWorktreeCreateCmd(branch))
	if err != nil {
		return "", fmt.Errorf("create worktree %q: %w", branch, err)
	}
	if !out.Success() {
		return "", fmt.Errorf("create worktree %q failed (exit %d): %s", branch, out.ExitCode(), out.Stderr())
	}

	dir, err := parseWorktreeResponse(out.Stdout())
	if err != nil {
		return "", fmt.Errorf("parse worktree response for %q: %w", branch, err)
	}
	ui.Verbosef("worktree for %q: %s", branch, dir)
	return dir, nil
}
