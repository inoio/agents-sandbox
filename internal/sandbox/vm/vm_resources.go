package vm

import (
	"context"
	"fmt"
	"strings"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

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

// waitForMemoryLimit polls read until the live memory limit reaches wantMiB or
// the bounded deadline elapses. A live resize is asynchronous — Modify returns
// before the limit catches up — so a single read never proves convergence. A
// read error is terminal; hitting the deadline reports the resize as
// unconverged.
func waitForMemoryLimit(
	ctx context.Context,
	read func(context.Context) (*msbSdk.Metrics, error),
	wantMiB uint32,
) error {
	ctx, cancel := context.WithTimeout(ctx, memoryConvergeTimeout)
	defer cancel()
	wantBytes := uint64(wantMiB) << 20
	for {
		metrics, err := read(ctx)
		if err != nil {
			return err
		}
		if metrics != nil && metrics.MemoryLimitBytes >= wantBytes {
			return nil
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("memory resize to %d MiB did not converge", wantMiB)
			}
			return ctx.Err()
		case <-time.After(memoryConvergeInterval):
		}
	}
}

// reconcileResourceConfig applies requested CPUs/memory changes to an existing
// (reused) VM via the SDK Modify API. A staged memory change is then waited on
// until the live limit converges, because a live resize is asynchronous.
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
	mo := buildModifyOptions(cfg, opts, ui)
	if mo.CPUs == 0 && mo.MemoryMiB == 0 && mo.RootDiskSizeMiB == 0 {
		return nil
	}
	mo.Policy = msbSdk.ModificationPolicyNoRestart
	plan, err := handle.Modify(ctx, mo)
	if err != nil {
		return fmt.Errorf("modify VM resources: %w", err)
	}
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("resource change not applied: %s", summarizeConflicts(plan.Conflicts))
	}
	if mo.MemoryMiB != 0 {
		if err := waitForMemoryLimit(ctx, handle.Metrics, mo.MemoryMiB); err != nil {
			return fmt.Errorf("wait for live memory resize: %w", err)
		}
	}
	return nil
}

// buildModifyOptions derives the live resource change for opts against the
// boot config, clamping each requested value to its boot maximum with a
// warning. A zero CPUs means "all CPUs" (left unchanged); a zero MemoryMiB
// means "unchanged". A root disk grows to a larger requested size; a smaller
// one is a recreate (reconfig.PlanReconfig), never staged here.
func buildModifyOptions(cfg *msbSdk.SandboxConfig, opts options.RunOptions, ui termio.Logger) msbSdk.ModifyOptions {
	var mo msbSdk.ModifyOptions
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
		if want != cfg.MemoryMiB {
			mo.MemoryMiB = want
		}
	}
	// An unset or unparseable disk size reads as 0, which never grows the disk.
	if want, _ := options.ParseMemoryOK(opts.DiskSize); cfg.RootDisk != nil && want > cfg.RootDisk.SizeMiB {
		mo.RootDiskSizeMiB = want
	}
	return mo
}

func summarizeConflicts(cs []msbSdk.ModificationConflict) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Field+": "+c.Message)
	}
	return strings.Join(b, "; ")
}
