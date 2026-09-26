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

// runningUnderHomebrew is the production entry point, wiring the real process
// and filesystem lookups into the testable underCellar decision.
func runningUnderHomebrew() bool {
	return underCellar(os.Executable, filepath.EvalSymlinks)
}

// underCellar reports whether the running executable is a Homebrew keg binary.
// It checks both the path the process was invoked through and its symlink-resolved
// target, because Homebrew launches the binary via the symlink in its bin
// directory while the real file sits inside the Cellar keg. getExecutable and
// resolve are injected so the path-lookup and resolution-failure branches are
// unit-testable; any failure to read the executable path means "not Homebrew".
func underCellar(getExecutable func() (string, error), resolve func(string) (string, error)) bool {
	exe, err := getExecutable()
	if err != nil {
		return false
	}
	if pathUnderHomebrewCellar(exe) {
		return true
	}
	resolved, err := resolve(exe)
	if err != nil {
		return false
	}
	return pathUnderHomebrewCellar(resolved)
}

// pathUnderHomebrewCellar reports whether path traverses a "Cellar" directory
// (a Homebrew keg location). It matches whole path segments so a binary merely
// named like the segment (for example "Cellarfoo") is not mistaken for one.
func pathUnderHomebrewCellar(path string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), homebrewCellarSegment)
}
