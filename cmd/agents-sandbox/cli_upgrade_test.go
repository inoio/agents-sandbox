package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/upgrade"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
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
		t.Setenv(upgradeAnyVersionEnv, "")
		version = devVersion
		updateCmd, _ := setupUpgradeTestFixtures(t)
		err := updateCmd.RunE(updateCmd, nil)
		if err == nil {
			t.Fatal("expected error for dev build")
		}
	})

	t.Run("up to date", func(t *testing.T) {
		version = "1.0.0"
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) { return "1.0.0", nil }
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
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) { return "2.0.0", nil }
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

	t.Run("homebrew install defers to brew", func(t *testing.T) {
		version = "1.0.0"
		origHomebrew := upgrade.InstalledViaHomebrew
		upgrade.InstalledViaHomebrew = func() bool { return true }
		t.Cleanup(func() { upgrade.InstalledViaHomebrew = origHomebrew })
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) {
			t.Fatal("LatestVersion should not be consulted for a Homebrew install")
			return "", nil
		}
		upgrade.Update = func(context.Context, string) error {
			t.Fatal("Update should not replace the binary of a Homebrew install")
			return nil
		}

		updateCmd, testUI := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(strings.Join(testUI.InfoCalls, " "), "brew") {
			t.Fatalf("expected brew upgrade guidance, got %v", testUI.InfoCalls)
		}
	})

	t.Run("latest lookup failure", func(t *testing.T) {
		version = "1.0.0"
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) { return "", errors.New("boom") }
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

func TestUpgradeAnyVersionLever(t *testing.T) {
	origVersion, origLatest := version, upgrade.LatestVersion
	t.Cleanup(func() { version, upgrade.LatestVersion = origVersion, origLatest })

	t.Run("dev build with the lever set is upgradable", func(t *testing.T) {
		t.Setenv(upgradeAnyVersionEnv, "1")
		version = devVersion
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) { return "2.0.0", nil }
		origUpdate := upgrade.Update
		upgrade.Update = func(context.Context, string) error { return nil }
		t.Cleanup(func() { upgrade.Update = origUpdate })

		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dev build without the lever is rejected", func(t *testing.T) {
		t.Setenv(upgradeAnyVersionEnv, "")
		version = devVersion
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) {
			t.Fatal("LatestVersion must not be consulted without the lever")
			return "", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.RunE(updateCmd, nil); err == nil {
			t.Fatal("expected a dev build to be rejected")
		}
	})
}

func TestUpgradeChannelSelection(t *testing.T) {
	origVersion, origLatest, origUpdate := version, upgrade.LatestVersion, upgrade.Update
	t.Cleanup(func() { version, upgrade.LatestVersion, upgrade.Update = origVersion, origLatest, origUpdate })
	version = "1.0.0"

	t.Run("flag selects canary", func(t *testing.T) {
		upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }
		var got upgrade.Channel
		upgrade.LatestVersion = func(_ context.Context, ch upgrade.Channel) (string, error) {
			got = ch
			return "1.0.0", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.Flags().Set(flagChannel, "canary"); err != nil {
			t.Fatal(err)
		}
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("resolved channel = %q, want canary", got)
		}
	})

	t.Run("configuration selects canary", func(t *testing.T) {
		upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }
		var got upgrade.Channel
		upgrade.LatestVersion = func(_ context.Context, ch upgrade.Channel) (string, error) {
			got = ch
			return "1.0.0", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
			Upgrade: launcherconfig.UpgradeConfig{Channel: "canary"},
		})
		updateCmd.SetContext(context.WithValue(context.Background(), (*launcherConfigKey)(nil), r))
		if err := updateCmd.RunE(updateCmd, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("resolved channel = %q, want canary from config", got)
		}
	})

	t.Run("invalid flag is rejected", func(t *testing.T) {
		upgrade.Update = func(context.Context, string) error { t.Fatal("Update should not be called"); return nil }
		upgrade.LatestVersion = func(context.Context, upgrade.Channel) (string, error) {
			t.Fatal("LatestVersion must not be consulted for an invalid channel")
			return "", nil
		}
		updateCmd, _ := setupUpgradeTestFixtures(t)
		if err := updateCmd.Flags().Set(flagChannel, "bogus"); err != nil {
			t.Fatal(err)
		}
		if err := updateCmd.RunE(updateCmd, nil); err == nil {
			t.Fatal("expected an error for an invalid --channel value")
		}
	})
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
		t.Setenv(upgradeAnyVersionEnv, "")
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

	t.Run("passes the configured channel", func(t *testing.T) {
		var got upgrade.Channel
		upgradeCheck = func(_ context.Context, opts upgrade.Options) (upgrade.Result, error) {
			got = opts.Channel
			return upgrade.Result{}, nil
		}
		r := launcherconfig.NewResolverWithConfig(launcherconfig.Config{
			Upgrade: launcherconfig.UpgradeConfig{Channel: "canary"},
		})
		if _, err := checkForUpgrade(context.Background(), r, &termio.Mock{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != upgrade.ChannelCanary {
			t.Fatalf("Check channel = %q, want canary", got)
		}
	})
}

func TestRunShellUpgradeExit(t *testing.T) {
	for _, cmdName := range []string{"run", "shell"} {
		t.Run(cmdName, func(t *testing.T) {
			initTestProjectDir(t)
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
			initTestProjectDir(t)
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
