package resourceproposal

import (
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const kibPerMiB = 1024

// DiskLimits are a run's disk limit settings in MiB, the memory limit /tmp
// shares (tmpfs is RAM-backed), and the free space on the host filesystems a
// root-disk and a quota raise draw on, each with whether it is known.
type DiskLimits struct {
	RootMiB           uint32
	WorkspaceQuotaMiB uint32
	TmpMiB            uint32
	MemoryMiB         uint32

	RootHostFreeMiB        uint32
	RootHostFreeKnown      bool
	WorkspaceHostFreeMiB   uint32
	WorkspaceHostFreeKnown bool
}

// RunDiskLimits are the disk limits a run's VM is set up with (the configured
// sizes or their defaults) and the memory limit /tmp shares.
func RunDiskLimits(opts options.RunOptions) DiskLimits {
	return DiskLimits{
		RootMiB:           options.ResolveDiskSizeMiB(opts.DiskSize),
		WorkspaceQuotaMiB: options.ResolveWorkspaceQuotaMiB(opts.WorkspaceQuota),
		TmpMiB:            options.ResolveTmpSizeMiB(opts.TmpSize, options.ParseMemory(opts.Memory)),
		MemoryMiB:         options.ParseMemory(opts.Memory),

		RootHostFreeMiB:        0,
		RootHostFreeKnown:      false,
		WorkspaceHostFreeMiB:   0,
		WorkspaceHostFreeKnown: false,
	}
}

// WithHostFreeSpace fills in the free space on the filesystem holding the
// runtime home (where root disks live) and on the repository's filesystem (where
// /workspace writes land; the launcher runs inside the repository).
func WithHostFreeSpace(limits DiskLimits) DiskLimits {
	limits.RootHostFreeMiB, limits.RootHostFreeKnown = sysinfo.FreeDiskMiB(configpaths.MSBHome())
	limits.WorkspaceHostFreeMiB, limits.WorkspaceHostFreeKnown = sysinfo.FreeDiskMiB(".")
	return limits
}

// diskLimit is one disk limit the offer can raise: the probed kind, its config
// key, its name for the user, and how a raise applies.
type diskLimit struct {
	kind        pressure.Kind
	key         string
	name        string
	appliesLive bool
	// mountSizeIsLimit reports whether the probed mount's size is the limit
	// itself. /workspace's size also counts the host repository's content, so
	// its limit is the configured quota.
	mountSizeIsLimit bool
	current          func(DiskLimits) uint32
	// maximum is the most a raise may reach and what that maximum is; zero
	// means none.
	maximum func(DiskLimits) (uint32, string)
	// hostFree is the free space on the host filesystem the raise draws on,
	// whether it is known, and what that filesystem is; nil when the limit does
	// not draw on one.
	hostFree func(DiskLimits) (uint32, bool, string)
}

// diskLimits are the disk limits in the order they are offered.
var diskLimits = []diskLimit{
	{
		kind: pressure.KindRootDisk, key: "disk-size", name: "root disk", appliesLive: true, mountSizeIsLimit: true,
		current: func(l DiskLimits) uint32 { return l.RootMiB },
		maximum: noMaximum,
		hostFree: func(l DiskLimits) (uint32, bool, string) {
			return l.RootHostFreeMiB, l.RootHostFreeKnown, "the host filesystem holding the sandbox runtime"
		},
	},
	{
		kind: pressure.KindWorkspaceQuota, key: "workspace-quota", name: "/workspace write quota", appliesLive: false,
		mountSizeIsLimit: false,
		current:          func(l DiskLimits) uint32 { return l.WorkspaceQuotaMiB },
		maximum:          noMaximum,
		hostFree: func(l DiskLimits) (uint32, bool, string) {
			return l.WorkspaceHostFreeMiB, l.WorkspaceHostFreeKnown, "the repository's host filesystem"
		},
	},
	{
		kind: pressure.KindTmp, key: "tmp-size", name: "/tmp size", appliesLive: false, mountSizeIsLimit: true,
		current: func(l DiskLimits) uint32 { return l.TmpMiB },
		maximum: func(l DiskLimits) (uint32, string) {
			return tmpRaiseMaximumMiB(l.MemoryMiB), "the tmp ceiling below the VM memory limit"
		},
		// /tmp is RAM, bounded by the memory limit, not by a host filesystem.
		hostFree: nil,
	},
}

func noMaximum(DiskLimits) (uint32, string) { return 0, "" }

// tmpRaiseMaximumMiB is the largest whole-GiB tmp size strictly below the memory
// limit. An explicit tmp-size must stay below memory (options.ValidateSizes) and
// a raise rounds to a whole GiB, so the offer must stop at a GiB boundary;
// memoryMiB-1 is the floor when no whole GiB fits (memory below 1 GiB).
func tmpRaiseMaximumMiB(memoryMiB uint32) uint32 {
	if memoryMiB == 0 {
		return 0
	}
	if wholeGiB := (memoryMiB - 1) / mibPerGiB * mibPerGiB; wholeGiB > 0 {
		return wholeGiB
	}
	return memoryMiB - 1
}

// limitUsage is the probed usage measured against the limit: a mount whose size
// is not the limit takes the configured limit as its size, its free space being
// what is left of that limit. An unprobed mount stays zero.
func (limit diskLimit) limitUsage(usage pressure.DiskUsage, limits DiskLimits) pressure.DiskUsage {
	if limit.mountSizeIsLimit || usage.SizeKiB == 0 {
		return usage
	}
	usage.SizeKiB = uint64(limit.current(limits)) * kibPerMiB
	return usage
}

// DetectAndOfferDiskRaise records the disk limits a probe found short of space
// and offers to raise each recorded, unanswered hit, persisting an accepted
// value. usages is the probe's result, or nil at the next launch, which offers
// only recorded hits. A recovered limit clears its marker so a later trip is
// offered again; an answered hit stays closed until then. It returns the
// persisted settings by config key. It never fails the caller: prompt and
// write failures are logged (specs/resources/resource-limit-proposal.md §1e, §3).
func DetectAndOfferDiskRaise(
	k state.Key,
	usages map[pressure.Kind]pressure.DiskUsage,
	markers map[string]state.DiskPressureState,
	limits DiskLimits,
	opts options.RunOptions,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) map[string]string {
	raised := map[string]string{}
	for _, limit := range diskLimits {
		marker, recorded := markers[string(limit.kind)]
		// A limit the probe did not measure (or no probe, at the next launch)
		// reads as zero usage, which neither trips nor recovers.
		usage := limit.limitUsage(usages[limit.kind], limits)
		if pressure.DiskRecovered(usage) {
			if recorded {
				clearDiskPressure(k, limit.kind, marker.SeenAt, ui)
			}
			continue
		}
		// Record through the locked write even when a marker was already read,
		// so the offer uses the marker stored now rather than the stale copy:
		// another session may have recorded and answered a newer hit since the
		// read. RecordDiskPressure keeps an existing marker and returns it, so
		// an answered hit stays closed and an unanswered one stays offerable.
		if pressure.DiskTripped(usage) {
			marker = recordDiskPressure(k, limit.kind, state.DiskPressureState{
				SeenAt:    time.Now(),
				SizeKiB:   usage.SizeKiB,
				AvailKiB:  usage.AvailKiB,
				OfferedAt: time.Time{},
			}, ui)
		}
		if marker.SeenAt.IsZero() || !marker.OfferedAt.IsZero() {
			continue
		}
		if setting := offerDiskRaise(k, limit, marker, limits, opts, paths, ui); setting != "" {
			raised[limit.key] = setting
		}
	}
	return raised
}

// offerDiskRaise offers the raise for one recorded hit and returns the
// persisted setting, or "". A size set by its flag or environment variable, or
// a /tmp already at the memory limit, is explained instead; the explanation
// answers the hit. A non-interactive UI leaves it unanswered for a later run.
func offerDiskRaise(
	k state.Key,
	limit diskLimit,
	marker state.DiskPressureState,
	limits DiskLimits,
	opts options.RunOptions,
	paths configpaths.ConfigPaths,
	ui termio.UI,
) string {
	if !ui.IsInteractive() {
		ui.Verbosef("recorded a full %s; a later interactive run will offer a raise", limit.name)
		return ""
	}
	// A VM can be larger than its configured size (created with a flag, run
	// without it); raising from the configured size could then shrink it.
	currentMiB := max(limit.current(limits), measuredMiB(marker))
	maxMiB, maximumFor := limit.maximum(limits)
	suggestion := Suggestion{
		CurrentMiB:  currentMiB,
		ProposedMiB: SuggestDiskRaise(currentMiB, maxMiB),
		BootMaxMiB:  maxMiB,
	}
	if source, shadowed := ShadowSource(limit.key, opts.SetByFlag(limit.key)); shadowed {
		ui.Warnf("The sandbox's %s is nearly full. It is set by %s, so raise it there (for example to %s).",
			limit.name, source, formatMiB(suggestion.ProposedMiB))
		markDiskPressureOffered(k, limit.kind, marker.SeenAt, ui)
		return ""
	}
	if suggestion.ProposedMiB <= currentMiB {
		ui.Warnf("The sandbox's %s is nearly full and already as large as %s %s; raise that first or free space.",
			limit.name, maximumFor, formatMiB(maxMiB))
		markDiskPressureOffered(k, limit.kind, marker.SeenAt, ui)
		return ""
	}
	newMiB, accepted, err := askRaise(ui, raiseOffer{
		question:   diskQuestion(limit, marker),
		limitName:  limit.name,
		maximumFor: maximumFor,
		suggestion: suggestion,
	})
	if err != nil {
		ui.Verbosef("disk raise prompt failed: %v", err)
		return ""
	}
	markDiskPressureOffered(k, limit.kind, marker.SeenAt, ui)
	if !accepted {
		return ""
	}
	warnIfTheHostLacksTheSpace(limit, newMiB-currentMiB, limits, ui)
	setting := memorySetting(newMiB)
	dir, persistErr := PersistSetting(paths, configSlug(opts, k), k.Slug, limit.key, setting, opts.SetByFlag(limit.key))
	if persistErr != nil {
		ui.Warnf("could not persist the raised %s: %v", limit.name, persistErr)
		return ""
	}
	ui.Infof("Set %s to %s in the config in %s.", limit.key, formatMiB(newMiB), dir)
	return setting
}

// diskQuestion frames the offer: the limit, its free space when the hit was
// seen, and how a raise applies.
func diskQuestion(limit diskLimit, marker state.DiskPressureState) string {
	applies := "Applying it rebuilds the VM; the home volume is kept."
	if limit.appliesLive {
		applies = "It applies without a rebuild."
	}
	//nolint:gosec // G115: KiB counts from df divided to MiB fit uint32
	free, size := uint32(marker.AvailKiB/kibPerMiB), uint32(marker.SizeKiB/kibPerMiB)
	return fmt.Sprintf("The sandbox's %s is nearly full (%s free of %s). Raise it? %s",
		limit.name, formatMiB(free), formatMiB(size), applies)
}

// warnIfTheHostLacksTheSpace warns when a raise lets the limit grow by more than
// the host filesystem it draws on has free. The raise stays: the space is only
// taken as the VM writes it.
func warnIfTheHostLacksTheSpace(limit diskLimit, increaseMiB uint32, limits DiskLimits, ui termio.Logger) {
	if limit.hostFree == nil {
		return
	}
	freeMiB, known, filesystem := limit.hostFree(limits)
	if !known || increaseMiB <= freeMiB {
		return
	}
	ui.Warnf("The raise lets the %s grow by %s, but %s has only %s free.",
		limit.name, formatMiB(increaseMiB), filesystem, formatMiB(freeMiB))
}

// measuredMiB is the hit's probed mount size in MiB.
func measuredMiB(marker state.DiskPressureState) uint32 {
	//nolint:gosec // G115: a df size in MiB fits uint32
	return uint32(marker.SizeKiB / kibPerMiB)
}

// recordDiskPressure records the hit and returns the marker to act on: the
// stored one when another session recorded it first, or the hit itself when
// recording fails.
func recordDiskPressure(
	k state.Key,
	kind pressure.Kind,
	hit state.DiskPressureState,
	ui termio.Logger,
) state.DiskPressureState {
	stored, err := state.RecordDiskPressure(k, string(kind), hit)
	if err != nil {
		ui.Verbosef("recording the full %s failed: %v", kind, err)
		return hit
	}
	return stored
}

func markDiskPressureOffered(k state.Key, kind pressure.Kind, seenAt time.Time, ui termio.Logger) {
	if err := state.MarkDiskPressureOffered(k, string(kind), seenAt, time.Now()); err != nil {
		ui.Verbosef("recording the %s raise answer failed: %v", kind, err)
	}
}

func clearDiskPressure(k state.Key, kind pressure.Kind, seenAt time.Time, ui termio.Logger) {
	if err := state.ClearDiskPressure(k, string(kind), seenAt); err != nil {
		ui.Verbosef("clearing the recovered %s failed: %v", kind, err)
	}
}
