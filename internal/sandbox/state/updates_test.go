package state

import (
	"errors"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestAgentCheckLivesUnderUpdates(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := writeRootState(RootState{Updates: Updates{Agents: map[string]AgentCheckState{
		"opencode": {LatestVersion: "2.0.0"},
	}}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "2.0.0" {
		t.Fatalf("Updates.Agents = %+v", got.Updates.Agents)
	}
}

func TestRootStateMarshalsUpdatesKey(t *testing.T) {
	rs := &RootState{}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"1.5.2"}})
	rs.SetAgentCheck("opencode", AgentCheckState{LatestVersion: "2.0.0"})
	data, err := marshalRootState(*rs)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["agents"]; ok { // no top-level agents key
		t.Fatalf("root state still has a top-level agents: key:\n%s", data)
	}
	updates, ok := raw["updates"].(map[string]any)
	if !ok {
		t.Fatalf("updates = %v, want a mapping:\n%s", raw["updates"], data)
	}
	launcher, ok := updates["launcher"].(map[string]any)
	if !ok {
		t.Fatalf("updates.launcher = %v, want a mapping:\n%s", updates["launcher"], data)
	}
	if _, ok := launcher["dismissed_versions"]; !ok {
		t.Fatalf("updates.launcher missing dismissed_versions:\n%s", data)
	}
	agents, ok := updates["agents"].(map[string]any)
	if !ok {
		t.Fatalf("updates.agents = %v, want a mapping:\n%s", updates["agents"], data)
	}
	if _, ok := agents["opencode"]; !ok {
		t.Fatalf("updates.agents missing opencode entry:\n%s", data)
	}
}

func TestRootStateOmitsEmptyUpdates(t *testing.T) {
	data, err := marshalRootState(RootState{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "updates:") {
		t.Fatalf("empty root state should omit updates:\n%s", data)
	}
}

func TestLauncherUpdateAccessorsRoundTrip(t *testing.T) {
	rs := &RootState{}
	if !rs.LauncherUpdate().IsZero() {
		t.Fatal("zero RootState should have a zero LauncherUpdate")
	}
	rs.SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"2.0.0"}})
	if got := rs.LauncherUpdate().DismissedVersions; len(got) != 1 || got[0] != "2.0.0" {
		t.Fatalf("LauncherUpdate = %+v", got)
	}
}

func TestParseRootStateFoldsLegacyAgentsKey(t *testing.T) {
	legacy := []byte("agents:\n  opencode:\n    last_checked: 2026-01-01T00:00:00Z\n    latest_version: 1.2.3\n")
	got, err := parseRootState(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "1.2.3" {
		t.Fatalf("legacy agents key not folded: %+v", got.Updates)
	}
}

// A malformed top-level agents: key cannot be folded, so the document is
// unparsable rather than read as if the key were absent.
func TestParseRootStateMalformedLegacyAgentsKeyIsUnparsable(t *testing.T) {
	if _, err := parseRootState([]byte("agents: [1, 2]\n")); !errors.Is(err, ErrStateUnparsable) {
		t.Fatalf("parseRootState() error = %v, want ErrStateUnparsable", err)
	}
}

func TestParseRootStateNewShapeWinsOverLegacyKey(t *testing.T) {
	both := []byte(
		"updates:\n  agents:\n    opencode:\n      latest_version: new\nagents:\n  opencode:\n    latest_version: old\n",
	)
	got, err := parseRootState(both)
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "new" {
		t.Fatalf("new shape did not win: %+v", got.Updates.Agents)
	}
}

// TestParseRootStateLegacyKeyKeepsLauncher covers the mixed shape the brief warns
// about: a legacy top-level agents: key alongside a populated updates.launcher.
// The fold must adopt the legacy map without rebuilding Updates from scratch and
// dropping the launcher.
func TestParseRootStateLegacyKeyKeepsLauncher(t *testing.T) {
	mixed := []byte(
		"updates:\n  launcher:\n    last_checked: 2026-01-02T03:04:05Z\nagents:\n  opencode:\n    latest_version: 1.2.3\n",
	)
	got, err := parseRootState(mixed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "1.2.3" {
		t.Fatalf("legacy agents key not folded: %+v", got.Updates)
	}
	if got.Updates.Launcher.LastChecked.IsZero() {
		t.Fatalf("fold dropped updates.launcher: %+v", got.Updates)
	}
}

func TestRootStateWriteDropsLegacyAgentsKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacy := []byte("agents:\n  opencode:\n    latest_version: 1.2.3\n")
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootStateFile(), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := WithRootState(true, func(tx *RootTxn) error {
		tx.State().SetLauncherUpdate(LauncherUpdateState{DismissedVersions: []string{"1.0.0"}})
		return tx.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\nagents:") {
		t.Fatalf("legacy top-level agents key survived a write:\n%s", data)
	}
	if !strings.Contains(string(data), "opencode") {
		t.Fatalf("folded agent check was lost on write:\n%s", data)
	}
}

// The legacy agents: fold must not disturb the other root-state registries: an
// old document that carries both a top-level agents: key and adoption claims
// folds the agent checks and keeps the claims.
func TestParseRootStateLegacyAgentsFoldKeepsAdoptionClaims(t *testing.T) {
	doc := []byte(
		"adopted_home_volumes:\n  widget-aaa/opencode:\n    checkout_slug: checkout-a\n    volume: vol-a\nagents:\n  opencode:\n    latest_version: 1.2.3\n",
	)
	got, err := parseRootState(doc)
	if err != nil {
		t.Fatalf("parseRootState: %v", err)
	}
	if got.Updates.Agents["opencode"].LatestVersion != "1.2.3" {
		t.Fatalf("legacy agents key not folded: %+v", got.Updates.Agents)
	}
	claim, ok := got.AdoptedHomeVolume("widget-aaa", "opencode")
	if !ok || claim.CheckoutSlug != "checkout-a" || claim.Volume != "vol-a" {
		t.Fatalf("adoption claim dropped by the legacy fold: %+v", got.AdoptedHomeVolumes)
	}
}
