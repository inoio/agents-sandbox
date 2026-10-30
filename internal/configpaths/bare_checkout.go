package configpaths

import (
	"os"
	"path/filepath"
	"sync"
)

// BareCheckoutLocator reports whether cwd lies in a bare checkout whose worktrees
// live inside the bare root, returning the bare root and the root of the
// worktree cwd is in (the bare root itself when cwd is the bare root).
// git.BareCheckoutRoots is the production implementation.
type BareCheckoutLocator func(cwd string) (bareRoot, worktreeRoot string, inside bool)

// bareCheckoutLookup is a cached result of the installed locator for one cwd.
type bareCheckoutLookup struct {
	bareRoot     string
	worktreeRoot string
	inside       bool
}

var (
	bareCheckoutMutex   sync.Mutex
	bareCheckoutLocator BareCheckoutLocator = notInBareCheckout
	bareCheckoutCache                       = map[string]bareCheckoutLookup{}
)

func notInBareCheckout(string) (string, string, bool) { return "", "", false }

// UseBareCheckoutLocator installs the locator the real resolver asks where a
// bare checkout's project config lives. The composition root installs
// git.BareCheckoutRoots; without a locator every directory resolves to the
// relative project base. Results are cached per cwd, since the layout of a
// checkout does not change while the launcher runs.
func UseBareCheckoutLocator(locator BareCheckoutLocator) {
	bareCheckoutMutex.Lock()
	defer bareCheckoutMutex.Unlock()
	bareCheckoutLocator = locator
	bareCheckoutCache = map[string]bareCheckoutLookup{}
}

func lookupBareCheckout(cwd string) bareCheckoutLookup {
	bareCheckoutMutex.Lock()
	defer bareCheckoutMutex.Unlock()
	if lookup, cached := bareCheckoutCache[cwd]; cached {
		return lookup
	}
	bareRoot, worktreeRoot, inside := bareCheckoutLocator(cwd)
	lookup := bareCheckoutLookup{bareRoot: bareRoot, worktreeRoot: worktreeRoot, inside: inside}
	bareCheckoutCache[cwd] = lookup
	return lookup
}

// resolveProjectConfig returns the directory project config is read from and,
// when a bare checkout ignores a worktree-level .agents-sandbox, its path. In a
// bare checkout the base is the absolute bare-root .agents-sandbox; the worktree
// is inside the bare root, so a copy at the worktree root is ignored — from any
// directory within the worktree, not just its root. Everywhere else the base is
// the relative constant, resolved against the CWD by the callers.
func resolveProjectConfig(cwd string) (string, string) {
	lookup := lookupBareCheckout(cwd)
	if !lookup.inside {
		return projectConfigDir, ""
	}
	base := filepath.Join(lookup.bareRoot, projectConfigDir)
	ignored := filepath.Join(lookup.worktreeRoot, projectConfigDir)
	if ignored == base {
		// The CWD is the bare root itself: this is the directory the base reads,
		// not a worktree copy that gets ignored.
		return base, ""
	}
	if info, statErr := os.Stat(ignored); statErr == nil && info.IsDir() {
		return base, ignored
	}
	return base, ""
}
