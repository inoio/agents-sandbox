package prepare

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func TestMigrateLegacyUpdaterStateFoldsLastCheckedAndDeletes(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must be deleted")
	}
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Fatal("last_checked not folded")
	}
}

func TestMigrateLegacyUpdaterStatePresentValueWins(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: time.Unix(5000, 0).UTC()},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	root, _ := state.ReadRootState()
	if root.AgentCheck("opencode").LastChecked.Unix() != 5000 {
		t.Fatal("present last_checked must win over the legacy value")
	}
}

func TestMigrateLegacyUpdaterStateCorruptIsDeleted(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("not: valid: yaml: ["),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); !os.IsNotExist(err) {
		t.Fatal("corrupt updater.yaml must be deleted")
	}
}

func TestMigrateLegacyUpdaterStateSkippedWhenLockHeld(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ok, err := state.WithRootState(true, func(*state.RootTxn) error {
		migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
		return nil
	})
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); err != nil {
		t.Fatal("updater.yaml must survive when the lock is held")
	}
}

func TestMigrateLegacyUpdaterStateFoldsFlatFieldIntoOpencode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("last_checked: 2021-06-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	root, _ := state.ReadRootState()
	if got := root.AgentCheck("opencode").LastChecked.UTC().Year(); got != 2021 {
		t.Fatalf("flat last_checked not folded into opencode: year=%d", got)
	}
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must be deleted")
	}
}

func TestMigrateLegacyUpdaterStateSkipsZeroLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    current_version: 1.2.3\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	root, _ := state.ReadRootState()
	if !root.AgentCheck("opencode").LastChecked.IsZero() {
		t.Fatal("a zero last_checked must not seed a check state")
	}
	// Folding nothing must not create the root document.
	if _, err := os.Stat(filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")); !os.IsNotExist(err) {
		t.Fatal("a fold-less migration must not write the root state")
	}
}

// TestMigrateLegacyUpdaterStateNoOpPreservesRootDocument ensures the fold-less
// no-op guard leaves an existing root document byte-for-byte untouched rather
// than rewriting it.
func TestMigrateLegacyUpdaterStateNoOpPreservesRootDocument(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedRootState(state.RootState{Updates: state.Updates{Agents: map[string]state.AgentCheckState{
		"opencode": {LastChecked: time.Unix(5000, 0).UTC()},
	}}}); err != nil {
		t.Fatal(err)
	}
	before, err := state.ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	after, err := state.ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*before, *after) {
		t.Fatalf("fold-less migration changed the root document:\n before %+v\n after  %+v", *before, *after)
	}
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); !os.IsNotExist(err) {
		t.Fatal("updater.yaml must still be deleted")
	}
}

func TestMigrateLegacyUpdaterStateReadErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(legacyUpdaterStatePath(configpaths.Get()), 0o700); err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	migrateLegacyUpdaterState(configpaths.Get(), &ui)
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); err != nil {
		t.Fatal("an unreadable updater.yaml must survive a read failure")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "reading legacy updater state") {
		t.Errorf("a non-not-found read error must warn, got %v", ui.WarnCalls)
	}
}

func TestMigrateLegacyUpdaterStateRootStateReadErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(rootStatePath, []byte("not: valid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ui := termio.NewTestMock(t)
	migrateLegacyUpdaterState(configpaths.Get(), &ui)
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); err != nil {
		t.Fatal("updater.yaml must survive a root-state read failure")
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "updating root state for legacy updater migration") {
		t.Errorf("a root-state error must warn, got %v", ui.WarnCalls)
	}
}

func TestMigrateLegacyUpdaterStateLockErrorIsNonFatal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.MkdirAll(filepath.Join(configpaths.Get().UserStateDir(), "locks", "root.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); err != nil {
		t.Fatal("updater.yaml must survive a lock failure")
	}
}

func TestMigrateLegacyUpdaterStateWriteErrorIsNonFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not block writes")
	}
	configpaths.WithMockConfigPaths(t)
	stateDir := configpaths.Get().UserStateDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "locks", "root.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  opencode:\n    last_checked: 2020-01-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	if _, err := os.Stat(legacyUpdaterStatePath(configpaths.Get())); err != nil {
		t.Fatal("updater.yaml must survive a root-state write failure")
	}
}

// TestMigrateLegacyUpdaterStateFoldsPastAZeroAgent pins the loop control: a
// zero-LastChecked agent is skipped, not a reason to stop, so a non-zero agent
// sorted after it is still folded. Agents are visited in sorted order ("aaa"
// before "opencode"), so a `break` on the zero entry deterministically drops
// opencode.
func TestMigrateLegacyUpdaterStateFoldsPastAZeroAgent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("agents:\n  aaa:\n    current_version: 1\n  opencode:\n    last_checked: 2021-06-01T00:00:00Z\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	root, _ := state.ReadRootState()
	if got := root.AgentCheck("opencode").LastChecked.UTC().Year(); got != 2021 {
		t.Fatalf("opencode was not folded past a zero agent: year=%d", got)
	}
}

// TestMigrateLegacyUpdaterStateTypeErrorFoldsNothing pins the parse guard against
// a partial decode: yaml.v3 sets an earlier valid field before failing on a later
// type error, and the guard must discard that partial state rather than fold it.
func TestMigrateLegacyUpdaterStateTypeErrorFoldsNothing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	rootStatePath := filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
	if err := os.WriteFile(
		legacyUpdaterStatePath(configpaths.Get()),
		[]byte("last_checked: 2021-06-01T00:00:00Z\nagents: 5\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	migrateLegacyUpdaterState(configpaths.Get(), &termio.Mock{})
	if _, err := os.Stat(rootStatePath); !os.IsNotExist(err) {
		t.Fatal("a parse failure must fold nothing, leaving no root document")
	}
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

// TestFoldFlatFieldIntoOpencodeIgnoresZeroLastChecked pins the guard on the flat
// cadence: a zero last_checked must leave the agents map untouched, not seed an
// opencode entry with the zero value.
func TestFoldFlatFieldIntoOpencodeIgnoresZeroLastChecked(t *testing.T) {
	legacy := legacyUpdaterState{}
	foldFlatFieldIntoOpencode(&legacy)
	if legacy.Agents != nil {
		t.Errorf("a zero flat last_checked must not create an agents entry, got %v", legacy.Agents)
	}
}

// legacyMirror is an independent description of the retired updater.yaml shape.
// The fuzz target parses the same bytes into it to derive the expected result
// without going through parseLegacyUpdaterState.
type legacyMirror struct {
	LastChecked time.Time `yaml:"last_checked"`
	Agents      map[string]struct {
		LastChecked time.Time `yaml:"last_checked"`
	} `yaml:"agents"`
}
