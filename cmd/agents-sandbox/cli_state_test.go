package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func rootStatePath() string {
	return filepath.Join(configpaths.Get().UserStateDir(), "state.yaml")
}

func seedRootStateFile(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(rootStatePath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(rootStatePath(), []byte(content), 0o644); err != nil {
		t.Fatalf("seed root state: %v", err)
	}
}

func TestStateRepairQuarantinesCorruptRootState(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdState, cmdRepair)
	seedRootStateFile(t, "migrations: [unterminated")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("state repair: %v", err)
	}
	quarantined, _ := filepath.Glob(rootStatePath() + ".corrupt-*")
	if len(quarantined) != 1 {
		t.Fatalf("quarantined files = %v, want one", quarantined)
	}
	if !containsNormalized(ui.OutCalls, "Moved the corrupt launcher state to "+quarantined[0]+".") {
		t.Errorf("OutCalls missing quarantine path; got: %v", ui.OutCalls)
	}
	if !containsNormalized(
		ui.OutCalls,
		"Update checks, migration records, and adoption claims start fresh on the next run.",
	) {
		t.Errorf("OutCalls missing fresh-start notice; got: %v", ui.OutCalls)
	}
	if _, err := state.ReadRootState(); err != nil {
		t.Errorf("root state still unreadable after repair: %v", err)
	}
}

func TestStateRepairLeavesHealthyRootStateAlone(t *testing.T) {
	cmd, ui := setupCommandFixtures(t, cmdState, cmdRepair)
	seedRootStateFile(t, "schema: 1\n")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("state repair: %v", err)
	}
	if !containsNormalized(ui.OutCalls, "Launcher state is healthy; nothing to repair.") {
		t.Errorf("OutCalls = %v, want healthy message", ui.OutCalls)
	}
	if got, _ := os.ReadFile(rootStatePath()); string(got) != "schema: 1\n" {
		t.Errorf("healthy root state changed: %q", got)
	}
}

func TestStateRepairRefusesNewerSchema(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdState, cmdRepair)
	seedRootStateFile(t, "schema: 99\n")

	err := cmd.Execute()
	if !errors.Is(err, state.ErrStateSchemaUnsupported) {
		t.Fatalf("state repair error = %v, want ErrStateSchemaUnsupported", err)
	}
	if !strings.Contains(err.Error(), "repair launcher state") {
		t.Errorf("error = %v, want command context", err)
	}
}

func TestStateRepairRejectsArguments(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdState, cmdRepair, "extra")
	if err := cmd.Execute(); err == nil {
		t.Fatal("state repair accepted a positional argument")
	}
}

// TestStateRepairSkipsRuntimePreflight pins that the recovery path stays usable
// when the microsandbox runtime is broken.
func TestStateRepairSkipsRuntimePreflight(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, cmdState, cmdRepair)
	oldPrepare := prepareMSBRuntime
	prepareMSBRuntime = func(context.Context, termio.UI, string) error {
		return errors.New("runtime must not be prepared")
	}
	t.Cleanup(func() { prepareMSBRuntime = oldPrepare })

	if err := cmd.Execute(); err != nil {
		t.Fatalf("state repair: %v", err)
	}
}
