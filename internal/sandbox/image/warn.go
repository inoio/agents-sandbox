package image

import (
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// WarnIgnoredProjectConfig warns when a bare checkout ignores a worktree-level
// .agents-sandbox: project config is read from the bare root, not the worktree.
// It is a no-op elsewhere and on a non-bare checkout. The path resolver is
// injected rather than read from a package global.
func WarnIgnoredProjectConfig(paths configpaths.ConfigPaths, ui termio.Logger) {
	if dir := paths.IgnoredWorktreeConfigDir(); dir != "" {
		ui.Warnf(
			"ignoring %s: in a bare checkout the project config is read from the bare root, not the worktree",
			dir,
		)
	}
}
