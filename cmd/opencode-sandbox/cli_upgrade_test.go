package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/update"
)

func TestUpgrade(t *testing.T) {
	origVersion := version
	origLatest := update.LatestVersion
	origUpdate := update.Update
	t.Cleanup(func() {
		version = origVersion
		update.LatestVersion = origLatest
		update.Update = origUpdate
	})

	t.Run("dev build is rejected", func(t *testing.T) {
		version = devVersion
		updateCmd, _ := setupUpgradeTestFixtures(t)
		err := updateCmd.RunE(updateCmd, nil)
		if err == nil {
			t.Fatal("expected error for dev build")
		}
	})

	t.Run("up to date", func(t *testing.T) {
		version = "1.0.0"
		update.LatestVersion = func(context.Context) (string, error) { return "1.0.0", nil }
		update.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }

		updateCmd, testUI := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(strings.Join(testUI.InfoCalls, " "), "up to date") {
			t.Fatalf("expected 'up to date' info, got %v", testUI.InfoCalls)
		}
	})

	t.Run("update available", func(t *testing.T) {
		version = "1.0.0"
		update.LatestVersion = func(context.Context) (string, error) { return "2.0.0", nil }
		var installed string
		update.Update = func(_ context.Context, latest string) error {
			installed = latest
			return nil
		}

		updateCmd, testUI := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if installed != "2.0.0" {
			t.Fatalf("installed version = %q, want 2.0.0", installed)
		}
		if !strings.Contains(strings.Join(testUI.InfoCalls, " "), "upgraded") {
			t.Fatalf("expected 'upgraded' info, got %v", testUI.InfoCalls)
		}
	})

	t.Run("latest lookup failure", func(t *testing.T) {
		version = "1.0.0"
		update.LatestVersion = func(context.Context) (string, error) { return "", errors.New("boom") }
		update.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }

		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err == nil {
			t.Fatal("expected error when latest lookup fails")
		}
	})
}

func setupUpgradeTestFixtures(t *testing.T) (*cobra.Command, *termio.Mock) {
	t.Helper()
	testUI := termio.NewTestMock(t)
	root := buildRootCmd(&testUI)
	upgradeCmd, _, _ := root.Find([]string{"upgrade"})
	return upgradeCmd, &testUI
}
