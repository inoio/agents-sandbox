package state

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestFirstRunRoundTrips pins that a recorded first-run decision reads back
// unchanged, so the menu can skip the prompt on the next run.
func TestFirstRunRoundTrips(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	want := FirstRunRecord{Choice: FirstRunLearned, SourceHash: "hostconfig-abc123"}
	if err := RecordFirstRun("widget-abc", "opencode", want); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}

	got, ok, err := ReadFirstRun("widget-abc", "opencode")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if !ok {
		t.Fatal("the recorded first run must be present")
	}
	if got.Choice != want.Choice || got.SourceHash != want.SourceHash {
		t.Errorf("ReadFirstRun = %+v, want choice %q and source hash %q", got, want.Choice, want.SourceHash)
	}
	if got.At.IsZero() {
		t.Error("ReadFirstRun did not stamp At")
	}
}

// TestFirstRunSecondRecordReplaces pins that a changed source hash overwrites
// the stored record: the host config moved, so the menu re-arms and the new
// decision is the one remembered.
func TestFirstRunSecondRecordReplaces(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	first := FirstRunRecord{Choice: FirstRunDismissed, SourceHash: "hostconfig-abc123"}
	if err := RecordFirstRun("widget-abc", "opencode", first); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}

	second := FirstRunRecord{Choice: FirstRunManual, SourceHash: "hostconfig-def456"}
	if err := RecordFirstRun("widget-abc", "opencode", second); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}

	got, ok, err := ReadFirstRun("widget-abc", "opencode")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if !ok {
		t.Fatal("the replaced record must be present")
	}
	if got.Choice != second.Choice || got.SourceHash != second.SourceHash {
		t.Errorf("ReadFirstRun = %+v, want the second record's choice %q and source hash %q",
			got, second.Choice, second.SourceHash)
	}

	root, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if len(root.FirstRuns) != 1 {
		t.Errorf("FirstRuns = %+v, want one entry for the replaced key", root.FirstRuns)
	}
}

// TestFirstRunAbsentIsFalse pins the missing-document and missing-key read path:
// nothing recorded reports (zero, false, nil), so the menu offers the prompt.
func TestFirstRunAbsentIsFalse(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	got, ok, err := ReadFirstRun("widget-abc", "opencode")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if ok {
		t.Error("nothing recorded yet, so no first run can be present")
	}
	if got != (FirstRunRecord{}) {
		t.Errorf("ReadFirstRun = %+v, want the zero record", got)
	}
}

// TestFirstRunKeyedBySlugAndAgent pins that the record is keyed by the pair: two
// agents under one repo slug, and one agent under two slugs, stay independent.
func TestFirstRunKeyedBySlugAndAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	records := []struct {
		slug, agent string
		choice      FirstRunChoice
	}{
		{"widget-abc", "opencode", FirstRunLearned},
		{"widget-abc", "claudecode", FirstRunManual},
		{"widget-xyz", "opencode", FirstRunDismissed},
	}
	for _, record := range records {
		if err := RecordFirstRun(
			record.slug,
			record.agent,
			FirstRunRecord{Choice: record.choice, SourceHash: "h1"},
		); err != nil {
			t.Fatalf("RecordFirstRun(%s, %s): %v", record.slug, record.agent, err)
		}
	}

	for _, want := range records {
		got, ok, err := ReadFirstRun(want.slug, want.agent)
		if err != nil {
			t.Fatalf("ReadFirstRun(%s, %s): %v", want.slug, want.agent, err)
		}
		if !ok || got.Choice != want.choice || got.SourceHash != "h1" {
			t.Errorf("ReadFirstRun(%s, %s) = %+v, %v, want choice %q and source hash h1",
				want.slug, want.agent, got, ok, want.choice)
		}
	}

	root, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if len(root.FirstRuns) != len(records) {
		t.Errorf("FirstRuns has %d entries, want %d", len(root.FirstRuns), len(records))
	}
}

// TestFirstRunKeepsExplicitAtAndStampsZero pins the At contract: a caller that
// supplies a timestamp keeps it, and a zero one is stamped from the clock.
func TestFirstRunKeepsExplicitAtAndStampsZero(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	explicit := time.Date(2026, 11, 19, 10, 30, 0, 0, time.UTC)
	if err := RecordFirstRun("widget-abc", "opencode", FirstRunRecord{
		Choice: FirstRunLearned, SourceHash: "h1", At: explicit,
	}); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}
	got, _, err := ReadFirstRun("widget-abc", "opencode")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if !got.At.Equal(explicit) {
		t.Errorf("At = %v, want the supplied %v", got.At, explicit)
	}

	if err := RecordFirstRun("widget-abc", "claudecode", FirstRunRecord{
		Choice: FirstRunLearned, SourceHash: "h1",
	}); err != nil {
		t.Fatalf("RecordFirstRun: %v", err)
	}
	got, _, err = ReadFirstRun("widget-abc", "claudecode")
	if err != nil {
		t.Fatalf("ReadFirstRun: %v", err)
	}
	if got.At.IsZero() {
		t.Error("a zero At must be stamped, not stored zero")
	}
}

// TestFirstRunDroppedFromYAMLWhenAbsent pins the omitempty contract: an empty
// registry is dropped from the document rather than written as an empty key.
func TestFirstRunDroppedFromYAMLWhenAbsent(t *testing.T) {
	data, err := marshalRootState(RootState{})
	if err != nil {
		t.Fatalf("marshalRootState: %v", err)
	}
	if strings.Contains(string(data), "first_runs:") {
		t.Fatalf("empty root state should omit first_runs:\n%s", data)
	}
}

// TestReadFirstRunSurfacesACorruptDocument pins that a root document that cannot
// be read is returned as an error rather than reported as "not recorded": an
// unreadable record must not silently re-offer a decision already made.
func TestReadFirstRunSurfacesACorruptDocument(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootStateFile(), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok, err := ReadFirstRun("widget-abc", "opencode")
	if err == nil {
		t.Fatal("ReadFirstRun over a corrupt document = nil error, want a read failure")
	}
	if ok {
		t.Error("a failed read reported a first run; want false")
	}
	if got != (FirstRunRecord{}) {
		t.Errorf("ReadFirstRun = %+v, want the zero record", got)
	}
}
