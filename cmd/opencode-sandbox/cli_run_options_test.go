package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
	launcherconfig "gitlab.inoio.de/inoio/opencode-sandbox/internal/viperconfig"
)

// extractRunOptionsReapPolicy tests that extractRunOptions populates
// ReapPolicy and IdleTimeout from a launcherconfig.Config stored in
// the command's context (wired via PersistentPreRunE in production).

// A zero-value Config produces the expected defaults.
func TestExtractRunOptionsDefaults(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{} // zero value
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	expectedPolicy := options.ReapPolicy{
		AutoStopOnActiveSessions: false,
		MaxSessionRetries:        10,
	}
	if opts.ReapPolicy != expectedPolicy {
		t.Errorf("ReapPolicy = %+v; want %+v", opts.ReapPolicy, expectedPolicy)
	}
	if opts.IdleTimeout != 10*time.Second {
		t.Errorf("IdleTimeout = %v; want 10s", opts.IdleTimeout)
	}
}

// AutoStopOnActiveSessions: true propagates to ReapPolicy.
func TestExtractRunOptionsAutoStopOnActiveSessions(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopOnActiveSessions: true}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if !opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Errorf("ReapPolicy.AutoStopOnActiveSessions = false; want true")
	}
}

// Custom AutoStopMaxSessionRetries propagates to ReapPolicy.
func TestExtractRunOptionsCustomMaxSessionRetries(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopMaxSessionRetries: 5}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.ReapPolicy.MaxSessionRetries != 5 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 5", opts.ReapPolicy.MaxSessionRetries)
	}
}

func TestExtractRunOptionsAutoStopTimeout(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{AutoStopTimeout: 30 * time.Second}
	cmd := buildCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsWithoutConfig(t *testing.T) {
	ui := &termio.Mock{}
	cmd := buildCommandWithoutLauncherConfig(ui)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	if opts.ReapPolicy.AutoStopOnActiveSessions {
		t.Error("unexpected ReapPolicy.AutoStopOnActiveSessions without config")
	}
	if opts.ReapPolicy.MaxSessionRetries != 0 {
		t.Errorf("ReapPolicy.MaxSessionRetries = %d; want 0 (zero value)", opts.ReapPolicy.MaxSessionRetries)
	}
	if opts.IdleTimeout != 0 {
		t.Errorf("IdleTimeout = %v; want 0 (zero value)", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsShellCommand(t *testing.T) {
	ui := &termio.Mock{}
	lc := launcherconfig.Config{
		AutoStopOnActiveSessions:  true,
		AutoStopMaxSessionRetries: 7,
		AutoStopTimeout:           25 * time.Second,
	}
	cmd := buildShellCommandWithLauncherConfig(ui, lc)

	opts, err := extractRunOptions(cmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}

	expectedPolicy := options.ReapPolicy{
		AutoStopOnActiveSessions: true,
		MaxSessionRetries:        7,
	}
	if opts.ReapPolicy != expectedPolicy {
		t.Errorf("ReapPolicy = %+v; want %+v", opts.ReapPolicy, expectedPolicy)
	}
	if opts.IdleTimeout != 25*time.Second {
		t.Errorf("IdleTimeout = %v; want 25s", opts.IdleTimeout)
	}
}

func TestExtractRunOptionsServeOnly(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagServeOnly, "true"); err != nil {
		t.Fatalf("set serve-only: %v", err)
	}
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if !opts.ServeOnly {
		t.Errorf("expected ServeOnly=true when --serve-only passed, got false")
	}

	cmd2 := buildRunCmd(&termio.Mock{})
	opts2, err := extractRunOptions(cmd2, &termio.Mock{})
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts2.ServeOnly {
		t.Errorf("expected ServeOnly=false by default, got true")
	}
}

// buildCommandWithLauncherConfig builds a "run" command and injects
// a viperconfig.Config into the context, mimicking what
// PersistentPreRunE does in production.
func buildCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	cmd := buildRunCmd(ui)
	rootCtx := context.Background()
	rootCtx = context.WithValue(rootCtx, (*launcherConfigKey)(nil), lc)
	cmd.SetContext(rootCtx)
	return cmd
}

// buildShellCommandWithLauncherConfig builds a "shell" command and injects
// a viperconfig.Config into the context, verifying the shell path.
func buildShellCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	pet := buildShellCmd(ui)
	petCtx := context.Background()
	petCtx = context.WithValue(petCtx, (*launcherConfigKey)(nil), lc)
	pet.SetContext(petCtx)
	return pet
}

// buildCommandWithoutLauncherConfig builds a "run" command with no
// launcher config in its context, exercising the absent-config path.
func buildCommandWithoutLauncherConfig(ui termio.UI) *cobra.Command {
	cmd := buildRunCmd(ui)
	return cmd // SetContext never called → no config key present
}

func TestExtractRunOptionsInvalidTmpSize(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagTmpSize, "bogus"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --tmp-size")
	}
	if want := "invalid --tmp-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsInvalidDiskSize(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagDiskSize, "bogus"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	_, err := extractRunOptions(cmd, &termio.Mock{})
	if err == nil {
		t.Fatal("expected error for invalid --disk-size")
	}
	if want := "invalid --disk-size"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want to contain %q", err, want)
	}
}

func TestExtractRunOptionsValidSizeFlags(t *testing.T) {
	cmd := buildRunCmd(&termio.Mock{})
	if err := cmd.Flags().Set(flagTmpSize, "4G"); err != nil {
		t.Fatalf("set tmp-size: %v", err)
	}
	if err := cmd.Flags().Set(flagDiskSize, "16G"); err != nil {
		t.Fatalf("set disk-size: %v", err)
	}
	opts, err := extractRunOptions(cmd, &termio.Mock{})
	if err != nil {
		t.Fatalf("expected no error for valid flags, got: %v", err)
	}
	if opts.TmpSize != "4G" {
		t.Errorf("TmpSize = %q, want 4G", opts.TmpSize)
	}
	if opts.DiskSize != "16G" {
		t.Errorf("DiskSize = %q, want 16G", opts.DiskSize)
	}
}
