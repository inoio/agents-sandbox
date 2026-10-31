package upgrade

import (
	"context"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/termio"
)

// stubRelease makes LatestVersion report latest and records what Update
// installs, restoring both when the test ends.
func stubRelease(t *testing.T, latest string) *string {
	t.Helper()
	origLatest, origUpdate := LatestVersion, Update
	t.Cleanup(func() { LatestVersion, Update = origLatest, origUpdate })
	LatestVersion = func(context.Context, Channel) (string, error) { return latest, nil }
	installed := new(string)
	Update = func(_ context.Context, version string) error {
		*installed = version
		return nil
	}
	return installed
}

func TestUpgradeSwitchesFromCanaryToStable(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "0.5.3" {
		t.Fatalf("installed = %q, want the stable 0.5.3", *installed)
	}
	if !strings.Contains(strings.Join(ui.InfoCalls, "\n"), "switching agents-sandbox 0.6.0-canary.1 -> 0.5.3") {
		t.Fatalf("expected a channel switch message, got %v", ui.InfoCalls)
	}
}

func TestUpgradeSwitchTreatsZeroChannelAsStable(t *testing.T) {
	installed := stubRelease(t, "0.5.3")

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "0.5.3" {
		t.Fatalf("installed = %q, want the stable 0.5.3", *installed)
	}
}

func TestUpgradeWithoutChannelSwitchKeepsCanaryAndHints(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion: "0.6.0-canary.1",
		Channel:        ChannelStable,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want no install without an explicit channel switch", *installed)
	}
	info := strings.Join(ui.InfoCalls, "\n")
	if !strings.Contains(info, "up to date (0.6.0-canary.1)") {
		t.Fatalf("expected the up-to-date message, got %v", ui.InfoCalls)
	}
	if !strings.Contains(info, "agents-sandbox upgrade --channel stable") || !strings.Contains(info, "0.5.3") {
		t.Fatalf("expected a hint to switch to the stable 0.5.3, got %v", ui.InfoCalls)
	}
}

func TestUpgradeCanaryChannelNeverSwitches(t *testing.T) {
	installed := stubRelease(t, "0.6.0-canary.1")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelCanary,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want nothing on an up-to-date canary", *installed)
	}
	if strings.Contains(strings.Join(ui.InfoCalls, "\n"), "--channel stable") {
		t.Fatalf("a canary-channel run must not hint at stable, got %v", ui.InfoCalls)
	}
}

func TestUpgradeStableReleaseIsNotSwitched(t *testing.T) {
	installed := stubRelease(t, "0.5.3")
	ui := termio.Mock{}

	err := Upgrade(context.Background(), &ui, UpgradeOptions{
		CurrentVersion:     "0.5.3",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want nothing on an up-to-date stable release", *installed)
	}
	if len(ui.InfoCalls) != 1 || !strings.Contains(ui.InfoCalls[0], "up to date") {
		t.Fatalf("expected only the up-to-date message, got %v", ui.InfoCalls)
	}
}

func TestUpgradeNeverDowngradesAStableRelease(t *testing.T) {
	installed := stubRelease(t, "0.5.3")

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *installed != "" {
		t.Fatalf("installed = %q, want no downgrade of a stable release", *installed)
	}
}

func TestUpgradeChannelSwitchReportsInstallFailure(t *testing.T) {
	stubRelease(t, "0.5.3")
	Update = func(context.Context, string) error { return errBoom }

	err := Upgrade(context.Background(), &termio.Mock{}, UpgradeOptions{
		CurrentVersion:     "0.6.0-canary.1",
		Channel:            ChannelStable,
		AllowChannelSwitch: true,
	})
	if err == nil {
		t.Fatal("expected the install failure to be returned")
	}
}
