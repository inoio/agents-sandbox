package session

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/resourceproposal"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sysinfo"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// abnormalEnd reports whether an attach ended abnormally: a signal death (the
// guest reports exit code -1 for any signal) or a non-nil attach error
// (specs/resource-limit-proposal.md §1b).
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
	sb msb.Sandbox,
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
