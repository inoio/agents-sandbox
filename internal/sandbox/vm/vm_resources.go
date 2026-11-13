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

// errMemoryLimitNotApplied marks a staged live memory change the runtime did not
// apply. The runtime never unplugs memory, so a decrease never converges; a raise
// that does not reach the requested limit before the deadline does not either.
var errMemoryLimitNotApplied = errors.New("not applied")

// errMemoryLimitUnknown marks a metrics read that returned no usable live limit —
// a nil snapshot or a zero limit. The limit is unknown, not zero: it neither
// satisfies a decrease nor confirms a raise, so it is reported like a failed read.
var errMemoryLimitUnknown = errors.New("the live memory limit is unknown")

// waitForMemoryLimit confirms a staged live memory change against read.
//
// A live resize is asynchronous — Modify returns before the limit catches up —
// so a raise polls until the live limit reaches wantMiB; a live limit already at
// or above wantMiB satisfies it (another client may hold a larger limit, which is
// still at least what was asked). A decrease never converges — the runtime never
// unplugs memory — so while the live limit is still above the request it is
// reported not applied from the first read instead of polling to the deadline.
// Every read runs under the convergence deadline, so a hanging metrics call
// cannot block the launch. A read error or an unknown limit (a nil snapshot or a
// zero limit) is terminal and reported as unconfirmed, never as not applied:
// Modify returned a conflict-free plan, so the change was planned and only its
// confirmation failed.
func waitForMemoryLimit(
	ctx context.Context,
	read func(context.Context) (*msb.Metrics, error),
	wantMiB uint32,
	raising bool,
) error {
	ctx, cancel := context.WithTimeout(ctx, memoryConvergeTimeout)
	defer cancel()
	wantBytes := uint64(wantMiB) << 20
	if !raising {
		return confirmMemoryDecrease(ctx, read, wantMiB, wantBytes)
	}
	return pollMemoryRaise(ctx, read, wantMiB, wantBytes)
}

// confirmMemoryDecrease reports a decrease the runtime will not apply. One read
// suffices: a live limit already at or below the request is applied, any larger
// limit is not applied, and an unknown limit or a failed read is unconfirmed.
func confirmMemoryDecrease(
	ctx context.Context,
	read func(context.Context) (*msb.Metrics, error),
	wantMiB uint32,
	wantBytes uint64,
) error {
	metrics, err := read(ctx)
	if err != nil {
		return fmt.Errorf("read VM metrics: %w", err)
	}
	if metrics == nil || metrics.MemoryLimitBytes == 0 {
		return errMemoryLimitUnknown
	}
	if metrics.MemoryLimitBytes <= wantBytes {
		return nil
	}
	return fmt.Errorf(
		"%w: the runtime does not unplug memory, so a decrease to %d MiB never takes effect",
		errMemoryLimitNotApplied, wantMiB,
	)
}

// pollMemoryRaise polls read until the live limit reaches at least wantBytes, the
// deadline passes (not applied), or a read fails or reports an unknown limit
// (unconfirmed).
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
// (reused) VM via the SDK Modify API. A staged memory change is then confirmed
// against the live limit: a raise is satisfied once the live limit reaches the
// request, and a decrease is reported not applied while the live limit stays
// above it (the runtime never unplugs memory). An unknown or unreadable live
// limit is reported as unconfirmed, not as not applied.
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
	mo, memoryRaise := buildModifyOptions(cfg, opts, ui)
	if mo.CPUs == 0 && mo.MemoryMiB == 0 && mo.RootDiskSizeMiB == 0 {
		return nil
	}
	mo.Policy = msb.ModificationPolicyNoRestart
	plan, err := handle.Modify(ctx, mo)
	if err != nil {
		return fmt.Errorf("modify VM resources: %w", err)
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("resource change not applied: %s", summarizeConflicts(plan.Conflicts))
	}
	if mo.MemoryMiB != 0 {
		if err := waitForMemoryLimit(ctx, handle.Metrics, mo.MemoryMiB, memoryRaise); err != nil {
			if errors.Is(err, errMemoryLimitNotApplied) {
				return fmt.Errorf("live memory change to %d MiB: %w", mo.MemoryMiB, err)
			}
			return fmt.Errorf("could not confirm the live memory change to %d MiB: %w", mo.MemoryMiB, err)
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
// The returned bool reports whether the requested memory is a raise above the
// boot limit (the runtime applies a raise live but never unplugs memory, so a
// decrease can never converge).
func buildModifyOptions(cfg *msb.SandboxConfig, opts options.RunOptions, ui termio.Logger) (msb.ModifyOptions, bool) {
	var mo msb.ModifyOptions
	memoryRaise := false
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
		memoryRaise = want > cfg.MemoryMiB
		if cfg.MaxMemoryMiB > 0 && want > cfg.MaxMemoryMiB {
			ui.Warnf("memory limited to %d MiB (boot maximum); requested %d MiB", cfg.MaxMemoryMiB, want)
			want = cfg.MaxMemoryMiB
		}
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	// An unset or unparseable disk size reads as 0, which never grows the disk.
	if want, _ := options.ParseMemoryOK(opts.DiskSize); cfg.RootDisk != nil && want > cfg.RootDisk.SizeMiB {
		mo.RootDiskSizeMiB = want
	}
	return mo, memoryRaise
}

func summarizeConflicts(cs []msb.ModificationConflict) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Field+": "+c.Message)
	}
	return strings.Join(b, "; ")
}
