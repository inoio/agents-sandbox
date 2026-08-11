package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

const defaultTargetDir = "/workspace"

type worktreeResponse struct {
	Directory string `json:"directory"`
}

// options.WorktreeSpec moved to internal/sandbox/options.

// ResolveWorktreeSpec parses a --worktree value of the form <name>[:<base>]
// and validates that the name is already a slug (slugify(name) == name).
func ResolveWorktreeSpec(value string) (options.WorktreeSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return options.WorktreeSpec{}, nil
	}
	name, base, _ := strings.Cut(value, ":")
	if name == "" || slugify(name) != name {
		return options.WorktreeSpec{}, fmt.Errorf(
			"worktree name %q is not a valid slug (use lowercase letters, digits, and single hyphens)",
			name,
		)
	}
	return options.WorktreeSpec{Name: name, Base: base}, nil
}

// slugify mirrors the opencode daemon's worktree name normalisation so we can
// match an existing worktree directory back to the requested --worktree. See the
// daemon's slugify: lowercase, collapse non-alphanumerics to "-", trim dashes.
var slugifyPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(slugifyPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
}

// resolveTargetNoBranch returns the default worktree directory when no branch is requested.
func resolveTargetNoBranch() string {
	return defaultTargetDir
}

// validateWorktreeBase confirms the base ref resolves among existing local refs
// inside the VM. It never fetches: the ref must already be present locally.
func validateWorktreeBase(ctx context.Context, sb msb.Sandbox, dir, base string) error {
	out, err := sb.Exec(ctx, "git", []string{"-C", dir, "rev-parse", "--verify", base + "^{commit}"})
	if err != nil {
		return fmt.Errorf("check base %q: %w", base, err)
	}
	if !out.Success() {
		return fmt.Errorf("base %q does not resolve locally (no fetch is performed)", base)
	}
	return nil
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

func buildWorktreeCreateBody(spec options.WorktreeSpec) string {
	if spec.Base == "" {
		return fmt.Sprintf(`{"name":%q}`, spec.Name)
	}
	return fmt.Sprintf(`{"name":%q,"startCommand":"git reset --hard %s"}`, spec.Name, spec.Base)
}

func buildWorktreeCreateCmd(spec options.WorktreeSpec) string {
	return fmt.Sprintf(
		`curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '%s'`,
		buildWorktreeCreateBody(spec),
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

// ResolveTarget returns the --dir target for opencode attach. An empty spec →
// /workspace. With a name → reuse an existing opencode worktree via the
// daemon's HTTP API when one already exists for that name, otherwise create
// a new one and return its directory path. When a base is present on an
// existing (reused) worktree the base is silently ignored with a warning.
// When a base is present on a fresh create the base is validated
// (no fetch) and the create body carries a `git reset --hard <base>`
// startCommand via the buildWorktreeCreateBody helper.
func ResolveTarget(
	ctx context.Context,
	sb msb.Sandbox,
	spec options.WorktreeSpec,
	ui termio.UI,
) (string, error) {
	if spec.Name == "" {
		return resolveTargetNoBranch(), nil
	}

	ui.Verbosef("checking for an existing worktree %q", spec.Name)
	listOut, err := sb.Shell(ctx, buildWorktreeListCmd())
	if err != nil {
		return "", fmt.Errorf("list worktrees for %q: %w", spec.Name, err)
	}
	if listOut.Success() {
		if dir, ok := findWorktreeDir(listOut.Stdout(), spec.Name); ok {
			if spec.Base != "" {
				ui.Warnf("worktree %q already exists; ignoring base %q", spec.Name, spec.Base)
			}
			ui.Verbosef("reusing existing worktree %q: %s", spec.Name, dir)
			return dir, nil
		}
	}

	ui.Verbosef("creating worktree %q", spec.Name)
	out, err := sb.Shell(ctx, buildWorktreeCreateCmd(spec))
	if err != nil {
		return "", fmt.Errorf("create worktree %q: %w", spec.Name, err)
	}
	if !out.Success() {
		return "", fmt.Errorf("create worktree %q failed (exit %d): %s", spec.Name, out.ExitCode(), out.Stderr())
	}

	dir, err := parseWorktreeResponse(out.Stdout())
	if err != nil {
		return "", fmt.Errorf("parse worktree response for %q: %w", spec.Name, err)
	}

	if spec.Base != "" {
		if err := validateWorktreeBase(ctx, sb, dir, spec.Base); err != nil {
			return "", fmt.Errorf("worktree %q: %w", spec.Name, err)
		}
	}

	ui.Verbosef("worktree for %q: %s", spec.Name, dir)
	return dir, nil
}
