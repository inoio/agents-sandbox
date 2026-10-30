package vm

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// errUpgradeQuit signals the user chose to abort the session because of the
// pending image upgrade.
var errUpgradeQuit = errors.New("agent upgrade cancelled")

// agentLatestVersion resolves an agent's newest release for the upgrade check.
// It is the same resolution the image build uses for a deferred version
// (agent.LatestVersion), so a change to how a release is resolved lands in one
// place; the alias keeps the lookup injectable, so no test hits the network.
var agentLatestVersion = agent.LatestVersion

// SetAgentLatestVersion replaces the agent-latest-version seam and returns the
// previous value. Tests use it to keep the upgrade check off the network.
//
//deadcode:ignore reason="test seam replacing the agent latest-version lookup"
func SetAgentLatestVersion(
	f func(ctx context.Context, a agent.Agent) (string, error),
) func(ctx context.Context, a agent.Agent) (string, error) {
	orig := agentLatestVersion
	agentLatestVersion = f
	return orig
}

// existingAgentVersion reports the agent release baked into a project's existing
// runner image, read from its identity label. A package seam so tests do not
// touch a real docker daemon.
var existingAgentVersion = image.ExistingAgentVersion

// resolveBuildVersion decides the agent version to bake into the runner
// image before the image is built, so a normal run never builds twice (once
// for the current version and once for an upgrade). It returns the target
// version, whether that version is a freshly chosen upgrade, and the release
// whose prompt was shown this run (empty when none was offered).
//
// A user-provided agent wins over an explicit pin: the tool does not own such an
// agent, so a rebuild would not change its version and any pin is ignored. An
// explicit pin on a tool-managed agent skips the update check entirely. An agent
// without an upgrade checker uses the current recorded version without any check.
// Otherwise the update check runs first and may offer to rebuild with a newer
// release, deciding the final version up front. A forced rebuild (--rebuild) only
// bypasses the cache; it does not suppress the update check.
//
// The current version comes from this project's per-(slug, agent) HomeState, so
// the prompt can only ever name this project's version, never another project's.
// It is reconciled against this project's existing image label before the check
// (see recoverAgentVersion): a missing baseline is recovered, and one that
// disagrees with a present image is a stale cache the label overrides.
//
// The offered release is returned because the offered set must be persisted
// after the home volume is resolved: pendingUpgrade can only record it when a
// state file already exists, and a state lost before this run is recreated
// later in PrepareSandbox. Without the deferred record, a "don't remind again"
// choice would be forgotten for that project.
func resolveBuildVersion(
	ctx context.Context,
	k state.Key,
	a agent.Agent,
	ui termio.UI,
	opts options.RunOptions,
) (string, bool, string, error) {
	// A user-provided agent is not owned by the tool: a rebuild would not
	// change its version, so never check or offer an upgrade, and ignore any pin.
	// If no version was recorded, leave it empty so the image build can resolve a
	// real version if the current base image does not provide the agent.
	if version, userProvided := ResolveAgentVersionForBuild(k, opts.AgentVersion); userProvided {
		return version, false, "", nil
	}

	// An explicitly pinned version is authoritative: never prompt for an upgrade.
	if opts.AgentVersion != "" {
		return opts.AgentVersion, false, "", nil
	}

	// Without an upgrade checker there is nothing to check against; reuse the
	// recorded version so the image identity stays stable.
	if _, ok := agent.AsUpgradeChecker(a); !ok {
		return currentAgentVersion(k), false, "", nil
	}

	current := recoverAgentVersion(ctx, k, currentAgentVersion(k), ui)
	if current == "" {
		// No recorded baseline and no existing image to recover one from (e.g.,
		// first run): resolve latest at build time rather than prompting against
		// an unknown version.
		return "", false, "", nil
	}

	latest, offer := pendingUpgrade(ctx, a, k, ui, current)
	if !offer {
		return current, false, "", nil
	}

	chosen, promptErr := promptUpgrade(ui, a, current, latest)
	if promptErr != nil {
		return "", false, "", promptErr
	}
	if chosen == latest {
		return latest, true, latest, nil
	}
	return current, false, latest, nil
}

// ResolveAgentVersionForBuild applies the one agent-version precedence shared by
// the standalone build and a run. A user-provided agent is not owned by the
// tool, so any requested version (an explicit --agent-version pin) is ignored in
// favor of the recorded version and the caller must treat the build as
// user-provided. Every other project honors the requested version as-is.
func ResolveAgentVersionForBuild(k state.Key, requestedVersion string) (string, bool) {
	if currentAgentSource(k) == agentSourceUser {
		return currentAgentVersion(k), true
	}
	return requestedVersion, false
}

// promptUpgrade offers to rebuild the runner image with a newer release. It
// returns the chosen version, or an error when the user aborts. A
// non-interactive session logs the availability but keeps the current version.
func promptUpgrade(ui termio.UI, a agent.Agent, current, latest string) (string, error) {
	if !ui.IsInteractive() {
		ui.Infof(
			"%s %s available (image has %s); run 'agents-sandbox build' to upgrade",
			a.Name(),
			latest,
			current,
		)
		return current, nil
	}

	prompt := fmt.Sprintf("Rebuild the runner image with %s %s? (current image has %s)", a.Name(), latest, current)
	choices := []termio.Choice{
		{Label: "Rebuild", Key: "r", Description: "Upgrade " + a.Name() + " " + current + " → " + latest},
		{Label: "Keep", Key: "k", Description: "Keep " + a.Name() + " " + current + ", don't remind again"},
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
// offered for rebuild. The network check and its cached latest version are
// machine-global (RootState), while the once-per-version offered gate is
// per-project (HomeState), so the prompt never names another project's version.
//
// The two documents are written in two separate transactions, never nested: the
// host-only root lock is taken only to read and to write the network-check
// bookkeeping, never across the version request itself (see checkAgentUpgrade),
// and is released before the home transaction records the offered mark. Within
// the home document, reading the offered set and appending the new release happen
// in one transaction, so the offer is claimed atomically and concurrent launches
// cannot both prompt. A held root lock (e.g. an in-flight runtime-home copy
// campaign) defers the whole step to the next run rather than racing it. An
// offline or failed check never fails the session and leaves the once-per-day
// window open.
func pendingUpgrade(
	ctx context.Context,
	a agent.Agent,
	k state.Key,
	ui termio.UI,
	currentVersion string,
) (string, bool) {
	checker, hasChecker := agent.AsUpgradeChecker(a)

	entry, checked := checkAgentUpgrade(ctx, a, hasChecker, ui)
	if !checked || !hasChecker {
		return "", false
	}
	latest := entry.LatestVersion
	if latest == "" {
		return "", false
	}
	newer, newerErr := checker.NewerThan(latest, currentVersion)
	if newerErr != nil || !newer {
		// An unparsable version never fails the session.
		return "", false
	}

	// Claim the offer for this (project, release) atomically: check and mark
	// inside a single home transaction, so two concurrent launches of the same
	// project cannot both pass the "already offered" gate and show the prompt.
	// The transaction never creates a state — it runs before home resolution, so
	// creating one here would leave a fingerprint-less partial state that poisons
	// that step's recovery (#183). When no state exists yet we still offer; the
	// deferred persistOfferedVersion records the mark after home resolution.
	offer := false
	if writeErr := state.WithHomeState(k, func(hs *state.HomeState, exists bool) (bool, error) {
		if !exists {
			offer = true
			return false, nil
		}
		if hs.Offered(latest) {
			return false, nil
		}
		hs.MarkOffered(latest)
		offer = true
		return true, nil
	}); writeErr != nil {
		ui.Warnf("could not read or persist project state: %v (continuing)", writeErr)
	}
	if !offer {
		return "", false
	}
	return latest, true
}

// checkAgentUpgrade runs the machine-global version check and returns this
// agent's cached check state. The host-only root lock is never held across the
// version request: the cached state is read under a non-blocking transaction to
// decide whether the check is due, the lock is released for the request, and the
// result is persisted under a second non-blocking transaction that re-checks
// DueForCheck first, so two concurrent due checks persist at most one refresh.
//
// The bool is true whenever the cached state was read — even for a checker-less
// agent, in which case the returned state is the zero value (no entry is ever
// persisted without a checker), so a caller must also consult whether a checker
// exists (as pendingUpgrade does). The bool is false when the check could not run
// — the lock was held, the root state was unreadable, or the version request
// failed — and the caller must then defer the upgrade step. A root-state write
// failure is warned and does not suppress the offer: the freshly fetched version
// is still used this run.
func checkAgentUpgrade(
	ctx context.Context,
	a agent.Agent,
	hasChecker bool,
	ui termio.UI,
) (state.AgentCheckState, bool) {
	entry, ok := readAgentCheck(a, ui)
	if !ok {
		return state.AgentCheckState{}, false
	}
	if !hasChecker || !entry.DueForCheck(now()) {
		return entry, true
	}

	latest, latestErr := agentLatestVersion(ctx, a)
	if latestErr != nil {
		// Offline or unreachable: never fail the session, and leave the
		// once-per-day window open so the next online run retries.
		ui.Warnf("could not check for %s updates: %v", a.Name(), latestErr)
		return state.AgentCheckState{}, false
	}
	// A successful check refreshes the once-per-day window regardless of whether
	// an upgrade is available.
	refreshed := state.AgentCheckState{LastChecked: now(), LatestVersion: latest}
	return recordAgentCheck(a, refreshed, ui)
}

// readAgentCheck reads this agent's cached machine-global check state under a
// non-blocking root transaction. The bool is false when the root lock is held or
// the root state is unreadable, so the caller defers the step to the next run.
func readAgentCheck(a agent.Agent, ui termio.UI) (state.AgentCheckState, bool) {
	var entry state.AgentCheckState
	ok, err := state.WithRootState(false, func(txn *state.RootTxn) error {
		entry = txn.State().AgentCheck(a.Name())
		return nil
	})
	if err != nil {
		// An unreadable root state must never be overwritten: a zero-value
		// rewrite would discard the migrations registry and every other agent's
		// cached check.
		ui.Warnf("could not read upgrade check state (root lock/read failed): %v (skipping check)", err)
		return state.AgentCheckState{}, false
	}
	return entry, ok
}

// recordAgentCheck persists a refreshed check state under a non-blocking root
// transaction, unless another process already refreshed the window while the
// version request ran. In that case the write is skipped and the freshest cached
// entry is returned, so concurrent due checks persist at most one refresh. A held
// lock or unreadable state defers the step; a write failure is warned but the
// freshly fetched entry is still returned, so the offer is not suppressed.
func recordAgentCheck(a agent.Agent, refreshed state.AgentCheckState, ui termio.UI) (state.AgentCheckState, bool) {
	entry := refreshed
	ok, err := state.WithRootState(false, func(txn *state.RootTxn) error {
		current := txn.State().AgentCheck(a.Name())
		if !current.DueForCheck(now()) {
			// Another process refreshed the window while the version request ran:
			// keep its fresher entry and write nothing.
			entry = current
			return nil
		}
		txn.State().SetAgentCheck(a.Name(), entry)
		if commitErr := txn.Commit(); commitErr != nil {
			ui.Warnf("could not persist upgrade check state: %v (continuing)", commitErr)
		}
		return nil
	})
	if err != nil {
		ui.Warnf("could not read upgrade check state (root lock/read failed): %v (skipping check)", err)
		return state.AgentCheckState{}, false
	}
	return entry, ok
}

// currentAgentVersion returns the agent version recorded for this project's
// image, or "" when none has been recorded or the state cannot be read.
func currentAgentVersion(k state.Key) string {
	hs, err := state.ReadState(k)
	if err != nil {
		return ""
	}
	return hs.AgentVersion
}

// currentAgentSource returns the agent-source recorded for this project's
// image, or "" when none has been recorded or the state cannot be read.
func currentAgentSource(k state.Key) string {
	hs, err := state.ReadState(k)
	if err != nil {
		return ""
	}
	return hs.AgentSource
}

// recoverAgentVersion reconciles a project's upgrade baseline with the release
// its existing runner image reports. A missing image (or a non-release sentinel)
// leaves the recorded value as the authoritative offline fallback; a label that
// disagrees with the recorded value wins, because the image is what is actually
// installed, and the cache is rewritten. It returns the version to build.
func recoverAgentVersion(ctx context.Context, k state.Key, recorded string, ui termio.UI) string {
	version, ok := existingAgentVersion(ctx, k.Slug, k.Agent)
	if !ok || version == recorded {
		return recorded
	}
	if err := state.RecordAgentVersion(k, version); err != nil {
		ui.Verbosef("could not record recovered agent version: %v", err)
	}
	return version
}
