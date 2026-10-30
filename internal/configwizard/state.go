package configwizard

import (
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configfile"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

const (
	stateVersion  = 1
	stateFileName = "config-wizard.yaml"
	stateFileMode = 0o600
)

type State struct {
	Version      int      `yaml:"version"`
	FirstRunDone bool     `yaml:"first_run_done,omitempty"`
	Asked        []string `yaml:"asked,omitempty"`
}

func statePath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), stateFileName)
}

func LoadState() State {
	var zero State
	data, err := os.ReadFile(statePath())
	if err != nil {
		return zero
	}
	var state State
	if yaml.Unmarshal(data, &state) != nil {
		return zero
	}
	return state
}

func SaveState(state State) error {
	state.Version = stateVersion
	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return configfile.WriteAtomic(statePath(), data, stateFileMode)
}

func (s State) WasAsked(key string) bool {
	return slices.Contains(s.Asked, key)
}

func (s *State) MarkAsked(keys ...string) {
	for _, key := range keys {
		if !s.WasAsked(key) {
			s.Asked = append(s.Asked, key)
		}
	}
}
