package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

func TestLearnSessionRoundTrips(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	started := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	baseline := []LearnManifestEntry{
		{Key: ".config/opencode/opencode.jsonc", Size: 128, ModTime: started, Hash: "abc"},
		{Key: "opencode2:opencode2-sqlite", Hash: "def"},
	}
	if err := RecordLearnSession(k, LearnSession{
		StartedAt:    started,
		HomeVolume:   "vol-1",
		ArtefactPath: "/state/learn/proj/1.json",
		Baseline:     baseline,
	}); err != nil {
		t.Fatalf("RecordLearnSession: %v", err)
	}

	got, ok, err := ReadLearnSession(k)
	if err != nil {
		t.Fatalf("ReadLearnSession: %v", err)
	}
	if !ok {
		t.Fatal("ReadLearnSession ok = false, want a recorded session")
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, started)
	}
	if got.HomeVolume != "vol-1" {
		t.Errorf("HomeVolume = %q, want vol-1", got.HomeVolume)
	}
	if got.ArtefactPath != "/state/learn/proj/1.json" {
		t.Errorf("ArtefactPath = %q, want /state/learn/proj/1.json", got.ArtefactPath)
	}
	if len(got.Baseline) != len(baseline) {
		t.Fatalf("Baseline = %+v, want %+v", got.Baseline, baseline)
	}
	for i := range baseline {
		if got.Baseline[i].Key != baseline[i].Key ||
			got.Baseline[i].Size != baseline[i].Size ||
			got.Baseline[i].Hash != baseline[i].Hash ||
			!got.Baseline[i].ModTime.Equal(baseline[i].ModTime) {
			t.Errorf("Baseline[%d] = %+v, want %+v", i, got.Baseline[i], baseline[i])
		}
	}

	// A second record replaces the first.
	if err := RecordLearnSession(k, LearnSession{StartedAt: started.Add(time.Hour), HomeVolume: "vol-2"}); err != nil {
		t.Fatalf("RecordLearnSession (replace): %v", err)
	}
	got2, ok, err := ReadLearnSession(k)
	if err != nil || !ok {
		t.Fatalf("ReadLearnSession after replace: ok=%v err=%v", ok, err)
	}
	if got2.HomeVolume != "vol-2" || len(got2.Baseline) != 0 {
		t.Errorf("replacement = %+v, want HomeVolume vol-2 and no baseline", got2)
	}

	// Clearing removes the record.
	if err := ClearLearnSession(k); err != nil {
		t.Fatalf("ClearLearnSession: %v", err)
	}
	if _, ok, err = ReadLearnSession(k); err != nil || ok {
		t.Fatalf("ReadLearnSession after clear: ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestLearnSessionAbsentIsFalse(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if _, ok, err := ReadLearnSession(k); err != nil || ok {
		t.Fatalf("ReadLearnSession (no state): ok=%v err=%v, want ok=false", ok, err)
	}

	// A state that exists but records no session.
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if _, ok, err := ReadLearnSession(k); err != nil || ok {
		t.Fatalf("ReadLearnSession (no record): ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestClearLearnSessionIsNoOpWhenAbsent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := ClearLearnSession(k); err != nil {
		t.Fatalf("ClearLearnSession (no state): %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Errorf("ClearLearnSession created state: err = %v, want ErrStateNotFound", err)
	}

	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := ClearLearnSession(k); err != nil {
		t.Fatalf("ClearLearnSession (no record): %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.HomeVolume != "vol" || got.Learn != nil {
		t.Errorf("state = %+v, want the home volume unchanged and no learn record", got)
	}
}

func TestLearnSessionDroppedFromYAMLWhenAbsent(t *testing.T) {
	data, err := marshalHomeState(
		versionedDocument[HomeState]{Schema: HomeStateSchema, Document: HomeState{HomeVolume: "vol"}},
	)
	if err != nil {
		t.Fatalf("marshalHomeState: %v", err)
	}
	if strings.Contains(string(data), "learn:") {
		t.Fatalf("an absent learn session should omit learn:\n%s", data)
	}
}
