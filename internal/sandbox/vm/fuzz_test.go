package vm

import (
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// legacyMirror is an independent description of the retired updater.yaml shape.
// The fuzz target parses the same bytes into it to derive the expected result
// without going through parseLegacyUpdaterState.
type legacyMirror struct {
	LastChecked time.Time `yaml:"last_checked"`
	Agents      map[string]struct {
		LastChecked time.Time `yaml:"last_checked"`
	} `yaml:"agents"`
}

// FuzzParseLegacyUpdaterState drives arbitrary bytes through the retired
// updater.yaml parser. The file is hand-editable and an interrupted write can
// corrupt it, so a tolerant reader must never panic: a malformed document must
// yield the zero state, and a valid one must map exactly — the flat
// last_checked folded into the opencode entry only when that entry has no
// cadence of its own — rather than merely producing some nonzero time.
func FuzzParseLegacyUpdaterState(f *testing.F) {
	for _, seed := range []string{
		"",
		"last_checked: 2026-01-02T03:04:05Z\n",
		"agents:\n  opencode:\n    last_checked: 2026-01-02T03:04:05Z\n",
		"agents:\n  pi:\n    last_checked: 2026-01-02T03:04:05Z\n",
		"last_checked: 2020-01-01T00:00:00Z\nagents:\n  opencode:\n    last_checked: 2021-02-02T00:00:00Z\n",
		"last_checked: 2026-01-02T03:04:05Z\nagents:\n  opencode:\n",
		"last_checked: 0000-1-1\nagents:\n  opencode: null\n",
		"last_checked: 2026-01-02T03:04:05+02:00\n",
		"last_checked: not-a-time\n",
		"last_checked: [unterminated",
		"agents: []\n",
		"agents:\n  a: {last_checked: x}\n  a: {last_checked: y}\n",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		legacy := parseLegacyUpdaterState(data)

		var mirror legacyMirror
		if err := yaml.Unmarshal(data, &mirror); err != nil {
			if !legacy.LastChecked.IsZero() || len(legacy.Agents) != 0 {
				t.Fatalf(
					"parseLegacyUpdaterState(%q) = %+v, want the zero state for an unparsable document",
					data,
					legacy,
				)
			}
			return
		}

		if !legacy.LastChecked.Equal(mirror.LastChecked) {
			t.Fatalf(
				"parseLegacyUpdaterState(%q) LastChecked = %v, want %v",
				data,
				legacy.LastChecked,
				mirror.LastChecked,
			)
		}
		for name, agent := range mirror.Agents {
			if name == "opencode" {
				// The fold may legitimately rewrite opencode; the dedicated
				// check below accounts for it.
				continue
			}
			if got := legacy.Agents[name].LastChecked; !got.Equal(agent.LastChecked) {
				t.Fatalf("parseLegacyUpdaterState(%q) agents[%q] = %v, want %v", data, name, got, agent.LastChecked)
			}
		}
		wantOpenCode := mirror.Agents["opencode"].LastChecked
		if wantOpenCode.IsZero() {
			wantOpenCode = mirror.LastChecked
		}
		if got := legacy.Agents["opencode"].LastChecked; !got.Equal(wantOpenCode) {
			t.Fatalf("parseLegacyUpdaterState(%q) agents[opencode] = %v, want %v", data, got, wantOpenCode)
		}
		for name := range legacy.Agents {
			if name == "opencode" {
				continue
			}
			if _, ok := mirror.Agents[name]; !ok {
				t.Fatalf("parseLegacyUpdaterState(%q) invented agent %q", data, name)
			}
		}
	})
}
