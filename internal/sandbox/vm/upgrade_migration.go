package vm

import (
	"os"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	"gopkg.in/yaml.v3"
)

// legacyUpdaterStateFile is the retired machine-global updater state file.
const legacyUpdaterStateFile = "updater.yaml"

// legacyUpdaterState mirrors the retired updater.yaml shape. Only the
// last_checked cadence is migrated; CurrentVersion, AgentSource, DockerSource,
// and OfferedVersions cannot be attributed to a project and are dropped, so
// provenance re-detects the version and the offered set resets.
//
//exhaustruct:ignore
type legacyUpdaterState struct {
	// LastChecked is the pre-per-agent flat cadence field, folded into
	// agents["opencode"].
	LastChecked time.Time                   `yaml:"last_checked"`
	Agents      map[string]legacyAgentState `yaml:"agents"`
}

//exhaustruct:ignore
type legacyAgentState struct {
	LastChecked time.Time `yaml:"last_checked"`
}

func legacyUpdaterStatePath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), legacyUpdaterStateFile)
}

// migrateLegacyUpdaterState folds each agent's last_checked from the retired
// updater.yaml into the root state and deletes the file. It is non-interactive
// and preserves any cadence already present in the root state. Missing,
// empty, or corrupt files fold nothing but are still removed. A contended root
// lock (typically a copy campaign holding it) or a failed write leaves the file
// in place so the migration retries.
func migrateLegacyUpdaterState(ui termio.UI) {
	path := legacyUpdaterStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			ui.Warnf("reading legacy updater state %s: %v (continuing)", path, err)
		}
		return
	}

	legacy := parseLegacyUpdaterState(data)

	ok, err := state.WithRootState(false, func(tx *state.RootTxn) error {
		root := tx.State()
		for name, agent := range legacy.Agents {
			if agent.LastChecked.IsZero() {
				continue
			}
			if root.AgentCheck(name).LastChecked.IsZero() {
				root.SetAgentCheck(name, state.AgentCheckState{LastChecked: agent.LastChecked})
			}
		}
		return tx.Commit()
	})
	if err != nil {
		ui.Warnf("updating root state for legacy updater migration: %v (continuing)", err)
		return
	}
	if !ok {
		return
	}
	_ = os.Remove(path)
}

// parseLegacyUpdaterState tolerantly parses the retired updater.yaml: a corrupt
// document yields the zero state so the migration deletes it without folding.
func parseLegacyUpdaterState(data []byte) legacyUpdaterState {
	var legacy legacyUpdaterState
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return legacyUpdaterState{}
	}
	foldFlatFieldIntoOpencode(&legacy)
	return legacy
}

// foldFlatFieldIntoOpencode honors the pre-per-agent flat cadence by attributing
// it to opencode, the only agent that predates the per-agent redesign.
func foldFlatFieldIntoOpencode(legacy *legacyUpdaterState) {
	if legacy.LastChecked.IsZero() {
		return
	}
	if legacy.Agents == nil {
		legacy.Agents = map[string]legacyAgentState{}
	}
	opencode := legacy.Agents["opencode"]
	if opencode.LastChecked.IsZero() {
		opencode.LastChecked = legacy.LastChecked
		legacy.Agents["opencode"] = opencode
	}
}
