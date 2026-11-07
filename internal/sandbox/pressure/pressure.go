// Package pressure classifies a VM resource-limit hit from evidence the
// launcher can read: the guest kernel OOM line in the sandbox system log, or a
// metrics snapshot whose memory headroom is below the threshold.
package pressure

import (
	"fmt"
	"math"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Kind identifies the limited resource: memory here, the disk limits in
// disk.go.
type Kind string

const (
	KindMemory Kind = "memory"
)

// Evidence identifies which read produced a Signal.
type Evidence string

const (
	// EvidenceOOMLog is the direct detector: the guest kernel's
	// "Out of memory: Killed process <pid> (<comm>)" printk, which reaches the
	// sandbox system log.
	EvidenceOOMLog Evidence = "oom-log"
	// EvidenceHeadroom is the fallback: a metrics snapshot whose memory usage
	// tripped the threshold. Used when no OOM line is found yet.
	EvidenceHeadroom Evidence = "headroom"
)

// Signal is one classified resource-pressure observation.
type Signal struct {
	Kind     Kind
	Evidence Evidence
	// Victim is the process comm of an OOM victim; empty for headroom.
	Victim   string
	UsedMiB  uint32
	LimitMiB uint32
}

// Threshold decides when memory pressure is offered. The percent and the floor
// are alternative triggers: either alone trips.
type Threshold struct {
	// Percent is the used fraction at or above which pressure trips.
	Percent float64
	// FloorMiB is an absolute free-space floor: free at or below this trips.
	FloorMiB uint32
	// ReArmPercent is the used fraction below which an answered offer re-arms.
	ReArmPercent float64
}

// DefaultThreshold is the single threshold concept shared by the memory
// fallback and (Phase 2) the disk signals.
func DefaultThreshold() Threshold {
	return Threshold{Percent: 0.90, FloorMiB: 1024, ReArmPercent: 0.80}
}

const (
	oomMarker = "Out of memory: Killed process "
	// bootStartMarker is the runtime's persisted line opening each boot.
	bootStartMarker = "sandbox starting"
)

// FromSystemLogAt returns the most recent OOM kill found in the entries along
// with its fingerprint, or ok=false when none is found. The fingerprint is what
// the anti-nag dedup compares a recorded kill against
// (specs/resources/resource-limit-proposal.md); see oomFingerprint.
func FromSystemLogAt(entries []msbSdk.LogEntry) (Signal, time.Time, bool) {
	var (
		newest  Signal
		newestT time.Time
		found   bool
	)
	for _, entry := range entries {
		_, rest, foundMarker := strings.Cut(string(entry.Data), oomMarker)
		if !foundMarker {
			continue
		}
		at := oomFingerprint(entries, entry)
		if found && !at.After(newestT) {
			continue
		}
		found = true
		newestT = at
		newest = Signal{
			Kind:     KindMemory,
			Evidence: EvidenceOOMLog,
			Victim:   parseVictim(rest),
			UsedMiB:  0,
			LimitMiB: 0,
		}
	}
	return newest, newestT, found
}

// NewestOOMLoggedAt returns when the runtime logged the newest OOM line, the
// bound for a read reaching back to that kill's boot start.
func NewestOOMLoggedAt(entries []msbSdk.LogEntry) (time.Time, bool) {
	var (
		newest time.Time
		found  bool
	)
	for _, entry := range entries {
		if strings.Contains(string(entry.Data), oomMarker) && entry.Timestamp.After(newest) {
			newest, found = entry.Timestamp, true
		}
	}
	return newest, found
}

// oomFingerprint identifies an OOM kill the same way in every read. The entry
// timestamp cannot: the runtime stamps a kernel line when it writes it and
// rewrites a boot's kernel lines with the stop time when the VM stops. The
// kill's boot start (the latest boot-start line logged before it) plus the
// kernel uptime in the line is stable and orders kills across boots. Without
// either, the entry timestamp stands in.
func oomFingerprint(entries []msbSdk.LogEntry, oom msbSdk.LogEntry) time.Time {
	uptime, ok := kernelUptime(string(oom.Data))
	if !ok {
		return oom.Timestamp
	}
	var (
		bootStart time.Time
		found     bool
	)
	for _, entry := range entries {
		if strings.Contains(string(entry.Data), bootStartMarker) && !entry.Timestamp.After(oom.Timestamp) &&
			entry.Timestamp.After(bootStart) {
			bootStart, found = entry.Timestamp, true
		}
	}
	if !found {
		return oom.Timestamp
	}
	return bootStart.Add(uptime)
}

// kernelUptime parses the "[    1.732160] " uptime prefix of a kernel line.
func kernelUptime(line string) (time.Duration, bool) {
	var seconds float64
	if _, err := fmt.Sscanf(line, "[%f]", &seconds); err != nil {
		// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="the uptime is ignored when ok is false"
		return 0, false
	}
	return time.Duration(math.Round(seconds*1e6)) * time.Microsecond, true
}

// parseVictim extracts the comm from "<pid> (<comm>) ...".
func parseVictim(rest string) string {
	open := strings.IndexByte(rest, '(')
	if open < 0 {
		return ""
	}
	closed := strings.IndexByte(rest[open:], ')')
	// gomutants:disable-next-line CONDITIONALS_BOUNDARY,INTEGER_INCREMENT reason="rest[open] is always '(' so closed is never 0; the closed==0 boundary cannot change the result"
	if closed < 0 {
		return ""
	}
	return rest[open+1 : open+closed]
}

// UnderPressure reports whether used against limit trips t: at or above
// Percent used, or at or below FloorMiB free.
func UnderPressure(usedMiB, limitMiB uint32, t Threshold) bool {
	if limitMiB == 0 {
		return false
	}
	// Integer-safe percent comparison: used/limit >= Percent.
	if uint64(usedMiB)*100 >= uint64(limitMiB)*uint64(t.Percent*100+0.5) {
		return true
	}
	// usedMiB <= limitMiB guards the subtraction: for usage above the limit the
	// uint32 difference wraps to a large value that must not be read as free
	// space. The percent trigger above already covers usage at or above the
	// limit for any Percent at or below 1, so this operand only decides for a
	// threshold with a larger percent.
	if usedMiB <= limitMiB && limitMiB-usedMiB <= t.FloorMiB {
		return true
	}
	return false
}

// FromMetrics classifies memory pressure from a snapshot, or ok=false when the
// limit is unknown (zero) or the usage does not trip t.
func FromMetrics(m *msbSdk.Metrics, t Threshold) (Signal, bool) {
	if m == nil || m.MemoryLimitBytes == 0 {
		return Signal{}, false
	}
	usedMiB := bytesToMiB(m.MemoryBytes)
	limitMiB := bytesToMiB(m.MemoryLimitBytes)
	if !UnderPressure(usedMiB, limitMiB, t) {
		return Signal{}, false
	}
	return Signal{
		Kind:     KindMemory,
		Evidence: EvidenceHeadroom,
		Victim:   "",
		UsedMiB:  usedMiB,
		LimitMiB: limitMiB,
	}, true
}

// MemoryLimitMiB returns a snapshot's memory limit in MiB, or 0 when the
// snapshot or its limit is unknown. It is the fallback basis for a suggested
// raise when the live VM's config cannot be read.
func MemoryLimitMiB(m *msbSdk.Metrics) uint32 {
	if m == nil {
		return 0
	}
	return bytesToMiB(m.MemoryLimitBytes)
}

// bytesToMiB converts a byte count to MiB, saturating rather than overflowing.
func bytesToMiB(bytes uint64) uint32 {
	mib := bytes >> 20
	if mib > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(mib)
}

// Recovered reports whether a snapshot's memory pressure has cleared, so an
// answered headroom offer may be offered again on a future trip. It requires
// both that no trigger still fires (neither the used fraction nor the free
// floor) and that usage is below t's re-arm fraction, so the percent trigger
// keeps its hysteresis. A nil snapshot or an unknown limit reports false:
// absence of pressure evidence is not proof of recovery.
//
// The "no trigger" half matters because the floor trips independently of the
// percent: for a small limit (free ≤ FloorMiB) a snapshot can sit below
// ReArmPercent and still be under pressure. Calling that recovered would re-arm
// and re-offer the same answered condition on every launch
// (specs/resources/resource-limit-proposal.md).
func Recovered(m *msbSdk.Metrics, t Threshold) bool {
	if m == nil || m.MemoryLimitBytes == 0 {
		return false
	}
	usedMiB := bytesToMiB(m.MemoryBytes)
	limitMiB := bytesToMiB(m.MemoryLimitBytes)
	if limitMiB == 0 || UnderPressure(usedMiB, limitMiB, t) {
		return false
	}
	return float64(usedMiB)/float64(limitMiB) < t.ReArmPercent
}
