package vm

import (
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"

	"gopkg.in/yaml.v3"
)

// upgradeCheckInterval is how often the opencode updater may hit the GitHub
// releases endpoint: at most once per day.
const upgradeCheckInterval = 24 * time.Hour

// upgradeStateFile is the machine-global updater state file under the tool's
// user-state directory.
const upgradeStateFile = "updater.yaml"

// now is a test seam for the current time.
//
//nolint:gochecknoglobals // test seam
var now = time.Now

// upgradeState is the persisted record gating the opencode updater. It is
// global (not per project): once checked, the version is checked machine-wide.
type upgradeState struct {
	LastChecked     time.Time `yaml:"last_checked"`
	OfferedVersions []string  `yaml:"offered_versions"`
	// CurrentVersion is the agent version currently baked into the runner
	// image, detected on first boot. It is reused as the build arg on
	// subsequent runs so a normal run does not re-resolve "latest" from the
	// network.
	CurrentVersion string `yaml:"current_version"`
	// AgentSource records where the agent came from (tool | user), read from
	// the image's /etc/opencode-sandbox/agent-source on first boot.
	AgentSource string `yaml:"agent_source"`
	// DockerSource records where dockerd came from (tool | user), for future
	// docker-version checks.
	DockerSource string `yaml:"docker_source"`
}

// dueForCheck reports whether the last successful check is older than one day
// (or absent), i.e. a fresh GitHub check is due.
func (s upgradeState) dueForCheck(t time.Time) bool {
	return s.LastChecked.IsZero() || t.Sub(s.LastChecked) >= upgradeCheckInterval
}

// offered reports whether the given version has already had its rebuild prompt
// shown.
func (s upgradeState) offered(v string) bool {
	return slices.Contains(s.OfferedVersions, v)
}

// markOffered appends v to the set of versions whose prompt has been shown.
func (s *upgradeState) markOffered(v string) {
	s.OfferedVersions = append(s.OfferedVersions, v)
}

func upgradeStatePath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), upgradeStateFile)
}

// loadUpgradeState reads the updater state file. A missing or corrupt file
// yields an empty state without error so bookkeeping never blocks a session.
func loadUpgradeState() (upgradeState, error) {
	data, err := os.ReadFile(upgradeStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return upgradeState{}, nil
		}
		return upgradeState{}, err
	}
	var s upgradeState
	if err := yaml.Unmarshal(data, &s); err != nil {
		// A corrupt state file must not fail the session; fall back to a
		// fresh state so the updater can simply check again.
		//
		//nolint:nilerr // corrupt-file tolerance is deliberate
		return upgradeState{}, nil
	}
	return s, nil
}

// saveUpgradeState atomically writes the updater state file.
func saveUpgradeState(s upgradeState) error {
	path := upgradeStatePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+upgradeStateFile+".tmp")
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// currentUpgradeVersion returns the opencode version currently baked into the
// runner image, or "" when none has been recorded yet. A missing or corrupt
// state file yields "" so the caller falls back to resolving latest.
func currentUpgradeVersion() string {
	state, err := loadUpgradeState()
	if err != nil {
		return ""
	}
	return state.CurrentVersion
}

// currentAgentSource returns the agent-source recorded from the image's
// provenance files, or "" when none has been recorded yet.
func currentAgentSource() string {
	state, err := loadUpgradeState()
	if err != nil {
		return ""
	}
	return state.AgentSource
}
