package vm

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// errUpgradeQuit signals the user chose to abort the session because of the
// pending image upgrade.
var errUpgradeQuit = errors.New("opencode upgrade cancelled") //nolint:err113 // static sentinel intended

// userProvidedAgentVersion is returned as the build version for a
// user-provided agent: the install block skips such agents, so the value is
// never consumed, and a non-empty value keeps resolveAgentVersion from hitting
// the network.
const userProvidedAgentVersion = "user-provided"

// agentLatestVersion returns the newest release version for the agent via its
// own UpgradeChecker.
//
//nolint:gochecknoglobals // test seam
var agentLatestVersion = func(ctx context.Context, a agent.Agent) (string, error) {
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		return "", nil
	}
	return checker.LatestVersion(ctx)
}

// resolveBuildVersion decides the opencode version to bake into the runner
// image before the image is built, so a normal run never builds twice (once
// for the current version and once for an upgrade). It returns the target
// version and whether that version is a freshly chosen upgrade.
//
// An explicitly pinned version skips the update check entirely. An agent
// without an upgrade checker uses the current recorded version without any
// check. Otherwise the update check runs first and may offer to rebuild with a
// newer release, deciding the final version up front.
func resolveBuildVersion(
	ctx context.Context,
	a agent.Agent,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, error) {
	// An explicitly pinned version is authoritative: never prompt for an upgrade.
	if opts.AgentVersion != "" {
		return opts.AgentVersion, false, nil
	}

	// A user-provided agent is not owned by the tool: a rebuild would not
	// change its version, so never check or offer an upgrade.
	if currentAgentSource(a) == agentSourceUser {
		version := currentUpgradeVersion(a)
		if version == "" {
			version = userProvidedAgentVersion
		}
		return version, false, nil
	}

	// Without an upgrade checker there is nothing to check against; reuse the
	// recorded version so the image identity stays stable.
	if _, ok := agent.AsUpgradeChecker(a); !ok {
		return currentUpgradeVersion(a), false, nil
	}

	// A forced rebuild uses whatever version is current; no upgrade prompt.
	if opts.Rebuild {
		return currentUpgradeVersion(a), false, nil
	}

	current := currentUpgradeVersion(a)
	if current == "" {
		// No recorded baseline (e.g. first run): resolve latest at build time
		// rather than prompting against an unknown version.
		return "", false, nil
	}

	latest, offer := pendingUpgrade(ctx, ui, a, current)
	if !offer {
		return current, false, nil
	}

	chosen, err := promptUpgrade(ui, a, current, latest)
	if err != nil {
		return "", false, err
	}
	if chosen == latest {
		return latest, true, nil
	}
	return current, false, nil
}

// promptUpgrade offers to rebuild the runner image with a newer release. It
// returns the chosen version, or an error when the user aborts. A
// non-interactive session logs the availability but keeps the current version.
func promptUpgrade(ui termio.UI, a agent.Agent, current, latest string) (string, error) {
	if !ui.IsInteractive() {
		ui.Infof(
			"%s %s available (image has %s); run 'opencode-sandbox build' to upgrade",
			a.Name(),
			latest,
			current,
		) //nolint:lll // user-facing line
		return current, nil
	}

	prompt := fmt.Sprintf("Rebuild the runner image with %s %s?", a.Name(), latest)
	choices := []termio.Choice{
		{Label: "Rebuild", Key: "r", Description: "Rebuild the image with " + a.Name() + " " + latest},
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

// pendingUpgrade resolves whether a newer version is available and should be
// offered for rebuild, enforcing the once-per-day and once-per-version gates
// against the given current version. It returns the candidate version and
// whether the user should be prompted. The once-per-day window and offered set
// are persisted on every successful check; an offline or failed check never
// fails the session and leaves the window open.
func pendingUpgrade(ctx context.Context, ui termio.UI, a agent.Agent, currentVersion string) (string, bool) {
	state := loadOrFreshUpgradeState(ui)
	entry := state.Agents[a.Name()]
	if !entry.dueForCheck(now()) {
		return "", false
	}

	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		return "", false
	}

	latest, err := agentLatestVersion(ctx, a)
	if err != nil {
		// Offline or unreachable: never fail the session, and leave the
		// once-per-day window open so the next online run retries.
		ui.Warnf("could not check for opencode updates: %v", err)
		return "", false
	}

	// A successful check refreshes the once-per-day window regardless of
	// whether an upgrade is available.
	entry.LastChecked = now()
	newer, err := checker.NewerThan(latest, currentVersion)
	if err != nil {
		// An unparsable version never fails the session; leave the window open.
		return "", false
	}
	if !newer || entry.offered(latest) {
		state.Agents[a.Name()] = entry
		persistUpgradeState(ui, state)
		return "", false
	}

	entry.markOffered(latest)
	state.Agents[a.Name()] = entry
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
