package resourceproposal

import (
	"context"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// MemoryReads abstracts the live-VM reads a memory-hit detection performs. Both
// msb.Sandbox and msb.SandboxHandle satisfy it.
type MemoryReads interface {
	Metrics(ctx context.Context) (*msbSdk.Metrics, error)
	Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error)
}

// MemoryLimits are the figures a memory offer is bounded by; a zero CurrentMiB
// or BootMaxMiB means unknown.
type MemoryLimits struct {
	// CurrentMiB is the VM's configured memory; zero falls back to the live
	// metrics limit.
	CurrentMiB uint32
	// BootMaxMiB is the VM's boot maximum (host RAM at creation).
	BootMaxMiB uint32
	// HostAvailableMiB is the memory the host can still hand out without
	// swapping (sysinfo.AvailableMemoryMiB), and HostAvailableKnown whether the
	// platform reported it; a known zero is an exhausted host.
	HostAvailableMiB   uint32
	HostAvailableKnown bool
}

// DetectAndOfferMemoryRaise reads r's system log and metrics for a memory-limit
// hit, dedups it against the project's pressure marker, and — interactively —
// offers a raise, persisting an accepted value. marker is the project's current
// marker (nil for none); the re-arm path rewrites only this function's local
// copy, never the caller's struct. witnessedHeadroomTrip reports a
// trip the caller already observed in-session so a headroom offer still fires
// when the fresh metrics read has recovered. limits bounds the offer. It returns the memory setting it persisted (e.g. "8192M"), or "".
// It never returns an
// error and never fails the caller: read, prompt, and persist failures are
// logged and swallowed.
//
// The HomeState marker is the dedup key. An OOM kill is offered when no marker
// exists, when the kill is newer than the recorded one, or when the recorded
// kill was never answered (a non-interactive run). A headroom trip is offered
// when no headroom condition is recorded or the recorded one was never
// answered; an answered headroom offer stays closed until the condition
// recovers below the re-arm level and trips again. Answering advances the
// marker. The recorded OOM timestamp is preserved across a headroom re-arm, so
// clearing pressure never replays an answered kill
// (specs/resource-limit-proposal.md §1, §7, §8).
func DetectAndOfferMemoryRaise(
	ctx context.Context,
	k state.Key,
	r MemoryReads,
	marker *state.ResourcePressureState,
	limits MemoryLimits,
	witnessedHeadroomTrip bool,
	opts options.RunOptions,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) string {
	threshold := pressure.DefaultThreshold()

	logEntries, logsErr := r.Logs(ctx, msbSdk.LogOptions{
		Tail:    oomLogTail,
		Since:   time.Time{},
		Until:   time.Time{},
		Sources: []msbSdk.LogSource{msbSdk.LogSourceSystem},
	})
	if logsErr != nil {
		ui.Verbosef("reading VM system log failed: %v (continuing)", logsErr)
		logEntries = nil
	}
	logEntries = withBootStart(ctx, r, logEntries, ui)
	oomSignal, oomAt, oomFound := pressure.FromSystemLogAt(logEntries)

	metrics, metricsErr := r.Metrics(ctx)
	if metricsErr != nil {
		ui.Verbosef("reading VM metrics failed: %v (continuing)", metricsErr)
		metrics = nil
	}
	headroomSignal, metricsTripped := pressure.FromMetrics(metrics, threshold)

	// Headroom attributes a hit only through the trip the in-session watcher
	// observed: a fresh read alone (a busy VM another client uses at launch) is
	// not a hit. When the fresh read does not describe the trip (recovered, or
	// the read failed), a bare headroom signal stands in; it is only consumed
	// under headroomTripped.
	headroomTripped := witnessedHeadroomTrip
	if !metricsTripped {
		headroomSignal = pressure.Signal{
			Kind:     pressure.KindMemory,
			Evidence: pressure.EvidenceHeadroom,
			Victim:   "",
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; the used/limit fields are never read from a bare headroom signal"
			UsedMiB: 0,
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; the used/limit fields are never read from a bare headroom signal"
			LimitMiB: 0,
		}
	}

	if limits.CurrentMiB == 0 {
		limits.CurrentMiB = pressure.MemoryLimitMiB(metrics)
	}

	marker = reArmAnsweredHeadroom(k, marker, metrics, threshold, ui)

	var (
		hit   pressure.Signal
		hitAt time.Time
		offer bool
	)
	switch {
	case oomFound && oomNeedsOffer(marker, oomAt):
		hit, hitAt, offer = oomSignal, oomAt, true
	case !oomFound && unansweredOOM(marker):
		// The recorded kill was never answered, but its log line has left the
		// tail (or the VM was recreated). Offer it from the marker so a
		// non-interactive run's promise is kept (specs/resource-limit-proposal.md
		// §1(d), §7).
		hit = pressure.Signal{
			Kind:     pressure.KindMemory,
			Evidence: pressure.EvidenceOOMLog,
			Victim:   marker.Victim,
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; an OOM-log signal's used/limit fields are never read"
			UsedMiB: 0,
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; an OOM-log signal's used/limit fields are never read"
			LimitMiB: 0,
		}
		hitAt, offer = marker.OOMSeenAt, true
	case headroomTripped && headroomNeedsOffer(marker):
		hit, hitAt, offer = headroomSignal, headroomSeenAt(marker, time.Now()), true
	case !headroomTripped && unansweredHeadroom(marker):
		// The recorded headroom condition was never answered, but the fresh
		// metrics read has recovered. Offer it from the marker so a
		// non-interactive run's promise is kept.
		hit = pressure.Signal{
			Kind:     pressure.KindMemory,
			Evidence: pressure.EvidenceHeadroom,
			Victim:   marker.Victim,
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; a headroom signal's used/limit fields are never read"
			UsedMiB: 0,
			// gomutants:disable-next-line INTEGER_INCREMENT reason="the marker records only Kind/Evidence/Victim; a headroom signal's used/limit fields are never read"
			LimitMiB: 0,
		}
		hitAt, offer = marker.OOMSeenAt, true
	}
	if !offer {
		return ""
	}

	recordResourcePressure(k, state.ResourcePressureState{
		Kind:      string(hit.Kind),
		Evidence:  string(hit.Evidence),
		Victim:    hit.Victim,
		OOMSeenAt: hitAt,
		OfferedAt: time.Time{},
	}, ui)

	// A raise needs a known basis. Without one (an OOM line whose metrics read
	// failed) SuggestRaise(0, …) would pre-fill a decrease, so record the hit
	// unanswered and let the next launch — which knows the configured memory —
	// offer it.
	if limits.CurrentMiB == 0 {
		ui.Verbosef("recorded a VM memory-limit hit with no known limit; a later run will offer a raise")
		return ""
	}

	return presentMemoryRaise(hit, hitAt, limits, k, opts, paths, ui)
}

// withBootStart extends a tail read holding an OOM line with everything logged
// before it, so the kill's boot start, which its fingerprint is anchored at, is
// present even when it has left the tail. A failed read keeps the tail.
func withBootStart(ctx context.Context, r MemoryReads, tail []msbSdk.LogEntry, ui termio.Logger) []msbSdk.LogEntry {
	loggedAt, found := pressure.NewestOOMLoggedAt(tail)
	if !found {
		// gomutants:disable-next-line RETURN_ZERO reason="a tail without an OOM line yields no kill whether it is returned or dropped"
		return tail
	}
	earlier, err := r.Logs(ctx, msbSdk.LogOptions{
		Tail:    0,
		Since:   time.Time{},
		Until:   loggedAt,
		Sources: []msbSdk.LogSource{msbSdk.LogSourceSystem},
	})
	if err != nil {
		ui.Verbosef("reading the VM system log back to the boot start failed: %v (continuing)", err)
		return tail
	}
	return append(earlier, tail...)
}

// reArmAnsweredHeadroom clears an answered headroom marker once its condition
// has recovered, preserving the recorded OOM timestamp. An unanswered hit is kept
// so its offer still happens on a later run.
func reArmAnsweredHeadroom(
	k state.Key,
	marker *state.ResourcePressureState,
	metrics *msbSdk.Metrics,
	threshold pressure.Threshold,
	ui termio.Logger,
) *state.ResourcePressureState {
	if marker == nil || marker.Evidence != string(pressure.EvidenceHeadroom) ||
		marker.OfferedAt.IsZero() || !pressure.Recovered(metrics, threshold) {
		return marker
	}
	reArmed := state.ResourcePressureState{
		Kind:      "",
		Evidence:  "",
		Victim:    "",
		OOMSeenAt: marker.OOMSeenAt,
		OfferedAt: time.Time{},
	}
	recordResourcePressure(k, reArmed, ui)
	return &reArmed
}

// presentMemoryRaise offers hit for an answer and persists an accepted raise. A
// non-interactive UI records the hit unanswered: it neither offers nor advances
// the marker, so the next interactive run offers it. It returns the persisted
// memory setting, or "". A prompt or persist failure is logged, never returned,
// so detection stays best-effort.
func presentMemoryRaise(
	hit pressure.Signal,
	hitAt time.Time,
	limits MemoryLimits,
	k state.Key,
	opts options.RunOptions,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) string {
	if !ui.IsInteractive() {
		ui.Verbosef("recorded a VM memory-limit hit; a later interactive run will offer a raise")
		return ""
	}
	currentMiB := limits.CurrentMiB
	suggestion := Suggestion{
		CurrentMiB:  currentMiB,
		ProposedMiB: SuggestRaise(currentMiB, limits.BootMaxMiB),
		BootMaxMiB:  limits.BootMaxMiB,
	}
	if source, shadowed := ShadowSource(memoryKey, opts.SetByFlag(memoryKey)); shadowed {
		// A raise saved to config could not take effect, so explain instead of
		// asking; the explanation answers the hit.
		ui.Warnf(
			"The sandbox hit its memory limit. Memory is set by %s, so raise it there (for example to %s).",
			source, formatMiB(suggestion.ProposedMiB),
		)
		markResourcePressureOffered(k, hitAt, ui)
		return ""
	}
	if suggestion.ProposedMiB <= currentMiB {
		// The VM already runs at its boot maximum, so no raise could apply.
		ui.Warnf(
			"The sandbox hit its memory limit, which is already the host maximum %s; free host memory or reduce the workload.",
			formatMiB(limits.BootMaxMiB),
		)
		markResourcePressureOffered(k, hitAt, ui)
		return ""
	}
	newMiB, accepted, err := Ask(ui, hit, suggestion)
	if err != nil {
		ui.Verbosef("memory raise prompt failed: %v", err)
		return ""
	}
	// The user answered (accept or decline): advance the marker so this hit is
	// not offered again. The identity guard keeps a newer hit another session
	// recorded while the prompt was open unanswered.
	markResourcePressureOffered(k, hitAt, ui)
	if !accepted {
		return ""
	}
	warnIfTheHostLacksTheRaise(newMiB-currentMiB, limits, ui)
	dir, persistErr := PersistSetting(
		paths, configSlug(opts, k), memoryKey, memorySetting(newMiB), opts.SetByFlag(memoryKey),
	)
	if persistErr != nil {
		ui.Warnf("could not persist the raised memory limit: %v", persistErr)
		return ""
	}
	ui.Infof("Set memory to %s in the config in %s.", formatMiB(newMiB), dir)
	return memorySetting(newMiB)
}

// warnIfTheHostLacksTheRaise warns when the memory a raise lets the VM take
// exceeds what the host has available. The raise stays: spare VM memory is
// only backed once the guest uses it, but then the host may swap or OOM-kill.
func warnIfTheHostLacksTheRaise(increaseMiB uint32, limits MemoryLimits, ui termio.Logger) {
	if !limits.HostAvailableKnown || increaseMiB <= limits.HostAvailableMiB {
		return
	}
	ui.Warnf(
		"The raise lets the VM use %s more memory, but the host has only %s available; once the VM uses it, the host may swap or kill processes.",
		formatMiB(increaseMiB),
		formatMiB(limits.HostAvailableMiB),
	)
}

// configSlug is the slug whose per-slug user config the raise is written to: the
// resolver's slug when the run carried one (the CLI always sets it), else the
// state key's slug for non-CLI callers.
func configSlug(opts options.RunOptions, k state.Key) string {
	if opts.ConfigSlug != "" {
		return opts.ConfigSlug
	}
	return k.Slug
}

// oomLogTail bounds the system-log read to the newest entries, so the
// next-launch OOM scan stays cheap.
const oomLogTail = 200

// oomNeedsOffer reports whether an OOM kill found in the log requires an offer:
// no marker at all, a kill newer than the recorded one, or a recorded kill its
// run never answered. A recorded kill that was answered is never re-offered,
// even if the same line is still in the log.
func oomNeedsOffer(marker *state.ResourcePressureState, oomAt time.Time) bool {
	if marker == nil {
		return true
	}
	if oomAt.After(marker.OOMSeenAt) {
		return true
	}
	return marker.OOMSeenAt.Equal(oomAt) && marker.OfferedAt.IsZero()
}

// unansweredOOM reports whether the marker records an OOM kill its run never
// answered. Such a hit stays offerable even when the log line has left the tail
// or the VM was recreated, so a non-interactive run's promise is kept.
func unansweredOOM(marker *state.ResourcePressureState) bool {
	return marker != nil &&
		marker.Evidence == string(pressure.EvidenceOOMLog) &&
		marker.OfferedAt.IsZero()
}

// unansweredHeadroom reports whether the marker records a headroom condition its
// run never answered. Such a hit stays offerable even after the condition has
// recovered, so a non-interactive run's promise is kept.
func unansweredHeadroom(marker *state.ResourcePressureState) bool {
	return marker != nil &&
		marker.Evidence == string(pressure.EvidenceHeadroom) &&
		marker.OfferedAt.IsZero()
}

// headroomNeedsOffer reports whether a headroom trip requires an offer: no
// recorded headroom condition, or one recorded but never answered. An answered
// offer stays closed until a re-arm clears the recorded headroom evidence.
func headroomNeedsOffer(marker *state.ResourcePressureState) bool {
	if marker == nil || marker.Evidence != string(pressure.EvidenceHeadroom) {
		return true
	}
	return marker.OfferedAt.IsZero()
}

// headroomSeenAt fingerprints a headroom trip: the time the condition was first
// seen, preserved from an already-recorded headroom hit, else now. A re-armed
// condition starts a fresh fingerprint so its re-trip is a new hit.
func headroomSeenAt(marker *state.ResourcePressureState, now time.Time) time.Time {
	if marker != nil && marker.Evidence == string(pressure.EvidenceHeadroom) && !marker.OOMSeenAt.IsZero() {
		return marker.OOMSeenAt
	}
	return now
}

// recordResourcePressure writes the marker, reporting a write failure at
// verbose level rather than failing the run: the detection is best-effort and
// the hit will be re-detected on the next launch.
func recordResourcePressure(k state.Key, rp state.ResourcePressureState, ui termio.Logger) {
	if err := state.RecordResourcePressure(k, rp); err != nil {
		ui.Verbosef("recording the VM memory-limit hit failed: %v", err)
	}
}

// markResourcePressureOffered advances the answered timestamp of the hit
// identified by seenAt, reporting a write failure at verbose level rather than
// failing the run.
func markResourcePressureOffered(k state.Key, seenAt time.Time, ui termio.Logger) {
	if err := state.MarkResourcePressureOffered(k, seenAt, time.Now()); err != nil {
		ui.Verbosef("recording the memory raise answer failed: %v", err)
	}
}
