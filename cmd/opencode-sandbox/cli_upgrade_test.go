package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	sandboxmsb "github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/upgrade"
	launcherconfig "github.com/inoio/opencode-sandbox/internal/viperconfig"
)

func TestUpgrade(t *testing.T) {
	origVersion := version
	origLatest := upgrade.LatestVersion
	origUpdate := upgrade.Update
	t.Cleanup(func() {
		version = origVersion
		upgrade.LatestVersion = origLatest
		upgrade.Update = origUpdate
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
		upgrade.LatestVersion = func(context.Context) (string, error) { return "1.0.0", nil }
		upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }

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
		upgrade.LatestVersion = func(context.Context) (string, error) { return "2.0.0", nil }
		var installed string
		upgrade.Update = func(_ context.Context, latest string) error {
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
		upgrade.LatestVersion = func(context.Context) (string, error) { return "", errors.New("boom") }
		upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }

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

func TestCheckForUpgrade(t *testing.T) {
	origVersion := version
	origCheck := upgradeCheck
	t.Cleanup(func() {
		version = origVersion
		upgradeCheck = origCheck
	})

	t.Run("nil resolver no-ops", func(t *testing.T) {
		exit, err := checkForUpgrade(context.Background(), nil, &termio.Mock{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exit {
			t.Fatal("expected exit=false for a nil resolver")
		}
	})

	t.Run("dev version no-ops", func(t *testing.T) {
		version = devVersion
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{})
		exit, err := checkForUpgrade(context.Background(), r, &termio.Mock{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exit {
			t.Fatal("expected exit=false for a dev version")
		}
	})

	t.Run("propagates check error", func(t *testing.T) {
		upgradeCheck = func(context.Context, upgrade.Options) (upgrade.Result, error) {
			return upgrade.Result{}, errors.New("boom")
		}
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{})
		if _, err := checkForUpgrade(context.Background(), r, &termio.Mock{}); err == nil {
			t.Fatal("expected the upgrade-check error to propagate")
		}
	})

	t.Run("reports exit", func(t *testing.T) {
		upgradeCheck = func(context.Context, upgrade.Options) (upgrade.Result, error) {
			return upgrade.Result{Exit: true}, nil
		}
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{})
		exit, err := checkForUpgrade(context.Background(), r, &termio.Mock{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exit {
			t.Fatal("expected exit=true when the upgrade-and-exit mode ran")
		}
	})
}

func TestRunShellUpgradeExit(t *testing.T) {
	for _, cmdName := range []string{"run", "shell"} {
		t.Run(cmdName, func(t *testing.T) {
			initTestRepo(t)
			origCheck := upgradeCheck
			t.Cleanup(func() { upgradeCheck = origCheck })
			upgradeCheck = func(context.Context, upgrade.Options) (upgrade.Result, error) {
				return upgrade.Result{Exit: true}, nil
			}

			mock := &sandboxmsb.MockMsbClient{}
			root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, cmdName)

			// The upgrade-and-exit path must terminate before starting a session,
			// so the command succeeds without ever creating a sandbox.
			if err := root.Execute(); err != nil {
				t.Fatalf("expected clean exit after upgrade-and-exit, got: %v", err)
			}
			if len(mock.CreatedSandboxCalls) != 0 {
				t.Errorf("expected no sandbox creation, got %d calls", len(mock.CreatedSandboxCalls))
			}
		})
	}
}

func TestRunShellUpgradeError(t *testing.T) {
	for _, cmdName := range []string{"run", "shell"} {
		t.Run(cmdName, func(t *testing.T) {
			initTestRepo(t)
			origCheck := upgradeCheck
			t.Cleanup(func() { upgradeCheck = origCheck })
			upgradeCheck = func(context.Context, upgrade.Options) (upgrade.Result, error) {
				return upgrade.Result{}, errors.New("upgrade failed")
			}

			mock := &sandboxmsb.MockMsbClient{}
			root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, cmdName)

			err := root.Execute()
			if err == nil {
				t.Fatal("expected the upgrade-check error to abort the command")
			}
			if !strings.Contains(err.Error(), "upgrade failed") {
				t.Errorf("expected error containing 'upgrade failed', got: %v", err)
			}
		})
	}
}
