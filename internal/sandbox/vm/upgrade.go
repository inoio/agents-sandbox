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

// agentLatestVersion returns the newest release version for the agent via its
// own UpgradeChecker.
var agentLatestVersion = func(ctx context.Context, a agent.Agent) (string, error) {
	checker, ok := agent.AsUpgradeChecker(a)
	if !ok {
		return "", nil
	}
	return checker.LatestVersion(ctx)
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
// When the HomeState has no recorded baseline, it is recovered from this
// project's existing image label (see recoverAgentVersion).
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
	if currentAgentSource(k) == agentSourceUser {
		return currentAgentVersion(k), false, "", nil
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

	current := currentAgentVersion(k)
	if current == "" {
		current = recoverAgentVersion(ctx, k, ui)
	}
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
// host-only root lock is held only for the network-check bookkeeping (see
// checkAgentUpgrade), then released before the home transaction records the
// offered mark. A held root lock (e.g. an in-flight runtime-home copy campaign)
// defers the whole step to the next run rather than racing it. An offline or
// failed check never fails the session and leaves the once-per-day window open.
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

	hs, hsErr := state.ReadState(k)
	if hsErr != nil {
		if !errors.Is(hsErr, state.ErrStateNotFound) {
			ui.Warnf("could not read project state: %v (continuing)", hsErr)
			return "", false
		}
		hs = nil
	}
	if hs != nil && hs.Offered(latest) {
		return "", false
	}

	// Record the offered mark in a separate home transaction, never nested
	// inside the root lock. Update an existing state only: this runs before home
	// resolution, so creating a state here would leave a fingerprint-less
	// partial state that poisons that step's recovery (#183).
	if writeErr := state.WithHomeState(k, func(hs *state.HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		hs.MarkOffered(latest)
		return true, nil
	}); writeErr != nil {
		ui.Warnf("could not persist project state: %v (continuing)", writeErr)
	}
	return latest, true
}

// checkAgentUpgrade runs the machine-global network check under the host-only
// root transaction and returns this agent's cached check state. When no checker
// is available there is nothing to check, so it returns the zero state and
// false. Otherwise the bool is false when the check could not run — the lock was
// held, the root state was unreadable, or the network check failed — and the
// caller must then defer the upgrade step. A root-state write failure is warned
// and does not suppress the offer: the freshly fetched version is still used
// this run.
func checkAgentUpgrade(
	ctx context.Context,
	a agent.Agent,
	hasChecker bool,
	ui termio.UI,
) (state.AgentCheckState, bool) {
	var (
		entry       state.AgentCheckState
		checkFailed bool
	)
	ok, err := state.WithRootState(false, func(txn *state.RootTxn) error {
		root := txn.State()
		entry = root.AgentCheck(a.Name())
		if !hasChecker || !entry.DueForCheck(now()) {
			// Nothing to refresh: reuse the cached entry (if any). Not due, so no
			// write and no need to checkpoint.
			return nil
		}
		latest, latestErr := agentLatestVersion(ctx, a)
		if latestErr != nil {
			// Offline or unreachable: never fail the session, and leave the
			// once-per-day window open so the next online run retries.
			ui.Warnf("could not check for %s updates: %v", a.Name(), latestErr)
			checkFailed = true
			return nil
		}
		// A successful check refreshes the once-per-day window regardless of
		// whether an upgrade is available.
		entry.LastChecked = now()
		entry.LatestVersion = latest
		root.SetAgentCheck(a.Name(), entry)
		if commitErr := txn.Commit(); commitErr != nil {
			ui.Warnf("could not persist upgrade check state: %v (continuing)", commitErr)
		}
		return nil
	})
	if err != nil {
		// The error is either a lock-acquisition failure or an unreadable root
		// state. Either way, never persist over it: a zero-value rewrite would
		// discard the migrations registry and every other agent's cached check.
		ui.Warnf("could not read upgrade check state (root lock/read failed): %v (skipping check)", err)
		return state.AgentCheckState{}, false
	}
	if !ok || checkFailed {
		return state.AgentCheckState{}, false
	}
	return entry, true
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

// recoverAgentVersion recovers a project's upgrade baseline from its existing
// runner image when the recorded HomeState has none, and records it. This is the
// run after the updater.yaml migration dropped the machine-global
// current_version: without recovery the first unpinned run would resolve the
// latest release and silently rebuild the image (and fail offline); recovering
// the version restores the prompt-and-reuse behavior. It returns "" when no
// image or no release label exists, so a genuine first run still resolves at
// build time.
//
// The version is recorded only when a state file already exists. This runs
// before home resolution, so creating a state here would leave a
// fingerprint-less partial state that the home-recovery step would treat as a
// prior state and copy empty fingerprints forward, spuriously detecting a
// config change (#183). A failure only costs a re-inspection next run.
func recoverAgentVersion(ctx context.Context, k state.Key, ui termio.UI) string {
	version, ok := existingAgentVersion(ctx, k.Slug, k.Agent)
	if !ok {
		return ""
	}
	if err := state.WithHomeState(k, func(hs *state.HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		hs.AgentVersion = version
		return true, nil
	}); err != nil {
		ui.Verbosef("could not record recovered agent version: %v", err)
	}
	return version
}
