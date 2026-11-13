package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// memoryConvergeInterval and memoryConvergeTimeout bound the poll for a live
// memory resize to converge. They are variables so tests can shrink them.
var (
	memoryConvergeInterval = 200 * time.Millisecond
	memoryConvergeTimeout  = 10 * time.Second
)

// errMemoryLimitNotApplied marks a live memory request the runtime did not
// apply. The runtime never unplugs memory, so a decrease never converges; a raise
// that does not reach the requested limit before the deadline does not either.
var errMemoryLimitNotApplied = errors.New("not applied")

// errMemoryLimitUnknown marks a metrics read that returned no usable live limit —
// a nil snapshot or a zero limit. The limit is unknown, not zero: it neither
// satisfies a decrease nor confirms a raise, so it is reported like a failed read.
var errMemoryLimitUnknown = errors.New("the live memory limit is unknown")

// waitForMemoryLimit confirms the live memory limit matches wantMiB.
//
// The live limit is the reference for direction: the saved config can diverge
// from the guest (a decrease the runtime never applied leaves the saved value
// below the live limit), so it cannot say whether the request is a raise or a
// decrease. A live limit above the request is a decrease the runtime never
// applies, reported not applied from the first read; a live limit at the request
// is applied; a live limit below it is a raise, polled until the limit reaches
// the request. Every read runs under the convergence deadline, so a hanging
// metrics call cannot block the launch. A read error or an unknown limit (a nil
// snapshot or a zero limit) is terminal and reported as unconfirmed, never as
// not applied: Modify returned a conflict-free plan, so the change was planned
// and only its confirmation failed.
func waitForMemoryLimit(
	ctx context.Context,
	read func(context.Context) (*msb.Metrics, error),
	wantMiB uint32,
) error {
	ctx, cancel := context.WithTimeout(ctx, memoryConvergeTimeout)
	defer cancel()
	wantBytes := uint64(wantMiB) << 20
	metrics, err := read(ctx)
	if err != nil {
		return fmt.Errorf("read VM metrics: %w", err)
	}
	if metrics == nil || metrics.MemoryLimitBytes == 0 {
		return errMemoryLimitUnknown
	}
	if metrics.MemoryLimitBytes > wantBytes {
		return fmt.Errorf(
			"%w: the runtime does not unplug memory, so a decrease to %d MiB never takes effect",
			errMemoryLimitNotApplied, wantMiB,
		)
	}
	if metrics.MemoryLimitBytes == wantBytes {
		return nil
	}
	return pollMemoryRaise(ctx, read, wantMiB, wantBytes)
}

// pollMemoryRaise polls read until the live limit reaches at least wantBytes, the
// deadline passes (not applied), or a read fails or reports an unknown limit
// (unconfirmed). It runs only after the first read showed the limit below the
// request, so a limit that reaches or exceeds the request is the raise applied
// (another client may push it above the request as it converges).
func pollMemoryRaise(
	ctx context.Context,
	read func(context.Context) (*msb.Metrics, error),
	wantMiB uint32,
	wantBytes uint64,
) error {
	for {
		metrics, err := read(ctx)
		if err != nil {
			return fmt.Errorf("read VM metrics: %w", err)
		}
		if metrics == nil || metrics.MemoryLimitBytes == 0 {
			return errMemoryLimitUnknown
		}
		if metrics.MemoryLimitBytes >= wantBytes {
			return nil
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf(
					"%w: the live limit did not reach %d MiB before the deadline",
					errMemoryLimitNotApplied, wantMiB,
				)
			}
			return ctx.Err()
		case <-time.After(memoryConvergeInterval):
		}
	}
}

// reconcileResourceConfig applies requested CPUs/memory changes to an existing
// (reused) VM via the SDK Modify API. Every memory request is then confirmed
// against the live limit, whether or not it staged a change: the saved config can
// diverge from the guest, so a request that matches the config can still be
// unapplied. The live limit decides the direction (see waitForMemoryLimit); an
// unknown or unreadable live limit is reported as unconfirmed, not as not applied.
//
//nolint:nilerr // Returning nil when config read fails is intentional — treat as no-op
func reconcileResourceConfig(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	ui termio.Logger,
) error {
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		return nil
	}
	mo, wantMiB := buildModifyOptions(cfg, opts, ui)
	if mo.CPUs != 0 || mo.MemoryMiB != 0 || mo.RootDiskSizeMiB != 0 {
		mo.Policy = msb.ModificationPolicyNoRestart
		plan, err := handle.Modify(ctx, mo)
		if err != nil {
			return fmt.Errorf("modify VM resources: %w", err)
		}
		if len(plan.Conflicts) > 0 {
			return fmt.Errorf("resource change not applied: %s", summarizeConflicts(plan.Conflicts))
		}
	}
	if wantMiB != 0 {
		if err := waitForMemoryLimit(ctx, handle.Metrics, wantMiB); err != nil {
			if errors.Is(err, errMemoryLimitNotApplied) {
				return fmt.Errorf("live memory change to %d MiB: %w", wantMiB, err)
			}
			return fmt.Errorf("could not confirm the live memory change to %d MiB: %w", wantMiB, err)
		}
	}
	return nil
}

// buildModifyOptions derives the live resource change for opts against the
// boot config, clamping each requested value to its boot maximum with a
// warning. A zero CPUs means "all CPUs" (left unchanged); a zero MemoryMiB
// means "unchanged". A root disk grows to a larger requested size; a smaller
// one is a recreate (reconfig.PlanReconfig), never staged here.
//
// The returned uint32 is the resolved memory request in MiB (0 when none). It is
// returned even when it equals the boot limit, so the caller can confirm it
// against the live limit: the saved config can diverge from the guest.
func buildModifyOptions(cfg *msb.SandboxConfig, opts options.RunOptions, ui termio.Logger) (msb.ModifyOptions, uint32) {
	var mo msb.ModifyOptions
	var wantMiB uint32
	if opts.CPUs != 0 {
		want := opts.CPUs
		if cfg.MaxCPUs > 0 && want > cfg.MaxCPUs {
			ui.Warnf("cpus limited to %d (boot maximum); requested %d", cfg.MaxCPUs, opts.CPUs)
			want = cfg.MaxCPUs
		}
		if want != cfg.CPUs {
			mo.CPUs = want
		}
	}
	if opts.Memory != "" {
		want := options.ParseMemory(opts.Memory)
		if cfg.MaxMemoryMiB > 0 && want > cfg.MaxMemoryMiB {
			ui.Warnf("memory limited to %d MiB (boot maximum); requested %d MiB", cfg.MaxMemoryMiB, want)
			want = cfg.MaxMemoryMiB
		}
		wantMiB = want
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	// An unset or unparseable disk size reads as 0, which never grows the disk.
	if want, _ := options.ParseMemoryOK(opts.DiskSize); cfg.RootDisk != nil && want > cfg.RootDisk.SizeMiB {
		mo.RootDiskSizeMiB = want
	}
	return mo, wantMiB
}

func summarizeConflicts(cs []msb.ModificationConflict) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Field+": "+c.Message)
	}
	return strings.Join(b, "; ")
}
