package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
)

func TestUpgradeStateRoundTrip(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	want := upgradeState{
		LastChecked:     time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		OfferedVersions: []string{"1.2.3", "1.3.0"},
		CurrentVersion:  "1.3.0",
	}
	if err := saveUpgradeState(want); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	got, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	if !got.LastChecked.Equal(want.LastChecked) {
		t.Errorf("LastChecked = %v, want %v", got.LastChecked, want.LastChecked)
	}
	if strings.Join(got.OfferedVersions, ",") != strings.Join(want.OfferedVersions, ",") {
		t.Errorf("OfferedVersions = %v, want %v", got.OfferedVersions, want.OfferedVersions)
	}
	if got.CurrentVersion != want.CurrentVersion {
		t.Errorf("CurrentVersion = %q, want %q", got.CurrentVersion, want.CurrentVersion)
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

	if got := currentUpgradeVersion(); got != "1.2.3" {
		t.Errorf("currentUpgradeVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestCurrentUpgradeVersionWhenMissing(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if got := currentUpgradeVersion(); got != "" {
		t.Errorf("currentUpgradeVersion() = %q, want empty for missing file", got)
	}
}

func TestRecordUpgradeVersion(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Recording preserves unrelated fields already persisted in the state.
	prior := upgradeState{LastChecked: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	if err := saveUpgradeState(prior); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}

	if err := recordUpgradeVersion("2.0.0"); err != nil {
		t.Fatalf("recordUpgradeVersion: %v", err)
	}

	got, err := loadUpgradeState()
	if err != nil {
		t.Fatalf("loadUpgradeState: %v", err)
	}
	if got.CurrentVersion != "2.0.0" {
		t.Errorf("CurrentVersion = %q, want %q", got.CurrentVersion, "2.0.0")
	}
	if !got.LastChecked.Equal(prior.LastChecked) {
		t.Errorf("LastChecked = %v, want preserved %v", got.LastChecked, prior.LastChecked)
	}
}

func TestRecordUpgradeVersionIgnoresEmpty(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	// Recording an empty version must not clobber an existing stored version.
	if err := recordUpgradeVersion(""); err != nil {
		t.Fatalf("recordUpgradeVersion: %v", err)
	}
	if got := currentUpgradeVersion(); got != "1.0.0" {
		t.Errorf("currentUpgradeVersion() = %q, want preserved %q", got, "1.0.0")
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
		state upgradeState
		want  bool
	}{
		{name: "zero time is due", state: upgradeState{}, want: true},
		{name: "checked within 24h not due", state: upgradeState{LastChecked: within}, want: false},
		{name: "checked over 24h is due", state: upgradeState{LastChecked: overdue}, want: true},
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
	s := upgradeState{OfferedVersions: []string{"1.2.3"}}
	if !s.offered("1.2.3") {
		t.Error("expected 1.2.3 to be marked offered")
	}
	if s.offered("1.3.0") {
		t.Error("did not expect 1.3.0 to be offered")
	}
}
