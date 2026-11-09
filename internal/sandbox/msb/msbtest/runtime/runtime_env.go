//deadcode:ignore reason="test support: strip the reuse-home variable from a child's environment, called only from the msb-tagged tests"
package runtime

import "strings"

// reuseRuntimeHomeEnv, when set, points the msb-tagged tests at an existing
// microsandbox home they keep between runs instead of a fresh temp home. A kept
// home skips reinstalling the SDK-pinned runtime and reloading cached images.
// Local iteration only: unset (the default) creates and removes a fresh home.
const reuseRuntimeHomeEnv = "AGENTS_SANDBOX_MSB_TEST_HOME"

// WithoutReuseHome returns env with the reuse-home variable removed, so a child
// process a test spawns borrows its parent's microsandbox home (through the
// child markers and the inherited MSB_HOME) instead of re-entering the parent's
// reuse lock in its own TestMain. A reused home is held under an exclusive lock
// for the whole run, so a child that ran InstallIsolatedRuntimeHome would block
// forever on that lock.
func WithoutReuseHome(env []string) []string {
	prefix := reuseRuntimeHomeEnv + "="
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
