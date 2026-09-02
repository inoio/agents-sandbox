package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// newerVersionThan reports whether a is a strictly newer semantic version than
// b, ignoring a leading "v" on either string. It is shared by every agent that
// implements an UpgradeChecker.
func newerVersionThan(a, b string) (bool, error) {
	av, err := semver.NewVersion(strings.TrimPrefix(a, "v"))
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", a, err)
	}
	bv, err := semver.NewVersion(strings.TrimPrefix(b, "v"))
	if err != nil {
		return false, fmt.Errorf("parse version %q: %w", b, err)
	}
	return av.GreaterThan(bv), nil
}

// versionOutputPattern matches the first semver-looking token in a version
// command's output, tolerating a leading "v" and surrounding text (e.g.
// "pi 0.3.0" or "@anthropic-ai/claude-code/2.3.4 linux-x64 ..."). It is an
// immutable package-level regex shared by every agent's version parser.
var versionOutputPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)`)

// extractSemverFromOutput returns the first version number found in stdout.
func extractSemverFromOutput(stdout string) (string, error) {
	m := versionOutputPattern.FindStringSubmatch(stdout)
	if m == nil {
		return "", fmt.Errorf("no version found in output %q", stdout)
	}
	return m[1], nil
}
