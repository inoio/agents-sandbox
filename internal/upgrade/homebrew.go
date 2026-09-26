package upgrade

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// homebrewCellarSegment is the directory a Homebrew formula keg lives under:
// "<prefix>/Cellar/<formula>/<version>/...". Homebrew installs the real binary
// into that keg and exposes it through a symlink in its bin directory, so a
// binary whose path traverses a "Cellar" segment is managed by Homebrew.
const homebrewCellarSegment = "Cellar"

// InstalledViaHomebrew reports whether the running binary was installed and is
// kept current by Homebrew. Self-upgrading such a binary would silently desync
// Homebrew's version bookkeeping, so callers defer updates to `brew upgrade`
// instead of replacing the keg binary.
//
//nolint:gochecknoglobals // test seam
var InstalledViaHomebrew = runningUnderHomebrew

// runningUnderHomebrew inspects both the path the process was invoked through
// and its symlink-resolved target, because Homebrew launches the binary via the
// symlink in its bin directory while the real file sits inside the Cellar keg.
func runningUnderHomebrew() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if pathUnderHomebrewCellar(exe) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return pathUnderHomebrewCellar(resolved)
	}
	return false
}

// pathUnderHomebrewCellar reports whether path traverses a "Cellar" directory
// (a Homebrew keg location). It matches whole path segments so a binary merely
// named like the segment (for example "Cellarfoo") is not mistaken for one.
func pathUnderHomebrewCellar(path string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), homebrewCellarSegment)
}
