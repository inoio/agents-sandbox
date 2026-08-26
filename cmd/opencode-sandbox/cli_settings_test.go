package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/termio"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

func TestLevelFrom(t *testing.T) {
	tests := []struct {
		name    string
		quiet   bool
		verbose bool
		want    termio.Level
	}{
		{"quiet wins over verbose", true, true, termio.LevelQuiet},
		{"quiet", true, false, termio.LevelQuiet},
		{"verbose", false, true, termio.LevelVerbose},
		{"normal default", false, false, termio.LevelNormal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := levelFrom(tc.quiet, tc.verbose); got != tc.want {
				t.Errorf("levelFrom(%v, %v) = %v, want %v", tc.quiet, tc.verbose, got, tc.want)
			}
		})
	}
}

func TestApplyCLISettingsAppliesResolverValues(t *testing.T) {
	ui := termio.NewTestMock(t)
	r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
		Error:   true,
		Verbose: true,
		Yes:     true,
	})

	applyCLISettings(&cobra.Command{}, &ui, r)

	if ui.Level() != termio.LevelQuiet {
		t.Errorf("Level() = %v, want LevelQuiet", ui.Level())
	}
	if !ui.AssumeYes() {
		t.Error("AssumeYes() = false, want true")
	}
}

func TestApplyCLISettingsNilCommandOrResolverNoop(t *testing.T) {
	ui := termio.NewTestMock(t)
	applyCLISettings(nil, &ui, nil)

	// No panic, and the UI keeps its defaults.
	if ui.Level() != termio.LevelNormal {
		t.Errorf("Level() = %v, want default LevelNormal", ui.Level())
	}
}
