package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

func TestApplyCLISettingsAppliesResolverValues(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
		LogLevel: "error",
		Yes:      true,
		Quiet:    true,
	})

	if err := applyCLISettings(&cobra.Command{}, &ui, r); err != nil {
		t.Fatalf("applyCLISettings: %v", err)
	}

	if ui.Level() != termio.LevelError {
		t.Errorf("Level() = %v, want LevelError", ui.Level())
	}
	if !ui.AssumeYes() {
		t.Error("AssumeYes() = false, want true")
	}
	if !ui.Quiet() {
		t.Error("Quiet() = false, want true")
	}
}

func TestApplyCLISettingsSetsWarningLevel(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{LogLevel: "warning"})

	if err := applyCLISettings(&cobra.Command{}, &ui, r); err != nil {
		t.Fatalf("applyCLISettings: %v", err)
	}
	if ui.Level() != termio.LevelWarning {
		t.Errorf("Level() = %v, want LevelWarning", ui.Level())
	}
}

func TestApplyCLISettingsRejectsInvalidLevel(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{LogLevel: "bogus"})

	if err := applyCLISettings(&cobra.Command{}, &ui, r); err == nil {
		t.Fatal("applyCLISettings with invalid level should error")
	}
}

func TestApplyCLISettingsNilCommandOrResolverNoop(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	if err := applyCLISettings(nil, &ui, nil); err != nil {
		t.Fatalf("applyCLISettings(nil): unexpected error %v", err)
	}

	// No panic, and the UI level is left untouched.
	if ui.Level() != termio.LevelError {
		t.Errorf("Level() = %v, want untouched LevelError", ui.Level())
	}
}

// TestApplyCLISettingsNilCommandDoesNotApply pins that a nil command is a no-op
// even when the resolver is present, and that a nil resolver is a no-op even
// when the command is present (no nil-dereference).
func TestApplyCLISettingsNilCommandDoesNotApply(t *testing.T) {
	ui := termiotest.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{LogLevel: "error", Yes: true, Quiet: true})

	if err := applyCLISettings(nil, &ui, r); err != nil {
		t.Fatalf("applyCLISettings(nil cmd): %v", err)
	}
	if ui.AssumeYes() || ui.Quiet() {
		t.Error("settings must not be applied for a nil command")
	}

	ui2 := termiotest.NewTestMock(t)
	if err := applyCLISettings(&cobra.Command{}, &ui2, nil); err != nil {
		t.Fatalf("applyCLISettings(nil resolver): %v", err)
	}
}
