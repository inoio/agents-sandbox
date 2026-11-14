package session

import (
	"context"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/resourceproposal"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// abnormalEnd reports whether an attach ended abnormally: a signal death (the
// guest reports exit code -1 for any signal) or a non-nil attach error
// (specs/resources/resource-limit-proposal.md).
func abnormalEnd(attachErr error, exitCode int) bool {
	return attachErr != nil || exitCode == -1
}

// bootMaxMemoryMiB and hostAvailableMemoryMiB are the host-RAM reads the offer
// bounds come from. They are vars so a test can pin a host, rather than assert
// against the installed RAM and /proc/meminfo of the machine running the suite
// (the boot maximum is the host's whole-GiB RAM, so the raise the offer can
// suggest depends on it).
var (
	bootMaxMemoryMiB       = sandbox.BootMaxMemoryMiB
	hostAvailableMemoryMiB = sysinfo.AvailableMemoryMiB
)

// offerMemoryRaiseAfterAttach detects a memory hit from the just-ended session
// and offers a raise. It runs inside the attach closure so the VM is still
// alive and the client lease not yet released/reaped. Best-effort: every error
// is swallowed by resourceproposal.DetectAndOfferMemoryRaise.
func offerMemoryRaiseAfterAttach(
	ctx context.Context,
	k state.Key,
	sb msb.SandboxObserver,
	summary pressureSummary,
	opts options.RunOptions,
	ui termio.UI,
) {
	var marker *state.ResourcePressureState
	if hs, err := state.ReadState(k); err != nil {
		ui.Verbosef("reading home state for memory detection failed: %v (continuing)", err)
	} else {
		marker = hs.ResourcePressure
	}
	hostAvailableMiB, hostAvailableKnown := hostAvailableMemoryMiB()
	raised := resourceproposal.DetectAndOfferMemoryRaise(
		ctx, k, sb, marker,
		resourceproposal.MemoryLimits{
			CurrentMiB:         0,
			BootMaxMiB:         bootMaxMemoryMiB(),
			HostAvailableMiB:   hostAvailableMiB,
			HostAvailableKnown: hostAvailableKnown,
		},
		summary.Tripped, opts, configpaths.Get(), ui,
	)
	if raised != "" {
		ui.Infof("The raised memory limit applies on your next run.")
	}
}

// diskProbeTimeout bounds the in-guest df, so a wedged guest cannot hang the
// end of a session.
const diskProbeTimeout = 10 * time.Second

// offerDiskRaiseAfterAttach probes the VM's disk limits with an in-guest df and
// offers to raise any that ran short. It runs after every attach end except a
// signal, since disk pressure rarely ends a session. Best-effort: a failed probe
// or state read is logged at verbose and offers nothing.
func offerDiskRaiseAfterAttach(
	ctx context.Context,
	k state.Key,
	sb msb.SandboxExecutor,
	opts options.RunOptions,
	ui termio.UI,
) {
	probeCtx, cancel := context.WithTimeout(ctx, diskProbeTimeout)
	defer cancel()
	command, args := pressure.DiskProbeCommand()
	result, err := sb.Exec(probeCtx, command, args, msb.ExecRequest{})
	if err != nil {
		ui.Verbosef("disk probe failed: %v (continuing)", err)
		return
	}
	var markers map[string]state.DiskPressureState
	if hs, readErr := state.ReadState(k); readErr != nil {
		ui.Verbosef("reading home state for disk detection failed: %v (continuing)", readErr)
	} else {
		markers = hs.DiskPressure
	}
	raised := resourceproposal.DetectAndOfferDiskRaise(
		k,
		pressure.ParseDiskUsage(result.Stdout()),
		markers,
		resourceproposal.WithHostFreeSpace(resourceproposal.RunDiskLimits(opts)),
		opts,
		configpaths.Get(),
		ui,
	)
	if len(raised) > 0 {
		ui.Infof("The raised disk limits apply on your next run.")
	}
}
