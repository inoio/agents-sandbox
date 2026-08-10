package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

const defaultTargetDir = "/workspace"

type worktreeResponse struct {
	Directory string `json:"directory"`
}

// slugify mirrors the opencode daemon's worktree name normalisation so we can
// match an existing worktree directory back to the requested --branch. See the
// daemon's slugify: lowercase, collapse non-alphanumerics to "-", trim dashes.
var slugifyPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(slugifyPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
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

// findWorktreeDir scans the daemon's worktree list response for a directory
// whose base name matches the given slug. The list may be an array of
// directory path strings (current daemon) or an array of objects each carrying
// a directory field (newer daemon). ok is false when no match is found.
func findWorktreeDir(listStdout string, slug string) (string, bool) {
	if strings.TrimSpace(listStdout) == "" {
		return "", false
	}
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(listStdout), &entries); err != nil {
		return "", false
	}
	for _, entry := range entries {
		var directory string
		switch {
		case len(entry) > 0 && entry[0] == '"':
			var path string
			if json.Unmarshal(entry, &path) != nil {
				continue
			}
			directory = path
		default:
			var info worktreeResponse
			if json.Unmarshal(entry, &info) != nil {
				continue
			}
			directory = info.Directory
		}
		if directory != "" && filepath.Base(directory) == slug {
			return directory, true
		}
	}
	return "", false
}

// ResolveTarget returns the --dir target for opencode attach. No branch →
// /workspace. With a branch → reuse an opencode worktree via the daemon's HTTP
// API when one already exists for the branch, otherwise create a new one, and
// return its directory path.
func ResolveTarget(
	ctx context.Context,
	sb Sandbox,
	branch string,
	ui termio.UI,
) (string, error) {
	if branch == "" {
		return resolveTargetNoBranch(), nil
	}

	slug := slugify(branch)

	// Reuse an existing worktree for this branch, if any. The create endpoint
	// is NOT idempotent by name: it always mints a fresh worktree (appending a
	// random suffix on collision), so re-issuing create would leak new
	// worktrees on every run.
	ui.Verbosef("checking for an existing worktree for branch %q", branch)
	listOut, err := sb.Shell(ctx, buildWorktreeListCmd())
	if err != nil {
		return "", fmt.Errorf("list worktrees for %q: %w", branch, err)
	}
	if listOut.Success() {
		if dir, ok := findWorktreeDir(listOut.Stdout(), slug); ok {
			ui.Verbosef("reusing existing worktree for %q: %s", branch, dir)
			return dir, nil
		}
	}

	ui.Verbosef("creating worktree for branch %q", branch)
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
