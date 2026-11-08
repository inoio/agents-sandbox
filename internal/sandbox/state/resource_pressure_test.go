package state

import (
	"errors"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

func TestRecordResourcePressureWritesExistingState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	seenAt := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	rp := ResourcePressureState{
		Kind:      "memory",
		Evidence:  "oom-log",
		Victim:    "opencode",
		OOMSeenAt: seenAt,
	}
	if err := RecordResourcePressure(k, rp); err != nil {
		t.Fatalf("RecordResourcePressure: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.ResourcePressure == nil {
		t.Fatal("ResourcePressure is nil")
	}
	if got.ResourcePressure.Kind != "memory" || got.ResourcePressure.Victim != "opencode" {
		t.Errorf("ResourcePressure = %+v", got.ResourcePressure)
	}
	if !got.ResourcePressure.OOMSeenAt.Equal(seenAt) {
		t.Errorf("OOMSeenAt = %v, want %v", got.ResourcePressure.OOMSeenAt, seenAt)
	}
	if got.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want preserved vol", got.HomeVolume)
	}
}

func TestRecordResourcePressureSkipsMissingState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := RecordResourcePressure(k, ResourcePressureState{Kind: "memory"}); err != nil {
		t.Fatalf("RecordResourcePressure: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

func TestRecordResourcePressureSkipsUnchangedMarker(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	rp := ResourcePressureState{Kind: "memory", OOMSeenAt: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	if err := writeState(k, HomeState{ResourcePressure: &rp}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	if err := RecordResourcePressure(k, rp); err != nil {
		t.Fatalf("RecordResourcePressure: %v", err)
	}
}

func TestMarkResourcePressureOfferedPreservesFields(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	seenAt := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if err := writeState(k, HomeState{
		HomeVolume:       "vol",
		ResourcePressure: &ResourcePressureState{Kind: "memory", Victim: "cat", OOMSeenAt: seenAt},
	}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	offeredAt := seenAt.Add(time.Hour)
	if err := MarkResourcePressureOffered(k, seenAt, offeredAt); err != nil {
		t.Fatalf("MarkResourcePressureOffered: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.ResourcePressure == nil {
		t.Fatal("ResourcePressure is nil")
	}
	if !got.ResourcePressure.OfferedAt.Equal(offeredAt) {
		t.Errorf("OfferedAt = %v, want %v", got.ResourcePressure.OfferedAt, offeredAt)
	}
	if got.ResourcePressure.Kind != "memory" || got.ResourcePressure.Victim != "cat" {
		t.Errorf("Kind/Victim not preserved: %+v", got.ResourcePressure)
	}
	if !got.ResourcePressure.OOMSeenAt.Equal(seenAt) {
		t.Errorf("OOMSeenAt = %v, want %v", got.ResourcePressure.OOMSeenAt, seenAt)
	}
}

func TestMarkResourcePressureOfferedSkipsNewerHit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	answeredSeenAt := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	newerSeenAt := answeredSeenAt.Add(time.Minute)
	if err := writeState(k, HomeState{
		ResourcePressure: &ResourcePressureState{Kind: "memory", OOMSeenAt: newerSeenAt},
	}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	if err := MarkResourcePressureOffered(k, answeredSeenAt, answeredSeenAt.Add(time.Hour)); err != nil {
		t.Fatalf("MarkResourcePressureOffered: %v", err)
	}

	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.ResourcePressure == nil || !got.ResourcePressure.OfferedAt.IsZero() {
		t.Fatalf("a newer hit must stay unanswered, got %+v", got.ResourcePressure)
	}
}

func TestMarkResourcePressureOfferedSkipsAbsentMarker(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	if err := MarkResourcePressureOffered(k, time.Time{}, time.Now()); err != nil {
		t.Fatalf("MarkResourcePressureOffered: %v", err)
	}
	got, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got.ResourcePressure != nil {
		t.Fatalf("ResourcePressure = %+v, want nil", got.ResourcePressure)
	}
}

// TestMarkResourcePressureOfferedSkipsMissingState pins that answering with no
// state at all is a no-op and never creates a fingerprint-less state file.
func TestMarkResourcePressureOfferedSkipsMissingState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := Key{Slug: "proj", Agent: "opencode"}

	if err := MarkResourcePressureOffered(k, time.Time{}, time.Now()); err != nil {
		t.Fatalf("MarkResourcePressureOffered: %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}
