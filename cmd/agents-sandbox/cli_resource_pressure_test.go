package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// memoryPressureSandbox is a mock project VM whose attach ends abnormally
// (signal death) with a guest OOM kill in the system log and metrics naming a
// 4 GiB limit, so the post-attach detection attributes the crash to memory.
func memoryPressureSandbox() *msbtest.MockSandbox {
	return &msbtest.MockSandbox{
		AttachCode: -1,
		LogsResult: []sandboxmsb.LogEntry{{
			Source:    sandboxmsb.LogSourceSystem,
			Timestamp: time.Now(),
			Data:      []byte("Out of memory: Killed process 42 (opencode) total-vm:1kB"),
		}},
		MetricsResult: &sandboxmsb.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}
}

// withoutMemoryEnv clears the memory env vars for the duration of the test, so
// PersistSetting's shadow guard does not refuse the write.
func withoutMemoryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
}

// resolvedMemory re-reads the project's effective memory through the resolver
// chain, so the assertion does not depend on guessing the owning config file's
// name or path.
func resolvedMemory(t *testing.T, slug string) string {
	t.Helper()
	r, err := viperconfig.NewResolver(nil, configpaths.Get(), slug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r.Config().Memory
}

// effectiveConfigSlugForTest resolves the config slug the CLI's run path uses
// (effectiveConfigSlug of the cwd identity), so a persist is read back through
// the same slug the resolver reads.
func effectiveConfigSlugForTest(t *testing.T, identity git.Identity) string {
	t.Helper()
	return effectiveConfigSlug(identity)
}

// TestResourcePressureInteractiveAcceptPersists drives `run` end to end through
// the real command/compose path with mocked seams: an abnormal attach with an
// OOM line prompts for a memory raise once, and an accepted value is persisted
// to the config slug the resolver reads (the repo slug, not the state/checkout
// slug), so the next run picks it up. The abnormal end is expected to surface as
// an ExitError code -1 (the guest's signal death), not a clean nil.
func TestResourcePressureInteractiveAcceptPersists(t *testing.T) {
	// An origin-holding clone gives distinct repo and checkout slugs, so a write
	// to the wrong slug is observable.
	t.Chdir(newOriginClone(t))
	withoutMemoryEnv(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, memoryPressureSandbox(), "run")
	identity := mustIdentity(t, ".")
	if identity.RepoSlug == identity.CheckoutSlug {
		t.Fatalf("fixture must have distinct repo and checkout slugs")
	}
	slug := effectiveConfigSlugForTest(t, identity)

	var selects int
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selects++
		return "raise", nil
	}
	ui.InputFn = func(string, string) (string, error) { return "6G", nil }

	err := root.Execute()
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("run = %v (%T), want ExitError code -1 from the abnormal attach", err, err)
	}
	if selects != 1 {
		t.Fatalf("memory raise prompt shown %d times, want 1", selects)
	}
	if got := resolvedMemory(t, slug); got != "6144M" {
		t.Errorf("resolved memory via the config slug = %q, want the accepted 6144M", got)
	}
	if got := resolvedMemory(t, identity.CheckoutSlug); got != "" {
		t.Errorf("resolved memory via the checkout slug = %q, want unset", got)
	}
}

// TestResourcePressureNonInteractiveDoesNotPrompt pins the never-block rule: a
// non-interactive run over the same OOM-like sandbox returns promptly with the
// ExitError, never prompts, and writes no memory value (the hit stays recorded
// for the next interactive launch).
func TestResourcePressureNonInteractiveDoesNotPrompt(t *testing.T) {
	t.Chdir(newOriginClone(t))
	withoutMemoryEnv(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, memoryPressureSandbox(), "run")
	identity := mustIdentity(t, ".")
	slug := effectiveConfigSlugForTest(t, identity)

	var selects int
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selects++
		return "raise", nil
	}
	// IsInteractiveResult is left at its zero value (false).

	err := root.Execute()
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != -1 {
		t.Fatalf("run = %v (%T), want ExitError code -1 from the abnormal attach", err, err)
	}
	if selects != 0 {
		t.Errorf("memory raise prompt shown %d times on a non-interactive run, want 0", selects)
	}
	if got := resolvedMemory(t, slug); got != "" {
		t.Errorf("resolved memory = %q, want unset (no write on a non-interactive run)", got)
	}
}

// diskPressureSandbox is a mock project VM whose attach ends cleanly and whose
// in-guest df reports /tmp short of space.
func diskPressureSandbox() *msbtest.MockSandbox {
	command, args := pressure.DiskProbeCommand()
	return &msbtest.MockSandbox{
		AttachCode: 0,
		ExecOut: map[string]sandboxmsb.ShellResult{
			command + " " + strings.Join(args, " "): msbtest.NewTestResult(true, 0,
				"Filesystem 1024-blocks Used Available Capacity Mounted on\n"+
					"overlay 4194304 1048576 3145728 25% /\n"+
					"workspace_x 16777216 1048576 15728640 7% /workspace\n"+
					"tmpfs 2097152 1996800 100352 96% /tmp\n", "", nil),
		},
	}
}

// withoutTmpSizeEnv clears the tmp-size env vars so neither shadows the write.
func withoutTmpSizeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTS_SANDBOX_TMP_SIZE", "")
	t.Setenv("OPENCODE_SANDBOX_TMP_SIZE", "")
}

// TestDiskPressureCleanExitOffersAndPersists drives `run` end to end: a session
// that ends cleanly with a full /tmp is offered a raise, and the accepted value
// is persisted to the config slug the resolver reads.
func TestDiskPressureCleanExitOffersAndPersists(t *testing.T) {
	t.Chdir(newOriginClone(t))
	withoutTmpSizeEnv(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, diskPressureSandbox(), "run")
	slug := effectiveConfigSlugForTest(t, mustIdentity(t, "."))
	var selects int
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selects++
		return "raise", nil
	}
	ui.InputFn = func(_, defaultValue string) (string, error) { return defaultValue, nil }

	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if selects != 1 {
		t.Fatalf("disk raise prompt shown %d times, want 1", selects)
	}
	r, err := viperconfig.NewResolver(nil, configpaths.Get(), slug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.Config().TmpSize; got != "3072M" {
		t.Errorf("resolved tmp-size = %q, want the accepted 3072M", got)
	}
}

// TestDiskPressureExplicitFlagExplainsInsteadOfPrompting pins the flag wiring
// end to end: with --tmp-size given, a full /tmp is explained (the flag sets
// the value) instead of offered.
func TestDiskPressureExplicitFlagExplainsInsteadOfPrompting(t *testing.T) {
	t.Chdir(newOriginClone(t))
	withoutTmpSizeEnv(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, diskPressureSandbox(), "run", "--tmp-size", "2G")
	var selects int
	ui.IsInteractiveResult = true
	ui.SelectFn = func(string, []termio.Choice, string) (string, error) {
		selects++
		return "raise", nil
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if selects != 0 {
		t.Errorf("disk raise prompt shown %d times with --tmp-size set, want 0", selects)
	}
	if !slices.ContainsFunc(
		ui.WarnCalls,
		func(message string) bool { return strings.Contains(message, "--tmp-size") },
	) {
		t.Errorf("warnings %q, want the --tmp-size flag named", ui.WarnCalls)
	}
}
