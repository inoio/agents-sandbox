package state

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

func TestLearnDecline(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline: %v", err)
	}

	declined, err := LearnDeclined("widget-abc", "h1")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if !declined {
		t.Error("the recorded evidence hash must be declined")
	}

	declined, err = LearnDeclined("widget-abc", "h2")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if declined {
		t.Error("changed evidence must re-arm the finding")
	}

	declined, err = LearnDeclined("widget-xyz", "h1")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if declined {
		t.Error("a decline must not leak across repo slugs")
	}
}

// TestRecordLearnDeclineIsIdempotent pins that recording an already-recorded
// hash appends nothing: the finding stays declined once and the entry keeps its
// original timestamp.
func TestRecordLearnDeclineIsIdempotent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline: %v", err)
	}
	before, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	recorded := before.LearnDeclines["widget-abc"][0]

	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline: %v", err)
	}
	after, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	declines := after.LearnDeclines["widget-abc"]
	if len(declines) != 1 {
		t.Fatalf("LearnDeclines[widget-abc] = %+v, want one entry", declines)
	}
	if !declines[0].At.Equal(recorded.At) {
		t.Errorf("the duplicate record rewrote At: %v → %v", recorded.At, declines[0].At)
	}
}

func TestRecordLearnDeclineDistinctHashesAccumulate(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	for _, hash := range []string{"h1", "h2"} {
		if err := RecordLearnDecline("widget-abc", hash); err != nil {
			t.Fatalf("RecordLearnDecline(%s): %v", hash, err)
		}
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if len(got.LearnDeclines["widget-abc"]) != 2 {
		t.Fatalf("LearnDeclines[widget-abc] = %+v, want both hashes", got.LearnDeclines["widget-abc"])
	}
	for _, hash := range []string{"h1", "h2"} {
		declined, err := LearnDeclined("widget-abc", hash)
		if err != nil {
			t.Fatalf("LearnDeclined(%s): %v", hash, err)
		}
		if !declined {
			t.Errorf("%s must be declined", hash)
		}
	}
}

// TestLearnDeclineAbsentIsNotDeclined pins the missing-document and
// missing-slug read path: a finding is proposed until a decline is recorded.
func TestLearnDeclineAbsentIsNotDeclined(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	declined, err := LearnDeclined("widget-abc", "h1")
	if err != nil {
		t.Fatalf("LearnDeclined: %v", err)
	}
	if declined {
		t.Error("nothing recorded yet, so nothing can be declined")
	}
}

// TestLearnDeclinedSurfacesACorruptDocument pins that a root document that
// cannot be read is returned as an error rather than reported as "not declined":
// an unreadable decline memory must not silently re-propose a declined finding.
func TestLearnDeclinedSurfacesACorruptDocument(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootStateFile(), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LearnDeclined("widget-abc", "h1"); err == nil {
		t.Fatal("LearnDeclined over a corrupt document = nil error, want a read failure")
	}
}

// TestRootStateOmitsEmptyLearnDeclines pins the omitempty contract: an empty
// registry is dropped from the document rather than written as an empty key.
func TestRootStateOmitsEmptyLearnDeclines(t *testing.T) {
	data, err := marshalRootState(RootState{})
	if err != nil {
		t.Fatalf("marshalRootState: %v", err)
	}
	if strings.Contains(string(data), "learn_declines:") {
		t.Fatalf("empty root state should omit learn_declines:\n%s", data)
	}
}

func TestLearnDeclineWireKeys(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline: %v", err)
	}

	data, err := os.ReadFile(rootStateFile())
	if err != nil {
		t.Fatalf("read root state: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	declines, ok := raw["learn_declines"].(map[string]any)
	if !ok {
		t.Fatalf("learn_declines = %v, want a mapping:\n%s", raw["learn_declines"], data)
	}
	entries, ok := declines["widget-abc"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("learn_declines.widget-abc = %v, want one entry", declines["widget-abc"])
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entry = %v, want a mapping", entries[0])
	}
	if entry["evidence_hash"] != "h1" {
		t.Errorf("evidence_hash = %v, want h1", entry["evidence_hash"])
	}
	if _, ok := entry["at"]; !ok {
		t.Error("at key missing")
	}
}

// TestRecordLearnDeclineAbsentDocumentCreatesTheRegistry covers the write path
// when nothing was recorded before: the registry is initialized and persisted.
func TestRecordLearnDeclineAbsentDocumentCreatesTheRegistry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	if err := RecordLearnDecline("widget-abc", "h1"); err != nil {
		t.Fatalf("RecordLearnDecline: %v", err)
	}
	got, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	if len(got.LearnDeclines) != 1 {
		t.Fatalf("LearnDeclines = %+v, want one slug", got.LearnDeclines)
	}
}
