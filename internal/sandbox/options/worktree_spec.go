package options

import (
	"fmt"
	"regexp"
	"strings"
)

// ResolveWorktreeSpec parses a --worktree value of the form <name>[:<base>]
// and validates that the name is already a slug (slugify(name) == name).
func ResolveWorktreeSpec(value string) (WorktreeSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return WorktreeSpec{}, nil
	}
	name, base, _ := strings.Cut(value, ":")
	if name == "" || slugify(name) != name {
		return WorktreeSpec{}, fmt.Errorf(
			"worktree name %q is not a valid slug (use lowercase letters, digits, and single hyphens)",
			name,
		)
	}
	return WorktreeSpec{Name: name, Base: base}, nil
}

// slugify mirrors the opencode daemon's worktree name normalization so we can
// match an existing worktree directory back to the requested --worktree. See the
// daemon's slugify: lowercase, collapse non-alphanumerics to "-", trim dashes.
var slugifyPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(slugifyPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
}
