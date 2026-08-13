package session

import (
	"context"
	"fmt"
	"strings"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// reconcileResourceConfig applies requested CPUs/memory changes to an existing
// (reused) VM via the SDK Modify API. Requests above the boot-time maximum are
// clamped to that maximum with a warning. Changes within the maximum apply live
// (policy NoRestart); CPU = 0 means "all CPUs" (left unchanged).
//
//nolint:nilerr // Returning nil when config read fails is intentional — treat as no-op
func reconcileResourceConfig(
	ctx context.Context,
	handle msb.SandboxHandle,
	opts options.RunOptions,
	ui termio.UI,
) error {
	cfg, err := handle.Config()
	if err != nil || cfg == nil {
		return nil
	}
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
	if mo.CPUs == 0 && mo.MemoryMiB == 0 {
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
	return nil
}

func summarizeConflicts(cs []msbSdk.ModificationConflict) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Field+": "+c.Message)
	}
	return strings.Join(b, "; ")
}
