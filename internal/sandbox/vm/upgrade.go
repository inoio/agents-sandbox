package vm

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/opencode"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// errUpgradeQuit signals the user chose to abort the session because of the
// pending image upgrade.
var errUpgradeQuit = errors.New("opencode upgrade cancelled") //nolint:err113 // static sentinel intended

// openCodeUpgradeInfo returns the latest opencode release version string.
//
//nolint:gochecknoglobals // test seam
var openCodeUpgradeInfo = func(ctx context.Context) (string, error) {
	return opencode.LatestVersion(ctx)
}

// resolveOpenCodeBuildVersion decides the opencode version to bake into the
// runner image before the image is built, so a normal run never builds twice
// (once for the current version and once for an upgrade). It returns the target
// version and whether that version is a freshly chosen upgrade.
//
// An explicitly pinned version skips the update check entirely. Otherwise the
// update check runs first and may offer to rebuild with a newer opencode
// release, deciding the final version up front.
func resolveOpenCodeBuildVersion(
	ctx context.Context,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, error) {
	// An explicitly pinned version is authoritative: never prompt for an upgrade.
	if opts.OpenCodeVersion != "" {
		return opts.OpenCodeVersion, false, nil
	}

	// A forced rebuild uses whatever version is current; no upgrade prompt.
	if opts.Rebuild {
		return currentUpgradeVersion(), false, nil
	}

	current := currentUpgradeVersion()
	if current == "" {
		// No recorded baseline (e.g. first run): resolve latest at build time
		// rather than prompting against an unknown version.
		return "", false, nil
	}

	latest, offer := pendingUpgrade(ctx, ui, current)
	if !offer {
		return current, false, nil
	}

	chosen, err := promptUpgrade(ui, current, latest)
	if err != nil {
		return "", false, err
	}
	if chosen == latest {
		return latest, true, nil
	}
	return current, false, nil
}

// promptUpgrade offers to rebuild the runner image with a newer opencode
// release. It returns the chosen version, or an error when the user aborts.
// A non-interactive session logs the availability but keeps the current version.
func promptUpgrade(ui termio.UI, current, latest string) (string, error) {
	if !ui.IsInteractive() {
		ui.Infof(
			"opencode %s available (image has %s); run 'opencode-sandbox build' to upgrade",
			latest,
			current,
		) //nolint:lll // user-facing line
		return current, nil
	}

	prompt := fmt.Sprintf("Rebuild the runner image with opencode %s?", latest)
	choices := []termio.Choice{
		{Label: "Rebuild", Key: "r", Description: "Rebuild the image with opencode " + latest},
		{Label: "Keep", Key: "k", Description: "Keep the current image, don't remind again"},
		{Label: "Quit", Key: "q", Description: "Abort this session"},
	}
	choice, err := ui.Select(prompt, choices, "r")
	if err != nil {
		return "", err
	}
	switch choice {
	case "r":
		return latest, nil
	case "q":
		return "", errUpgradeQuit
	default:
		return current, nil
	}
}

// pendingUpgrade resolves whether a newer opencode version is available and
// should be offered for rebuild, enforcing the once-per-day and once-per-version
// gates against the given current version. It returns the candidate version and
// whether the user should be prompted. The once-per-day window and offered set
// are persisted on every successful check; an offline or failed check never
// fails the session and leaves the window open.
func pendingUpgrade(ctx context.Context, ui termio.UI, currentVersion string) (string, bool) {
	state := loadOrFreshUpgradeState(ui)
	if !state.dueForCheck(now()) {
		return "", false
	}

	latest, err := openCodeUpgradeInfo(ctx)
	if err != nil {
		// Offline or unreachable: never fail the session, and leave the
		// once-per-day window open so the next online run retries.
		ui.Warnf("could not check for opencode updates: %v", err)
		return "", false
	}

	// A successful check refreshes the once-per-day window regardless of
	// whether an upgrade is available.
	state.LastChecked = now()
	if opencode.VersionCompare(latest, currentVersion) <= 0 || state.offered(latest) {
		persistUpgradeState(ui, state)
		return "", false
	}

	state.markOffered(latest)
	persistUpgradeState(ui, state)
	return latest, true
}

// loadOrFreshUpgradeState loads the updater state, falling back to a fresh
// state on any read error so bookkeeping never blocks a session.
func loadOrFreshUpgradeState(ui termio.UI) upgradeState {
	state, err := loadUpgradeState()
	if err != nil {
		ui.Warnf("could not read updater state: %v (checking anyway)", err)
		return upgradeState{}
	}
	return state
}

// persistUpgradeState best-effort writes the updater state, warning on failure
// without ever failing the session.
func persistUpgradeState(ui termio.UI, state upgradeState) {
	if err := saveUpgradeState(state); err != nil {
		ui.Warnf("could not persist updater state: %v", err)
	}
}
