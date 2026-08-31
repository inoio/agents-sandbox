package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/termio"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

func TestApplyCLISettingsAppliesResolverValues(t *testing.T) {
	ui := termio.NewTestMock(t)
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
	ui := termio.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{LogLevel: "warning"})

	if err := applyCLISettings(&cobra.Command{}, &ui, r); err != nil {
		t.Fatalf("applyCLISettings: %v", err)
	}
	if ui.Level() != termio.LevelWarning {
		t.Errorf("Level() = %v, want LevelWarning", ui.Level())
	}
}

func TestApplyCLISettingsRejectsInvalidLevel(t *testing.T) {
	ui := termio.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{LogLevel: "bogus"})

	if err := applyCLISettings(&cobra.Command{}, &ui, r); err == nil {
		t.Fatal("applyCLISettings with invalid level should error")
	}
}

func TestApplyCLISettingsNilCommandOrResolverNoop(t *testing.T) {
	ui := termio.NewTestMock(t)
	if err := applyCLISettings(nil, &ui, nil); err != nil {
		t.Fatalf("applyCLISettings(nil): unexpected error %v", err)
	}

	// No panic, and the UI level is left untouched.
	if ui.Level() != termio.LevelError {
		t.Errorf("Level() = %v, want untouched LevelError", ui.Level())
	}
}
