package pressure

import (
	"math"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestFromSystemLogParsesOOMKill(t *testing.T) {
	entries := []msbSdk.LogEntry{{
		Source: msbSdk.LogSourceSystem,
		Data:   []byte("[    3.046430] Out of memory: Killed process 468 (cat) total-vm:1628kB, oom_score_adj:0"),
	}}
	got, _, ok := FromSystemLogAt(entries)
	if !ok || got.Kind != KindMemory || got.Evidence != EvidenceOOMLog || got.Victim != "cat" {
		t.Fatalf("%+v ok=%v", got, ok)
	}
	// An OOM-log signal carries no metrics figures; the fields stay zero until a
	// headroom snapshot fills them.
	if got.UsedMiB != 0 || got.LimitMiB != 0 {
		t.Errorf("OOM signal metrics = (%d, %d), want (0, 0)", got.UsedMiB, got.LimitMiB)
	}
}

func TestFromSystemLogIgnoresNonOOM(t *testing.T) {
	entries := []msbSdk.LogEntry{{
		Source: msbSdk.LogSourceSystem,
		Data:   []byte("reboot: Power down"),
	}}
	if _, _, ok := FromSystemLogAt(entries); ok {
		t.Fatal("want no signal")
	}
}

// TestFromSystemLogAtUnterminatedVictim pins the parseVictim no-closing-paren
// branch: a truncated kill line still reports the hit, with an empty victim.
func TestFromSystemLogAtUnterminatedVictim(t *testing.T) {
	entries := []msbSdk.LogEntry{{
		Source: msbSdk.LogSourceSystem,
		Data:   []byte("Out of memory: Killed process 42 (opencode"),
	}}
	got, _, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "" {
		t.Fatalf("%+v ok=%v, want a hit with an empty victim", got, ok)
	}
}

// TestFromSystemLogAtNoVictimParen pins the parseVictim no-open-paren branch:
// a kill line without a victim still reports the hit with an empty victim.
func TestFromSystemLogAtNoVictimParen(t *testing.T) {
	entries := []msbSdk.LogEntry{{
		Source: msbSdk.LogSourceSystem,
		Data:   []byte("Out of memory: Killed process 42"),
	}}
	got, _, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "" {
		t.Fatalf("%+v ok=%v, want a hit with an empty victim", got, ok)
	}
}

// TestBytesToMiBSaturates pins the overflow guard for a byte count whose MiB
// value exceeds uint32.
func TestBytesToMiBSaturates(t *testing.T) {
	if got := bytesToMiB(uint64(math.MaxUint32) << 21); got != math.MaxUint32 {
		t.Errorf("bytesToMiB(huge) = %d, want saturating %d", got, uint32(math.MaxUint32))
	}
}

func TestFromSystemLogReturnsNewest(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	entries := []msbSdk.LogEntry{
		{
			Source:    msbSdk.LogSourceSystem,
			Timestamp: early,
			Data:      []byte("Out of memory: Killed process 1 (early-victim)"),
		},
		{
			Source:    msbSdk.LogSourceSystem,
			Timestamp: late,
			Data:      []byte("Out of memory: Killed process 2 (late-victim)"),
		},
	}
	got, _, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "late-victim" {
		t.Fatalf("%+v ok=%v", got, ok)
	}
}

func TestFromSystemLogAtReturnsNewestTimestamp(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	entries := []msbSdk.LogEntry{
		{Source: msbSdk.LogSourceSystem, Timestamp: early, Data: []byte("Out of memory: Killed process 1 (early)")},
		{Source: msbSdk.LogSourceSystem, Timestamp: late, Data: []byte("Out of memory: Killed process 2 (late)")},
	}
	got, at, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "late" || !at.Equal(late) {
		t.Fatalf("%+v at=%v ok=%v", got, at, ok)
	}
}

func TestFromSystemLogAtNoHit(t *testing.T) {
	_, at, ok := FromSystemLogAt([]msbSdk.LogEntry{{
		Source: msbSdk.LogSourceSystem,
		Data:   []byte("reboot: Power down"),
	}})
	if ok {
		t.Fatal("want no signal")
	}
	if !at.IsZero() {
		t.Fatalf("timestamp = %v, want the zero time for no hit", at)
	}
}

func TestFromSystemLogAtSkipsOlderAndNonOOMEntries(t *testing.T) {
	late := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	early := late.Add(-time.Hour)
	entries := []msbSdk.LogEntry{
		{Source: msbSdk.LogSourceSystem, Timestamp: late, Data: []byte("Out of memory: Killed process 2 (late)")},
		{Source: msbSdk.LogSourceSystem, Timestamp: early, Data: []byte("Out of memory: Killed process 1 (early)")},
		{Source: msbSdk.LogSourceSystem, Timestamp: late, Data: []byte("reboot: Power down")},
	}
	got, at, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "late" || !at.Equal(late) {
		t.Fatalf("%+v at=%v ok=%v, want the newest OOM entry", got, at, ok)
	}
}

// systemEntry builds a system-log entry the runtime wrote at loggedAt.
func systemEntry(loggedAt time.Time, data string) msbSdk.LogEntry {
	return msbSdk.LogEntry{Source: msbSdk.LogSourceSystem, Timestamp: loggedAt, Data: []byte(data)}
}

const (
	bootStartLine = "INFO microsandbox_runtime::runner::vm: sandbox starting sandbox=project-vm"
	oomKillLine   = "[    1.732160] Out of memory: Killed process 464 (opencode) total-vm:394852kB"
)

// TestFromSystemLogAtFingerprintIsStableAcrossStop pins the OOM fingerprint
// against the runtime's logging (observed on a KVM host): a running VM's read
// stamps the kernel line when it was written, while a stopped VM's read carries
// the same line re-stamped with the stop time. Both must yield the same
// fingerprint, the boot start plus the kernel uptime, or an answered kill is
// offered again after the VM idle-stops.
func TestFromSystemLogAtFingerprintIsStableAcrossStop(t *testing.T) {
	bootStart := time.Date(2026, 11, 5, 20, 30, 19, 677_000_000, time.UTC)
	running := []msbSdk.LogEntry{
		systemEntry(bootStart, bootStartLine),
		systemEntry(bootStart.Add(1900*time.Millisecond), oomKillLine),
	}
	stopped := []msbSdk.LogEntry{
		systemEntry(bootStart, bootStartLine),
		systemEntry(bootStart.Add(4*time.Second), "INFO microsandbox_runtime::runner::vm: graceful shutdown requested"),
		systemEntry(bootStart.Add(4*time.Second+9*time.Millisecond), oomKillLine),
	}

	_, runningAt, runningFound := FromSystemLogAt(running)
	_, stoppedAt, stoppedFound := FromSystemLogAt(stopped)

	want := bootStart.Add(1_732_160 * time.Microsecond)
	if !runningFound || !stoppedFound || !runningAt.Equal(want) || !stoppedAt.Equal(want) {
		t.Fatalf("fingerprints running=%v (%v) stopped=%v (%v); want both %v",
			runningAt, runningFound, stoppedAt, stoppedFound, want)
	}
}

// TestFromSystemLogAtOrdersKillsAcrossBoots pins that a later boot's kill is
// newer even with a smaller kernel uptime, because each kill is anchored at its
// own boot's start.
func TestFromSystemLogAtOrdersKillsAcrossBoots(t *testing.T) {
	firstBoot := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	secondBoot := firstBoot.Add(time.Hour)
	entries := []msbSdk.LogEntry{
		systemEntry(firstBoot, bootStartLine),
		systemEntry(firstBoot.Add(time.Minute), "[  900.000000] Out of memory: Killed process 9 (first) total-vm:1kB"),
		systemEntry(secondBoot, bootStartLine),
		systemEntry(
			secondBoot.Add(time.Second),
			"[    0.500000] Out of memory: Killed process 9 (second) total-vm:1kB",
		),
	}

	// The read's order must not matter: each kill is anchored at the latest
	// boot start logged before it.
	for _, read := range [][]msbSdk.LogEntry{entries, {entries[2], entries[0], entries[1], entries[3]}} {
		got, at, ok := FromSystemLogAt(read)

		if !ok || got.Victim != "second" || !at.Equal(secondBoot.Add(500*time.Millisecond)) {
			t.Fatalf("newest = %q at %v (%v); want the second boot's kill at %v",
				got.Victim, at, ok, secondBoot.Add(500*time.Millisecond))
		}
	}
}

// TestFromSystemLogAtFallsBackToTheLoggedTime pins the fallback: without the
// boot start in the read, or without a kernel uptime prefix, the kill is
// identified by the time the runtime logged it.
func TestFromSystemLogAtFallsBackToTheLoggedTime(t *testing.T) {
	loggedAt := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		entries []msbSdk.LogEntry
	}{
		{name: "no boot start", entries: []msbSdk.LogEntry{systemEntry(loggedAt, oomKillLine)}},
		{name: "boot start after the kill", entries: []msbSdk.LogEntry{
			systemEntry(loggedAt, oomKillLine),
			systemEntry(loggedAt.Add(time.Second), bootStartLine),
		}},
		{name: "no uptime prefix", entries: []msbSdk.LogEntry{
			systemEntry(loggedAt.Add(-time.Minute), bootStartLine),
			systemEntry(loggedAt, "Out of memory: Killed process 464 (opencode) total-vm:1kB"),
		}},
		{name: "malformed uptime", entries: []msbSdk.LogEntry{
			systemEntry(loggedAt.Add(-time.Minute), bootStartLine),
			systemEntry(loggedAt, "[ uptime] Out of memory: Killed process 464 (opencode) total-vm:1kB"),
		}},
		{name: "unterminated uptime", entries: []msbSdk.LogEntry{
			systemEntry(loggedAt.Add(-time.Minute), bootStartLine),
			systemEntry(loggedAt, "[    1.5 Out of memory: Killed process 464 (opencode) total-vm:1kB"),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, at, ok := FromSystemLogAt(tc.entries); !ok || !at.Equal(loggedAt) {
				t.Errorf("fingerprint = %v (%v), want the logged time %v", at, ok, loggedAt)
			}
		})
	}
}

// TestNewestOOMLoggedAt pins the read bound for the boot-start lookup: the
// newest time the runtime logged an OOM line.
func TestNewestOOMLoggedAt(t *testing.T) {
	first := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	entries := []msbSdk.LogEntry{
		systemEntry(first, oomKillLine),
		systemEntry(first.Add(2*time.Minute), oomKillLine),
		systemEntry(first.Add(time.Minute), oomKillLine),
		systemEntry(first.Add(3*time.Minute), bootStartLine),
	}
	if at, ok := NewestOOMLoggedAt(entries); !ok || !at.Equal(first.Add(2*time.Minute)) {
		t.Errorf("NewestOOMLoggedAt = %v (%v), want %v", at, ok, first.Add(2*time.Minute))
	}
	if _, ok := NewestOOMLoggedAt(entries[3:]); ok {
		t.Error("NewestOOMLoggedAt found an OOM line in a log without one")
	}
}

func TestUnderPressurePercentOrFloor(t *testing.T) {
	threshold := DefaultThreshold()
	if !UnderPressure(950, 1000, threshold) {
		t.Error("95% should trip")
	}
	if !UnderPressure(2000, 3000, threshold) {
		t.Error("1000 MiB free below the floor should trip")
	}
	if UnderPressure(2048, 4096, threshold) {
		t.Error("50% with 2 GiB free must not trip")
	}
	if UnderPressure(100, 0, threshold) {
		t.Error("zero limit must not trip")
	}
}

func TestFromMetricsHeadroom(t *testing.T) {
	m := &msbSdk.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20}
	got, ok := FromMetrics(m, DefaultThreshold())
	if !ok || got.Kind != KindMemory || got.Evidence != EvidenceHeadroom {
		t.Fatalf("%+v ok=%v", got, ok)
	}
}

func TestFromMetricsZeroLimit(t *testing.T) {
	if _, ok := FromMetrics(&msbSdk.Metrics{MemoryBytes: 1, MemoryLimitBytes: 0}, DefaultThreshold()); ok {
		t.Fatal("zero limit must not trip")
	}
}

func TestFromMetricsBelowThreshold(t *testing.T) {
	m := &msbSdk.Metrics{MemoryBytes: 512 << 20, MemoryLimitBytes: 4096 << 20}
	if _, ok := FromMetrics(m, DefaultThreshold()); ok {
		t.Fatal("50% with 3.5 GiB free must not trip")
	}
}

func TestDefaultThresholdValues(t *testing.T) {
	d := DefaultThreshold()
	if d.Percent != 0.90 || d.FloorMiB != 1024 || d.ReArmPercent != 0.80 {
		t.Fatalf("%+v", d)
	}
}

func TestRecoveredBelowReArmLevel(t *testing.T) {
	threshold := DefaultThreshold()
	// Plenty of free memory (> the floor) and usage below the re-arm fraction.
	if !Recovered(&msbSdk.Metrics{MemoryBytes: 5000 << 20, MemoryLimitBytes: 8192 << 20}, threshold) {
		t.Error("61% usage with 3 GiB free should be recovered")
	}
	// Below the trip level but above the re-arm fraction: the percent trigger
	// has not cleared its hysteresis band.
	if Recovered(&msbSdk.Metrics{MemoryBytes: 6800 << 20, MemoryLimitBytes: 8192 << 20}, threshold) {
		t.Error("83% usage is above the re-arm level and must not report recovered")
	}
	if Recovered(&msbSdk.Metrics{MemoryBytes: 7800 << 20, MemoryLimitBytes: 8192 << 20}, threshold) {
		t.Error("95% usage is not recovered")
	}
}

// TestRecoveredFloorTrippedButBelowReArm pins the free-floor/re-arm
// interaction: the absolute floor trips independently of the percent, so a
// small-limit snapshot can be below ReArmPercent and still under pressure. It
// must not report recovered, or an answered headroom offer re-arms and re-nags
// on every launch (specs/resource-limit-proposal.md §8).
func TestRecoveredFloorTrippedButBelowReArm(t *testing.T) {
	threshold := DefaultThreshold()
	// 1000 MiB limit, 700 MiB used: 70% (< 80% re-arm) but only 300 MiB free
	// (≤ 1024 floor), so still under pressure.
	if Recovered(&msbSdk.Metrics{MemoryBytes: 700 << 20, MemoryLimitBytes: 1000 << 20}, threshold) {
		t.Error("a floor-tripped snapshot below the re-arm level must not report recovered")
	}
}

func TestRecoveredUnknownLimit(t *testing.T) {
	threshold := DefaultThreshold()
	if Recovered(nil, threshold) {
		t.Error("a nil snapshot must not report recovered")
	}
	if Recovered(&msbSdk.Metrics{MemoryLimitBytes: 0}, threshold) {
		t.Error("an unknown limit must not report recovered")
	}
	if Recovered(&msbSdk.Metrics{MemoryLimitBytes: 1}, threshold) {
		t.Error("a sub-MiB limit must not report recovered")
	}
}

func TestMemoryLimitMiB(t *testing.T) {
	if got := MemoryLimitMiB(nil); got != 0 {
		t.Errorf("MemoryLimitMiB(nil) = %d, want 0", got)
	}
	if got := MemoryLimitMiB(&msbSdk.Metrics{MemoryLimitBytes: 4096 << 20}); got != 4096 {
		t.Errorf("MemoryLimitMiB = %d, want 4096", got)
	}
}

// TestParseVictimLeadingParen pins the parseVictim open-paren boundary: a comm
// at position 0 (the victim text begins with "(") is still extracted.
func TestParseVictimLeadingParen(t *testing.T) {
	if got := parseVictim("(cat) total-vm:1kB"); got != "cat" {
		t.Errorf("parseVictim = %q, want cat", got)
	}
}

// TestFromSystemLogAtScansPastNonOOMAndOlderEntries pins the loop controls: a
// non-OOM entry and an older OOM entry between newer ones must be skipped, not
// stop the scan.
func TestFromSystemLogAtScansPastNonOOMAndOlderEntries(t *testing.T) {
	mid := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	early := mid.Add(-time.Hour)
	late := mid.Add(time.Hour)
	entries := []msbSdk.LogEntry{
		{Source: msbSdk.LogSourceSystem, Timestamp: mid, Data: []byte("Out of memory: Killed process 1 (mid)")},
		{Source: msbSdk.LogSourceSystem, Timestamp: mid, Data: []byte("reboot: Power down")},
		{Source: msbSdk.LogSourceSystem, Timestamp: early, Data: []byte("Out of memory: Killed process 2 (early)")},
		{Source: msbSdk.LogSourceSystem, Timestamp: late, Data: []byte("Out of memory: Killed process 3 (late)")},
	}
	got, at, ok := FromSystemLogAt(entries)
	if !ok || got.Victim != "late" || !at.Equal(late) {
		t.Fatalf("%+v at=%v ok=%v, want the newest OOM after a non-OOM and an older one", got, at, ok)
	}
}

// TestUnderPressurePercentBoundary pins the integer percent comparison: each
// case leaves more than the floor free, so only the percent trigger can decide,
// and a large limit makes the 90%-vs-100 multiplier boundary observable.
func TestUnderPressurePercentBoundary(t *testing.T) {
	threshold := DefaultThreshold()
	cases := []struct {
		name        string
		used, limit uint32
		want        bool
	}{
		{"exactly at the threshold", 18000, 20000, true},
		{"just above the threshold", 18100, 20000, true},
		{"just below the threshold", 17900, 20000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnderPressure(tc.used, tc.limit, threshold); got != tc.want {
				t.Errorf("UnderPressure(%d,%d) = %v, want %v", tc.used, tc.limit, got, tc.want)
			}
		})
	}
}

// TestUnderPressureFloorBoundary pins the floor comparison at exactly the floor:
// 1 GiB free with 75% used must trip, and only the floor can decide.
func TestUnderPressureFloorBoundary(t *testing.T) {
	if !UnderPressure(3072, 4096, DefaultThreshold()) {
		t.Error("free space exactly at the floor must trip")
	}
}

// TestUnderPressureLimitBoundary pins the floor trigger at exactly the limit:
// with a threshold whose percent trigger cannot fire, usage equal to the limit
// still trips through the zero free space.
func TestUnderPressureLimitBoundary(t *testing.T) {
	if !UnderPressure(100, 100, Threshold{Percent: 1.5, FloorMiB: 0}) {
		t.Error("used == limit must trip the floor trigger")
	}
}

// TestUnderPressureUsedAboveLimitDoesNotUnderflow pins the usedMiB <= limitMiB
// operand: for usage above the limit the uint32 limitMiB-usedMiB wraps, and a
// wrapped value at or below the floor must not be reported as pressure.
// Reaching the operand needs a threshold whose percent trigger stays silent at
// a huge usage, so the percent is deliberately extreme; for any realistic
// percent the trigger above already covers usage at or above the limit.
func TestUnderPressureUsedAboveLimitDoesNotUnderflow(t *testing.T) {
	if UnderPressure(math.MaxUint32, 999, Threshold{Percent: 1e7, FloorMiB: 1000}) {
		t.Error("usage above the limit must not trip the floor through a wrapped subtraction")
	}
}

// TestFromMetricsNilSnapshot pins the nil guard: a nil snapshot is not pressure
// (and must not be dereferenced).
func TestFromMetricsNilSnapshot(t *testing.T) {
	if _, ok := FromMetrics(nil, DefaultThreshold()); ok {
		t.Fatal("a nil snapshot must not trip")
	}
}

// TestRecoveredAtExactReArmLevelIsNotRecovered pins the re-arm boundary: usage
// exactly at ReArmPercent (with the floor clear) is not yet recovered.
func TestRecoveredAtExactReArmLevelIsNotRecovered(t *testing.T) {
	// 80% usage, 2 GiB free: the floor does not fire, so only the re-arm
	// comparison decides.
	if Recovered(&msbSdk.Metrics{MemoryBytes: 8000 << 20, MemoryLimitBytes: 10000 << 20}, DefaultThreshold()) {
		t.Error("usage exactly at the re-arm level must not report recovered")
	}
}
