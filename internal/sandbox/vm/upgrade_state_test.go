package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
)

func TestUpgradeStateRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	want := upgradeState{
		Agents: map[string]agentUpgradeState{
			"opencode": {
				LastChecked:     time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
				OfferedVersions: []string{"1.2.3", "1.3.0"},
				CurrentVersion:  "1.3.0",
				AgentSource:     agentSourceTool,
				DockerSource:    agentSourceUser,
			},
		},
	}
	if err := saveUpgradeState(want); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	got, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := got.Agents["opencode"]
	wantOC := want.Agents["opencode"]
	if !oc.LastChecked.Equal(wantOC.LastChecked) {
		t.Errorf("LastChecked = %v, want %v", oc.LastChecked, wantOC.LastChecked)
	}
	if strings.Join(oc.OfferedVersions, ",") != strings.Join(wantOC.OfferedVersions, ",") {
		t.Errorf("OfferedVersions = %v, want %v", oc.OfferedVersions, wantOC.OfferedVersions)
	}
	if oc.CurrentVersion != wantOC.CurrentVersion {
		t.Errorf("CurrentVersion = %q, want %q", oc.CurrentVersion, wantOC.CurrentVersion)
	}
	if oc.AgentSource != agentSourceTool || oc.DockerSource != agentSourceUser {
		t.Errorf("sources = %q/%q, want tool/user", oc.AgentSource, oc.DockerSource)
	}
}

func TestUpgradeStateMigrationFoldsLegacyIntoOpencode(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := "last_checked: 2026-09-01T00:00:00Z\noffered_versions: [\"1.0.0\"]\ncurrent_version: \"0.9.0\"\nagent_source: tool\ndocker_source: tool\n"
	if err := os.WriteFile(upgradeStatePath(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := loadUpgradeState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Agents["opencode"].CurrentVersion != "0.9.0" {
		t.Errorf("migrated current_version = %q, want 0.9.0", state.Agents["opencode"].CurrentVersion)
	}
	if len(state.Agents["opencode"].OfferedVersions) != 1 {
		t.Errorf("migrated offered_versions = %v", state.Agents["opencode"].OfferedVersions)
	}
	if state.Agents["opencode"].AgentSource != agentSourceTool {
		t.Errorf("migrated agent_source = %q, want tool", state.Agents["opencode"].AgentSource)
	}
}

func TestUpgradeStatePerAgentIsolation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	pi, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}

	if err := saveUpgradeState(upgradeState{
		Agents: map[string]agentUpgradeState{
			"opencode": {
				CurrentVersion: "1.0.0",
				LastChecked:    time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
			},
			"pi": {
				CurrentVersion: "2.0.0",
				LastChecked:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
			},
		},
	}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	if got := currentUpgradeVersion(opencodeAgent(t)); got != "1.0.0" {
		t.Errorf("opencode version = %q, want 1.0.0", got)
	}
	if got := currentUpgradeVersion(pi); got != "2.0.0" {
		t.Errorf("pi version = %q, want 2.0.0", got)
	}
	if got := currentUpgradeVersion(&fakeAgent{}); got != "" {
		t.Errorf("unrecorded agent version = %q, want empty", got)
	}

	// dueForCheck uses each agent's own LastChecked: at 8/19 12:00 opencode
	// (checked 8/18 12:00) is due, pi (checked 8/19 12:00) is not.
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	st, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	oc := st.Agents["opencode"]
	if !oc.dueForCheck(now) {
		t.Error("expected opencode to be due for check")
	}
	piState := st.Agents["pi"]
	if piState.dueForCheck(now) {
		t.Error("expected pi to not be due for check")
	}
}

func TestUpgradeStateLoadsVersionWithoutLastChecked(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	path := filepath.Join(configpaths.Get().UserStateDir(), "updater.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A state written before the current-version field existed must still load
	// and expose the stored version.
	if err := os.WriteFile(path, []byte("current_version: \"1.2.3\"\n"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if got := currentUpgradeVersion(opencodeAgent(t)); got != "1.2.3" {
		t.Errorf("currentUpgradeVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestCurrentUpgradeVersionWhenMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if got := currentUpgradeVersion(opencodeAgent(t)); got != "" {
		t.Errorf("currentUpgradeVersion() = %q, want empty for missing file", got)
	}
}

func TestLoadUpgradeStateWhenMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	got, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState on missing file: %v", err)
	}
	if !got.LastChecked.IsZero() {
		t.Errorf("expected zero LastChecked for missing file, got %v", got.LastChecked)
	}
	if len(got.OfferedVersions) != 0 {
		t.Errorf("expected empty OfferedVersions for missing file, got %v", got.OfferedVersions)
	}
}

func TestLoadUpgradeStateIgnoresCorruptFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	path := filepath.Join(configpaths.Get().UserStateDir(), "updater.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("::: not yaml :::"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	got, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState should tolerate corrupt file, got: %v", err)
	}
	if !got.LastChecked.IsZero() || len(got.OfferedVersions) != 0 {
		t.Errorf("expected empty state for corrupt file, got %+v", got)
	}
}

func TestUpgradeStateDueForCheck(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	within := now.Add(-23 * time.Hour)
	overdue := now.Add(-25 * time.Hour)

	cases := []struct {
		name  string
		state agentUpgradeState
		want  bool
	}{
		{name: "zero time is due", state: agentUpgradeState{}, want: true},
		{name: "checked within 24h not due", state: agentUpgradeState{LastChecked: within}, want: false},
		{name: "checked over 24h is due", state: agentUpgradeState{LastChecked: overdue}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.dueForCheck(now); got != tc.want {
				t.Errorf("dueForCheck() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpgradeStateOffered(t *testing.T) {
	s := agentUpgradeState{OfferedVersions: []string{"1.2.3"}}
	if !s.offered("1.2.3") {
		t.Error("expected 1.2.3 to be marked offered")
	}
	if s.offered("1.3.0") {
		t.Error("did not expect 1.3.0 to be offered")
	}
}
