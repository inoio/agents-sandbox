package vm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestReconcileResourceConfigConfigErrorIsNoop pins the read-failure no-op: a
// handle whose Config errors is left untouched, even when it also returns a
// non-nil config (which the `|| cfg == nil` half alone would not catch).
func TestReconcileResourceConfigConfigErrorIsNoop(t *testing.T) {
	handle := reconcileHandle(8, 8192)
	handle.ConfigErr = errors.New("config boom")
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(
		context.Background(), handle, options.RunOptions{CPUs: 8, Memory: "4G"}, &ui,
	); err != nil {
		t.Fatalf("a read failure must be a no-op, got: %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("a Config error must not modify the VM, got %+v", handle.ModifiedOptions)
	}
}

// TestReconcileResourceConfigCPUsOneIsApplied pins the falsy-CPU sentinel: "all
// CPUs" is exactly 0, so a request for 1 CPU is a real change and must reach
// Modify.
func TestReconcileResourceConfigCPUsOneIsApplied(t *testing.T) {
	handle := reconcileHandle(0, 0)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 1}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify for a 1-CPU request, got %d", len(handle.ModifiedOptions))
	}
	if got := handle.ModifiedOptions[0].CPUs; got != 1 {
		t.Errorf("Modify CPUs = %d, want 1", got)
	}
}

// TestReconcileResourceConfigClampsCPUsToBootMaxOfOne pins that a boot maximum
// of 1 is a real clamp (the `> 0` guard must not read the maximum itself as
// "unset") and that the clamp warns.
func TestReconcileResourceConfigClampsCPUsToBootMaxOfOne(t *testing.T) {
	handle := reconcileHandle(1, 0)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 4}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(handle.ModifiedOptions))
	}
	if got := handle.ModifiedOptions[0].CPUs; got != 1 {
		t.Errorf("Modify CPUs = %d, want it clamped to the boot maximum 1", got)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "cpus limited to") }) {
		t.Errorf("expected a CPU clamp warning, got %v", ui.WarnCalls)
	}
}

// TestReconcileResourceConfigNoCPUsWarnAtBootMax pins the boundary: a request
// exactly at the boot maximum is not a clamp, so it must not warn (the value is
// unchanged either way).
func TestReconcileResourceConfigNoCPUsWarnAtBootMax(t *testing.T) {
	handle := reconcileHandle(8, 0)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 8}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if got := handle.ModifiedOptions[0].CPUs; got != 8 {
		t.Errorf("Modify CPUs = %d, want 8", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("a request at the boot maximum must not warn, got %v", ui.WarnCalls)
	}
}

// TestReconcileResourceConfigClampsMemoryToBootMaxOfOne pins that a memory boot
// maximum of 1 MiB is a real clamp and that the clamp warns.
func TestReconcileResourceConfigClampsMemoryToBootMaxOfOne(t *testing.T) {
	handle := reconcileHandle(0, 1)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(handle.ModifiedOptions))
	}
	if got := handle.ModifiedOptions[0].MemoryMiB; got != 1 {
		t.Errorf("Modify MemoryMiB = %d, want it clamped to the boot maximum 1", got)
	}
	if !slices.ContainsFunc(ui.WarnCalls, func(s string) bool { return strings.Contains(s, "memory limited to") }) {
		t.Errorf("expected a memory clamp warning, got %v", ui.WarnCalls)
	}
}

// TestReconcileResourceConfigNoMemoryWarnAtBootMax pins the memory boundary: a
// request exactly at the boot maximum is not a clamp, so it must not warn.
func TestReconcileResourceConfigNoMemoryWarnAtBootMax(t *testing.T) {
	handle := reconcileHandle(0, 4096)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if got := handle.ModifiedOptions[0].MemoryMiB; got != 4096 {
		t.Errorf("Modify MemoryMiB = %d, want 4096", got)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("a request at the boot maximum must not warn, got %v", ui.WarnCalls)
	}
}

// TestReconcileResourceConfigMemoryOnlyChangeApplies pins the no-op condition:
// a memory-only change (CPUs unset) must still reach Modify, so the guard
// cannot collapse to "CPUs is zero".
func TestReconcileResourceConfigMemoryOnlyChangeApplies(t *testing.T) {
	handle := reconcileHandle(8, 8192)
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify for a memory-only change, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.MemoryMiB != 4096 || mo.CPUs != 0 {
		t.Errorf("Modify options = %+v, want MemoryMiB=4096 and no CPU change", mo)
	}
}

// TestReconcileResourceConfigModifyErrorWrapsCause pins the Modify-failure wrap:
// the SDK error's cause is preserved.
func TestReconcileResourceConfigModifyErrorWrapsCause(t *testing.T) {
	handle := reconcileHandle(8, 8192)
	sentinel := errors.New("modify boom")
	handle.ModifyErr = sentinel
	ui := termio.NewTestMock(t)

	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 4}, &ui)
	if err == nil {
		t.Fatal("expected a Modify failure to abort")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the Modify cause", err)
	}
}

// TestReconcileResourceConfigClampsCpusToMax verifies that CPU/memory requests
// above the boot-time maximum are clamped (not rejected).
func TestReconcileResourceConfigClampsCpusToMax(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 4096, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ui := termio.NewTestMock(t)
	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 16, Memory: "4G"}, &ui)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 8 {
		t.Errorf("expected clamped CPUs=8, got %d", mo.CPUs)
	}
	if mo.MemoryMiB != 0 {
		t.Errorf("expected no memory change (already 4G), got %d", mo.MemoryMiB)
	}
}

// applySandboxOpts applies captured functional options to a fresh SandboxConfig
// so tests can assert what createProjectVM configured.
func TestReconcileResourceConfigAppliesCPUsAndMemory(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MemoryMiB: 2048},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ctx := context.Background()
	ui := termio.NewTestMock(t)
	if err := reconcileResourceConfig(ctx, handle, options.RunOptions{CPUs: 8, Memory: "4G"}, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig failed: %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 8 || mo.MemoryMiB != 4096 {
		t.Errorf("ModifyOptions = CPUs=%d Mem=%d, want 8 / 4096", mo.CPUs, mo.MemoryMiB)
	}
}

func TestReconcileResourceConfigNoopWhenSame(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 8, MemoryMiB: 4096},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
	ui := termio.NewTestMock(t)
	if err := reconcileResourceConfig(
		context.Background(),
		handle,
		options.RunOptions{CPUs: 8, Memory: "4G"},
		&ui,
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call, got %d", len(handle.ModifiedOptions))
	}
}

func TestReconcileResourceConfig_AppliesCPUsAndMemory(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{},
	}
	ui := termio.NewTestMock(t)

	opts := options.RunOptions{CPUs: 4, Memory: "4G"}
	if err := reconcileResourceConfig(context.Background(), handle, opts, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 4 || mo.MemoryMiB != 4096 {
		t.Errorf("Modify options = %+v, want CPUs=4 MemoryMiB=4096", mo)
	}
	if mo.Policy != msbSdk.ModificationPolicyNoRestart {
		t.Errorf("Modify policy = %v, want NoRestart", mo.Policy)
	}
}

func TestReconcileResourceConfig_ClampsToBootMax(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg:  &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 4, MemoryMiB: 2048, MaxMemoryMiB: 4096},
		Plan: &msbSdk.SandboxModificationPlan{},
	}
	ui := termio.NewTestMock(t)

	opts := options.RunOptions{CPUs: 16, Memory: "16G"}
	if err := reconcileResourceConfig(context.Background(), handle, opts, &ui); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 1 {
		t.Fatalf("expected 1 Modify call, got %d", len(handle.ModifiedOptions))
	}
	mo := handle.ModifiedOptions[0]
	if mo.CPUs != 4 || mo.MemoryMiB != 4096 {
		t.Errorf("Modify options = %+v, want clamped CPUs=4 MemoryMiB=4096", mo)
	}
}

func TestReconcileResourceConfig_NoChangeSkipsModify(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
	}
	ui := termio.NewTestMock(t)

	if err := reconcileResourceConfig(
		context.Background(),
		handle,
		options.RunOptions{CPUs: 2, Memory: "2048"},
		&ui,
	); err != nil {
		t.Fatalf("reconcileResourceConfig() error = %v", err)
	}
	if len(handle.ModifiedOptions) != 0 {
		t.Errorf("expected no Modify call when nothing changes, got %d", len(handle.ModifiedOptions))
	}
}

func TestReconcileResourceConfig_ConflictsError(t *testing.T) {
	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{CPUs: 2, MaxCPUs: 8, MemoryMiB: 2048, MaxMemoryMiB: 8192},
		Plan: &msbSdk.SandboxModificationPlan{
			Conflicts: []msbSdk.ModificationConflict{{Field: "cpus", Message: "conflict"}},
		},
	}
	ui := termio.NewTestMock(t)

	err := reconcileResourceConfig(context.Background(), handle, options.RunOptions{CPUs: 4}, &ui)
	if err == nil {
		t.Fatal("expected error when plan has conflicts")
	}
}

func TestSummarizeConflicts(t *testing.T) {
	cs := []msbSdk.ModificationConflict{
		{Field: "cpus", Message: "too high"},
		{Field: "memory", Message: "not enough"},
	}
	if got := summarizeConflicts(cs); got != "cpus: too high; memory: not enough" {
		t.Errorf("summarizeConflicts() = %q, want joined string", got)
	}
	if got := summarizeConflicts(nil); got != "" {
		t.Errorf("summarizeConflicts(nil) = %q, want empty", got)
	}
}

// reconcileHandle builds a handle whose boot config reports 2 CPUs and 2048 MiB,
// the given CPU and memory boot maxima, and a plan that applies cleanly. The
// boot values stay below every requested count, so a change always registers
// unless a test intends none.
func reconcileHandle(maxCPUs uint8, maxMemoryMiB uint32) *msb.MockSandboxHandle {
	return &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			CPUs:         2,
			MaxCPUs:      maxCPUs,
			MemoryMiB:    2048,
			MaxMemoryMiB: maxMemoryMiB,
		},
		Plan: &msbSdk.SandboxModificationPlan{Applied: true},
	}
}
