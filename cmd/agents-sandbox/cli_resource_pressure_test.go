package main

import (
	"errors"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// memoryPressureSandbox is a mock project VM whose attach ends abnormally
// (signal death) with a guest OOM kill in the system log and metrics naming a
// 4 GiB limit, so the post-attach detection attributes the crash to memory.
func memoryPressureSandbox() *sandboxmsb.MockSandbox {
	return &sandboxmsb.MockSandbox{
		AttachCode: -1,
		LogsResult: []msbSdk.LogEntry{{
			Source:    msbSdk.LogSourceSystem,
			Timestamp: time.Now(),
			Data:      []byte("Out of memory: Killed process 42 (opencode) total-vm:1kB"),
		}},
		MetricsResult: &msbSdk.Metrics{MemoryBytes: 1024 << 20, MemoryLimitBytes: 4096 << 20},
	}
}

// withoutMemoryEnv clears the memory env vars for the duration of the test, so
// PersistMemory's shadow guard does not refuse the write.
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

	mock := &sandboxmsb.MockMsbClient{}
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

	mock := &sandboxmsb.MockMsbClient{}
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
