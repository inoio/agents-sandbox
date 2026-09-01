package agent

import (
	"fmt"
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
