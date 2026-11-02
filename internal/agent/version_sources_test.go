package agent

import (
	"slices"
	"testing"
)

// versionSourceGaps names the version sources a lacks. specs/agent-version-sync.md
// makes the runner image's agent-version label the version authority and
// requires every tool-managed profile to also report its installed version in
// the VM, so the HomeState.agent_version baseline can never silently go stale:
//   - an UpgradeChecker resolves the release an unpinned build bakes,
//   - ImageSpec.VersionArg carries it into the build, which stamps the label,
//   - a VersionProvider detects the installed version at boot.
func versionSourceGaps(a Agent) []string {
	var gaps []string
	if _, ok := AsUpgradeChecker(a); !ok {
		gaps = append(gaps, "UpgradeChecker")
	}
	if a.ImageSpec().VersionArg == "" {
		gaps = append(gaps, "ImageSpec.VersionArg")
	}
	if _, ok := AsVersionProvider(a); !ok {
		gaps = append(gaps, "VersionProvider")
	}
	return gaps
}

// TestRegisteredAgentsHaveVersionSources guards the version authority for every
// registered profile, so a new profile cannot skip it.
func TestRegisteredAgentsHaveVersionSources(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			a, _ := Lookup(name)
			if gaps := versionSourceGaps(a); len(gaps) > 0 {
				t.Errorf("agent %q lacks version sources %v (see specs/agent-version-sync.md)", name, gaps)
			}
		})
	}
}

// TestVersionSourceGapsReportsAgentWithoutSources pins that the guard fails for
// a profile registered without any version source.
func TestVersionSourceGapsReportsAgentWithoutSources(t *testing.T) {
	want := []string{"UpgradeChecker", "ImageSpec.VersionArg", "VersionProvider"}
	if gaps := versionSourceGaps(stubAgent{name: "no-version-sources"}); !slices.Equal(gaps, want) {
		t.Errorf("versionSourceGaps = %v, want %v", gaps, want)
	}
}
