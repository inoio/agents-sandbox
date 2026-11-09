package state

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

func diskStateKey() Key { return Key{Slug: "proj", Agent: "opencode"} }

func readDiskPressure(t *testing.T, k Key) map[string]DiskPressureState {
	t.Helper()
	hs, err := ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return hs.DiskPressure
}

// TestRecordDiskPressureKeepsOtherKindsAndFields pins that recording one kind's
// hit leaves the other kinds' markers and the rest of the state untouched.
func TestRecordDiskPressureKeepsOtherKindsAndFields(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tmp := DiskPressureState{SeenAt: seen, SizeKiB: 2048, AvailKiB: 10}
	if err := writeState(
		k,
		HomeState{HomeVolume: "vol", DiskPressure: map[string]DiskPressureState{"tmp": tmp}},
	); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	root := DiskPressureState{SeenAt: seen.Add(time.Hour), SizeKiB: 4096, AvailKiB: 100}
	if stored, err := RecordDiskPressure(k, "root-disk", root); err != nil || stored != root {
		t.Fatalf("RecordDiskPressure = %+v, %v; want %+v stored", stored, err, root)
	}

	got := readDiskPressure(t, k)
	if len(got) != 2 || got["root-disk"] != root || got["tmp"] != tmp {
		t.Errorf("DiskPressure = %+v, want root-disk %+v and tmp %+v", got, root, tmp)
	}
	if hs, _ := ReadState(k); hs.HomeVolume != "vol" {
		t.Errorf("HomeVolume = %q, want preserved vol", hs.HomeVolume)
	}
}

// TestRecordDiskPressureStartsTheMap pins the first hit recorded into a state
// that has no disk markers yet.
func TestRecordDiskPressureStartsTheMap(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	if err := writeState(k, HomeState{HomeVolume: "vol"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	tmp := DiskPressureState{SeenAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC), SizeKiB: 2048}

	if _, err := RecordDiskPressure(k, "tmp", tmp); err != nil {
		t.Fatalf("RecordDiskPressure: %v", err)
	}

	if got := readDiskPressure(t, k); len(got) != 1 || got["tmp"] != tmp {
		t.Errorf("DiskPressure = %+v, want only tmp %+v", got, tmp)
	}
}

// TestDiskPressureWritersSkipMissingState pins that no writer creates a
// fingerprint-less partial state.
func TestDiskPressureWritersSkipMissingState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	now := time.Now()

	hit := DiskPressureState{SeenAt: now}
	if stored, err := RecordDiskPressure(k, "tmp", hit); err != nil || stored != hit {
		t.Fatalf("RecordDiskPressure = %+v, %v; want the caller's hit back", stored, err)
	}
	if err := MarkDiskPressureOffered(k, "tmp", now, now); err != nil {
		t.Fatalf("MarkDiskPressureOffered: %v", err)
	}
	if err := ClearDiskPressure(k, "tmp", now); err != nil {
		t.Fatalf("ClearDiskPressure: %v", err)
	}
	if err := ClearDiskPressure(k, "tmp", time.Time{}); err != nil {
		t.Fatalf("ClearDiskPressure(zero hit): %v", err)
	}
	if _, err := ReadState(k); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("ReadState error = %v, want ErrStateNotFound (no state created)", err)
	}
}

// TestMarkDiskPressureOfferedAnswersOnlyTheNamedHit pins that an answer
// advances only the hit the offer named: a newer hit recorded meanwhile, or a
// kind without a marker, stays as it is.
func TestMarkDiskPressureOfferedAnswersOnlyTheNamedHit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	answeredAt := seen.Add(time.Minute)
	if err := writeState(k, HomeState{DiskPressure: map[string]DiskPressureState{
		"tmp":       {SeenAt: seen},
		"root-disk": {SeenAt: seen.Add(time.Hour)},
	}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	for _, call := range []struct {
		kind   string
		seenAt time.Time
	}{
		{kind: "tmp", seenAt: seen},
		{kind: "root-disk", seenAt: seen},
		{kind: "workspace-quota", seenAt: seen},
		{kind: "workspace-quota", seenAt: time.Time{}},
	} {
		if err := MarkDiskPressureOffered(k, call.kind, call.seenAt, answeredAt); err != nil {
			t.Fatalf("MarkDiskPressureOffered(%s): %v", call.kind, err)
		}
	}

	got := readDiskPressure(t, k)
	if !got["tmp"].OfferedAt.Equal(answeredAt) {
		t.Errorf("tmp OfferedAt = %v, want %v", got["tmp"].OfferedAt, answeredAt)
	}
	if !got["root-disk"].OfferedAt.IsZero() {
		t.Errorf("root-disk OfferedAt = %v, want the newer hit left unanswered", got["root-disk"].OfferedAt)
	}
	if _, ok := got["workspace-quota"]; ok {
		t.Error("answering a kind without a marker created one")
	}
}

// TestRecordDiskPressureKeepsAnExistingHit pins the race guard: a hit another
// session recorded first, answered or not, is kept and returned, so a later
// recorder neither re-opens an answered offer nor replaces a pending one.
func TestRecordDiskPressureKeepsAnExistingHit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	first := DiskPressureState{SeenAt: seen, SizeKiB: 2048, AvailKiB: 10, OfferedAt: seen.Add(time.Minute)}
	if err := writeState(k, HomeState{DiskPressure: map[string]DiskPressureState{"tmp": first}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	stored, err := RecordDiskPressure(k, "tmp", DiskPressureState{SeenAt: seen.Add(time.Hour), SizeKiB: 2048})
	if err != nil || stored != first {
		t.Fatalf("RecordDiskPressure = %+v, %v; want the existing hit %+v", stored, err, first)
	}
	if got := readDiskPressure(t, k)["tmp"]; got != first {
		t.Errorf("tmp marker = %+v, want the existing hit %+v kept", got, first)
	}
}

// TestRecordDiskPressureReportsAnUnreadableState pins that a state the
// transaction cannot read fails the record, returning the caller's hit.
func TestRecordDiskPressureReportsAnUnreadableState(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	if err := writeState(k, HomeState{}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := os.WriteFile(stateFile(k), []byte("{ corrupt: [\n"), 0o600); err != nil {
		t.Fatalf("corrupt state: %v", err)
	}
	hit := DiskPressureState{SeenAt: time.Now()}

	if stored, err := RecordDiskPressure(k, "tmp", hit); err == nil || stored != hit {
		t.Errorf("RecordDiskPressure = %+v, %v; want the caller's hit and an error", stored, err)
	}
}

// TestClearDiskPressureRemovesOnlyTheNamedHit pins the re-arm: clearing drops
// the kind's marker only when it is the hit the caller saw recover, so a newer
// hit another session recorded meanwhile, and the other kinds, stay.
func TestClearDiskPressureRemovesOnlyTheNamedHit(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	k := diskStateKey()
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if err := writeState(k, HomeState{DiskPressure: map[string]DiskPressureState{
		"tmp":       {SeenAt: seen},
		"root-disk": {SeenAt: seen.Add(time.Hour)},
	}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	for _, kind := range []string{"tmp", "root-disk", "workspace-quota"} {
		if err := ClearDiskPressure(k, kind, seen); err != nil {
			t.Fatalf("ClearDiskPressure(%s): %v", kind, err)
		}
	}

	got := readDiskPressure(t, k)
	if len(got) != 1 || !got["root-disk"].SeenAt.Equal(seen.Add(time.Hour)) {
		t.Errorf("DiskPressure = %+v, want only the newer root-disk hit kept", got)
	}
}
