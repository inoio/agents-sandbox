//go:build msb

// This test lives in the external msb_test package on purpose: it needs the
// pressure package, which in turn imports msb for the launcher's observation
// value types. As an internal (package msb) test it would put msb and pressure
// in an import cycle; as an external test it does not.
package msb_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
)

// diskProbeDestroyTimeout and diskProbeIdleTimeout mirror the msb package's
// test isolation constants; this external test cannot see them.
const (
	diskProbeDestroyTimeout = 30 * time.Second
	diskProbeIdleTimeout    = 5 * time.Minute
)

// ensureDiskProbeRuntime resolves the SDK-pinned runtime in the isolated home
// TestMain installed, installing it on first use. It must run before the first
// real SDK call.
func ensureDiskProbeRuntime(t *testing.T) {
	t.Helper()
	if _, err := msbSdk.EnsureRuntime(
		context.Background(),
		msbSdk.RuntimeConfig{Home: os.Getenv("MSB_HOME")},
		msbSdk.InstallOptions{},
	); err != nil {
		t.Fatalf("ensure isolated msb runtime: %v", err)
	}
}

// probeDiskUsage runs the launcher's in-guest disk probe against sb and parses
// its output.
func probeDiskUsage(t *testing.T, ctx context.Context, sb *msbSdk.Sandbox) map[pressure.Kind]pressure.DiskUsage {
	t.Helper()
	command, args := pressure.DiskProbeCommand()
	result, err := sb.Exec(ctx, command, args)
	if err != nil {
		t.Fatalf("disk probe: %v", err)
	}
	return pressure.ParseDiskUsage(result.Stdout())
}

// assertConfiguredDiskLimits checks the probe reports the root disk and the
// /workspace and /tmp mounts at their configured limits.
func assertConfiguredDiskLimits(t *testing.T, usages map[pressure.Kind]pressure.DiskUsage) {
	t.Helper()
	if len(usages) != 3 {
		t.Fatalf("probe = %+v, want the root disk, /workspace and /tmp", usages)
	}
	if usages[pressure.KindWorkspaceQuota].SizeKiB != 64<<10 || usages[pressure.KindTmp].SizeKiB != 64<<10 {
		t.Errorf("probe = %+v, want /workspace and /tmp at their 64 MiB limits", usages)
	}
	if root := usages[pressure.KindRootDisk].SizeKiB; root < 800<<10 || root > 1024<<10 {
		t.Errorf("root disk = %d KiB, want close to the 1 GiB managed root", root)
	}
}

// TestRealDiskProbeAndRootGrow proves the disk contract the launcher relies on
// against the real runtime: the in-guest probe reports the root disk, the
// /workspace write budget and /tmp at their configured limits, a filled /tmp
// trips the threshold, and a larger RootDiskSizeMiB applies live and persists
// (specs/resources/resource-limit-proposal.md §1e, §5).
func TestRealDiskProbeAndRootGrow(t *testing.T) {
	ensureDiskProbeRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	name := "agents-sandbox-msb-test-disk-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sb, err := msbSdk.CreateSandbox(ctx, name,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithIdleTimeout(diskProbeIdleTimeout),
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
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), diskProbeDestroyTimeout)
		defer cleanupCancel()
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, name); handleErr == nil {
			_ = handle.Destroy(cleanupCtx)
		}
	})

	assertConfiguredDiskLimits(t, probeDiskUsage(t, ctx, sb))

	if _, fillErr := sb.Shell(
		ctx,
		"dd if=/dev/zero of=/tmp/fill bs=1M count=100 2>/dev/null; true",
	); fillErr != nil {
		t.Fatalf("fill /tmp: %v", fillErr)
	}
	if tmp := probeDiskUsage(t, ctx, sb)[pressure.KindTmp]; !pressure.DiskTripped(tmp) {
		t.Errorf("/tmp after filling = %+v, want the threshold tripped", tmp)
	}

	growRootDiskAndVerify(t, ctx, name, sb)
}

// growRootDiskAndVerify applies a live root-disk grow, proves the probe sees the
// larger disk, and proves the new size persists in the sandbox config.
func growRootDiskAndVerify(t *testing.T, ctx context.Context, name string, sb *msbSdk.Sandbox) {
	t.Helper()
	handle, err := msbSdk.GetSandbox(ctx, name)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	plan, err := handle.Modify(ctx, msbSdk.ModifyOptions{
		RootDiskSizeMiB: 2048,
		Policy:          msbSdk.ModificationPolicyNoRestart,
	})
	if err != nil || !plan.Applied || len(plan.Conflicts) != 0 {
		t.Fatalf("grow root disk: plan=%+v err=%v, want a live, conflict-free apply", plan, err)
	}
	if root := probeDiskUsage(t, ctx, sb)[pressure.KindRootDisk].SizeKiB; root < 1800<<10 {
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
