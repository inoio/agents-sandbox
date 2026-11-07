//go:build msb

package msb

import (
	"context"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// TestRealDiskProbeAndRootGrow proves the disk contract the launcher relies on
// against the real runtime: the in-guest probe reports the root disk, the
// /workspace write budget and /tmp at their configured limits, a filled /tmp
// trips the threshold, and a larger RootDiskSizeMiB applies live and persists
// (specs/resources/resource-limit-proposal.md §1e, §5).
func TestRealDiskProbeAndRootGrow(t *testing.T) {
	ensureIsolatedRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	name := "agents-sandbox-msb-test-disk-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sb, err := msbSdk.CreateSandbox(ctx, name,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(testIdleTimeout),
		msbSdk.WithRootDisk(msbSdk.RootDisk.Managed(1024)),
		msbSdk.WithMounts(map[string]msbSdk.MountConfig{
			"/workspace": msbSdk.Mount.Bind(t.TempDir(), msbSdk.MountOptions{QuotaMiB: 64}),
			"/tmp":       msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: 64}),
		}),
	)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), destroyTimeout)
		defer cleanupCancel()
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, name); handleErr == nil {
			_ = handle.Destroy(cleanupCtx)
		}
	})
	guest := realSandbox{sandbox: sb}
	probe := func() map[pressure.Kind]pressure.DiskUsage {
		t.Helper()
		command, args := pressure.DiskProbeCommand()
		result, execErr := guest.Exec(ctx, command, args)
		if execErr != nil {
			t.Fatalf("disk probe: %v", execErr)
		}
		return pressure.ParseDiskUsage(result.Stdout())
	}

	usages := probe()
	if len(usages) != 3 {
		t.Fatalf("probe = %+v, want the root disk, /workspace and /tmp", usages)
	}
	if usages[pressure.KindWorkspaceQuota].SizeKiB != 64<<10 || usages[pressure.KindTmp].SizeKiB != 64<<10 {
		t.Errorf("probe = %+v, want /workspace and /tmp at their 64 MiB limits", usages)
	}
	if root := usages[pressure.KindRootDisk].SizeKiB; root < 800<<10 || root > 1024<<10 {
		t.Errorf("root disk = %d KiB, want close to the 1 GiB managed root", root)
	}

	if _, fillErr := guest.Shell(
		ctx,
		"dd if=/dev/zero of=/tmp/fill bs=1M count=100 2>/dev/null; true",
	); fillErr != nil {
		t.Fatalf("fill /tmp: %v", fillErr)
	}
	if tmp := probe()[pressure.KindTmp]; !pressure.DiskTripped(tmp) {
		t.Errorf("/tmp after filling = %+v, want the threshold tripped", tmp)
	}

	handle, err := msbSdk.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	plan, err := realSandboxHandle{handle: handle}.Modify(ctx, msbSdk.ModifyOptions{
		RootDiskSizeMiB: 2048,
		Policy:          msbSdk.ModificationPolicyNoRestart,
	})
	if err != nil || !plan.Applied || len(plan.Conflicts) != 0 {
		t.Fatalf("grow root disk: plan=%+v err=%v, want a live, conflict-free apply", plan, err)
	}
	if root := probe()[pressure.KindRootDisk].SizeKiB; root < 1800<<10 {
		t.Errorf("root disk after the grow = %d KiB, want close to 2 GiB", root)
	}
	fresh, err := msbSdk.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("GetSandbox after grow: %v", err)
	}
	if cfg, cfgErr := fresh.Config(); cfgErr != nil || cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != 2048 {
		t.Errorf("persisted root disk = %+v (err %v), want 2048 MiB", cfg, cfgErr)
	}
}
