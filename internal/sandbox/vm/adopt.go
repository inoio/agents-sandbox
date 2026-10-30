package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// IdentityAdoption reports how a legacy origin-keyed project identity was
// adopted for the current checkout (#144).
//
//exhaustruct:ignore
type IdentityAdoption struct {
	// Slug is the effective project slug for the caller's remaining work: the
	// checkout slug, or, while a pending migration is left unapplied (a dry
	// run), the legacy slug the pending state was actually found under — which a
	// bare root may have under its path-derived identity rather than the repo
	// slug.
	Slug string
	// Pending reports whether a legacy origin-keyed identity was found and
	// needed adopting.
	Pending bool
	// Applied reports whether the state directory was moved onto the checkout
	// slug this call. False when nothing was pending or the caller asked for a
	// dry run.
	Applied bool
}

// adoptLockHook is a test seam invoked inside the locked adoption, before the
// trigger is re-checked. A test uses it to simulate another checkout adopting the
// shared legacy identity while this run waited for the root lock.
var adoptLockHook func()

// adoptKeysLockedHook is a test seam invoked inside the WithKeyLocks region,
// after every moved key's per-key locks are held: once before the destroy and
// again after the state move, so a test can observe the held locks and their
// release spanning both.
var adoptKeysLockedHook func()

// renameConfigDir is a seam over os.Rename for the legacy config-directory move.
// A same-parent rename cannot be made to fail deterministically without
// permission tricks, so tests force the failure through this seam.
var renameConfigDir = os.Rename

// AdoptLegacyIdentity moves a project from the legacy origin-keyed identity to
// its checkout-scoped one on the first run after upgrade (#144).
//
// Before the identity split, one origin-derived slug keyed the state directory,
// the VM, the home volume and the runner image, so two clones of one remote
// collided on a single VM. Adoption destroys the legacy VM first and then merges
// the state directory onto the checkout slug. That order is crash-safe: a crash
// in between leaves the legacy state in place, so the next run re-adopts and
// merely recreates the VM, while moving first would leave an untracked VM
// running against the shared home volume. The merge is the idempotence record —
// the legacy state file is gone afterwards, so adoption cannot re-fire.
//
// A bare root records a second legacy identity (its pre-split path-derived
// slug), so adoption merges from every legacy identity the caller resolved, in
// order. Migrating a directory moves every agent's state, so every moved agent's
// legacy VM is destroyed, not only the requested one; otherwise a still-running
// sibling agent would recreate itself over the same home volume.
//
// A live legacy VM is itself a legacy identity, even when its state file was
// lost (issue #219): without adopting it the run would treat the checkout as
// brand new, mint a fresh home volume and orphan the user's credentials and
// files. That arm has no state file to move, so adoption records the mounted
// home volume durably in the checkout state's RecoveredHomeVolume before it
// destroys the VM, and home resolution later recovers it into HomeVolume. The
// destroy is what makes the volume unreadable afterwards, so the reference has
// to be written first; recording it in memory (as this once did) loses it the
// moment the caller discards the return value, which every entry point except a
// successful run did (issue #244 review).
//
// With apply=false (a dry run) it prints the notice and leaves everything in
// place, returning the legacy slug so the rest of the pipeline keeps resolving
// against the pre-migration location.
//
// The destroy-and-move runs inside one root-state transaction, so two concurrent
// first-post-upgrade runs cannot both destroy and move. The trigger is
// re-checked while the lock is held: a second run that lost the race to another
// checkout sees the state already moved and continues normally under the
// checkout identity instead of failing. The destroy and move additionally hold
// the per-key home and project-VM locks (WithKeyLocks) of every key they touch,
// so a concurrent per-key state write or VM creation cannot interleave with them
// (#333).
func AdoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	agentName string,
	apply bool,
	ui termio.UI,
) (IdentityAdoption, error) {
	checkoutKey := state.Key{Slug: identity.CheckoutSlug, Agent: agentName}
	slugs := legacySlugs(identity)

	source, pending, err := legacyAdoptionPending(ctx, client, slugs, checkoutKey)
	if err != nil {
		return IdentityAdoption{}, fmt.Errorf("check for a pending legacy identity: %w", err)
	}
	if !pending {
		return IdentityAdoption{Slug: identity.CheckoutSlug}, nil
	}

	notAdopted := IdentityAdoption{Slug: source.Slug, Pending: true}
	if !apply {
		ui.Warnf("%s", adoptionNotice(identity, agentName, source.HomeVolume, false))
		// Resolve the caller's remaining work against the source the state was
		// actually found under, not always the repo slug: for a bare root the
		// pending state can live under the path-derived legacy slug, and a
		// dry-run that named the repo slug would read the wrong (absent) state.
		return notAdopted, nil
	}

	var result IdentityAdoption
	_, err = state.WithRootState(true, func(txn *state.RootTxn) error {
		// Re-check under the lock: another checkout (or another agent of this
		// one) may have adopted the shared legacy identity between the pre-lock
		// check and acquiring the lock. If so there is nothing left to adopt and
		// the caller continues normally, rather than tripping the
		// incomplete-migration failure on a source that is already gone.
		if adoptLockHook != nil {
			adoptLockHook()
		}
		source, pending, recheckErr := legacyAdoptionPending(ctx, client, slugs, checkoutKey)
		if recheckErr != nil {
			return fmt.Errorf("check for a pending legacy identity: %w", recheckErr)
		}
		if !pending {
			result = IdentityAdoption{Slug: identity.CheckoutSlug}
			return nil
		}
		ui.Warnf("%s", adoptionNotice(identity, agentName, source.HomeVolume, true))
		if adoptErr := adoptLegacyIdentity(
			ctx,
			client,
			identity,
			slugs,
			checkoutKey,
			source,
			txn,
			ui,
		); adoptErr != nil {
			return adoptErr
		}
		result = IdentityAdoption{Slug: identity.CheckoutSlug, Pending: true, Applied: true}
		return nil
	})
	if err != nil {
		return IdentityAdoption{}, err
	}
	return result, nil
}

// legacySlugs returns the ordered, de-duplicated origin-keyed identities to adopt
// from: the repo slug (the legacy identity of a normal clone or any worktree),
// followed by the extra identities a bare root records. The checkout slug is
// never a source: state already there needs no migration.
func legacySlugs(identity git.Identity) []string {
	seen := map[string]bool{identity.CheckoutSlug: true}
	var slugs []string
	for _, slug := range append([]string{identity.RepoSlug}, identity.LegacyRepoSlugs...) {
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)
	}
	return slugs
}

// adoptionLockKeys returns every (slug, agent) key whose per-key locks the
// adoption must hold: the directory move carries every agent's state from each
// legacy slug onto the checkout slug, so both the source key and the destination
// key of every moved agent are locked. The order is the caller's; WithKeyLocks
// sorts and de-duplicates.
func adoptionLockKeys(slugs []string, checkoutSlug, agentName string) ([]state.Key, error) {
	var keys []state.Key
	for _, slug := range slugs {
		agents, err := legacyAgentsToMigrate(slug, agentName)
		if err != nil {
			return nil, err
		}
		for _, agent := range agents {
			keys = append(keys, state.Key{Slug: slug, Agent: agent}, state.Key{Slug: checkoutSlug, Agent: agent})
		}
	}
	return keys, nil
}

// legacyPendingSource is where a pending legacy identity was found. FromVM marks
// an identity evidenced only by a surviving project VM whose state file was lost
// (issue #219); HomeVolume is the volume to preserve in either case.
type legacyPendingSource struct {
	Slug       string
	HomeVolume string
	FromVM     bool
}

// persistRecoveredHomeVolume records the home volume a to-be-destroyed legacy VM
// mounted in the checkout state, so home resolution can recover it after the VM
// (its only other reference) is gone. It flows through the recovery transaction,
// which repairs a missing or unparsable document; the trigger guarantees no
// settled checkout state exists yet, and a retried adoption rewrites its own
// marker. The key is not necessarily this checkout's: a checkout that does not
// own the volume still records it under the owner's key, materializing the
// owner's marker from the claim before it destroys the shared VM (see
// persistAdoptionOwnership). A VM with no home mount has nothing to preserve and
// writes nothing.
func persistRecoveredHomeVolume(k state.Key, homeVolume string) error {
	if homeVolume == "" {
		return nil
	}
	if err := state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		cur.RecoveredHomeVolume = homeVolume
		return true, nil
	}); err != nil {
		return fmt.Errorf("record the recovered home volume for %s: %w", k.Slug, err)
	}
	return nil
}

// claimLegacyHomeVolume records this checkout as the owner of a shared legacy
// identity's lost-state home volume, within the adoption root transaction. The
// boolean reports whether this checkout is that owner; false means another
// checkout already owns the identity's volume, in which case the returned claim
// names it (so the caller can materialize the owner's marker) and this checkout
// must not record one of its own. A claim by this checkout is re-recorded, so a
// retried adoption is idempotent — but only when it names the volume the live
// legacy VM actually mounts. A claim that disagrees with the observed volume,
// whoever holds it, is a state adoption cannot reconcile, so it fails closed
// instead of destroying the VM and stranding whatever the VM actually mounts.
func claimLegacyHomeVolume(
	txn *state.RootTxn,
	source legacyPendingSource,
	checkoutKey state.Key,
) (state.AdoptedHomeVolume, bool, error) {
	if owner, claimed := txn.State().AdoptedHomeVolume(source.Slug, checkoutKey.Agent); claimed {
		if owner.Volume != source.HomeVolume {
			return state.AdoptedHomeVolume{}, false, fmt.Errorf(
				"adoption claim for legacy identity %s records home volume %q but the live VM mounts %q: refusing to destroy it",
				source.Slug,
				owner.Volume,
				source.HomeVolume,
			)
		}
		if owner.CheckoutSlug != checkoutKey.Slug {
			return owner, false, nil
		}
	}
	txn.State().ClaimAdoptedHomeVolume(source.Slug, checkoutKey.Agent, checkoutKey.Slug, source.HomeVolume)
	return state.AdoptedHomeVolume{CheckoutSlug: checkoutKey.Slug, Volume: source.HomeVolume}, true, nil
}

// persistAdoptionOwnership durably records a shared legacy identity's lost-state
// home volume for this checkout, under the adoption root transaction: it claims
// the volume, commits the claim, then records the recovered reference in the
// checkout state. Both writes happen before the legacy VM is destroyed, so a
// destroy failure (or a caller that discards the adoption result) still leaves
// the volume owned and recoverable, and a marker write that fails cannot leave
// the volume recorded without an owner — which would let a second clone claim
// and recover the same volume. When another checkout already owns the volume,
// this checkout starts with its own fresh volume instead, but it first
// materializes the owner's recovery marker from the claim: it is about to destroy
// the legacy VM, the volume's only other reference, so without the marker the
// owner's retry would find neither a marker nor a VM and mint a fresh home. A
// failed restore fails the whole adoption closed, so a VM is never destroyed
// before its home volume has a durable reference.
func persistAdoptionOwnership(
	txn *state.RootTxn,
	source legacyPendingSource,
	checkoutKey state.Key,
) error {
	owner, own, err := claimLegacyHomeVolume(txn, source, checkoutKey)
	if err != nil {
		return err
	}
	if !own {
		return persistRecoveredHomeVolume(
			state.Key{Slug: owner.CheckoutSlug, Agent: checkoutKey.Agent},
			owner.Volume,
		)
	}
	if err := txn.Commit(); err != nil {
		return fmt.Errorf("record adoption ownership for %s: %w", source.Slug, err)
	}
	return persistRecoveredHomeVolume(checkoutKey, source.HomeVolume)
}

// legacyAdoptionPending reports whether a legacy identity is pending: the
// checkout-keyed state file is absent and either a legacy state file exists or a
// live legacy project VM for the agent still exists. It returns the slug the
// identity was found under (so a dry run can resolve read-only against the real
// source — a bare root's state may live under its path-derived slug, not the repo
// slug) and the volume to preserve.
//
// The state-file predicate is state-*file* absence, not directory absence, so a
// stray or aborted run that created the empty checkout directory cannot
// permanently skip migration, and it is re-evaluated on every run until it fires.
// It is deliberately first-adopter-wins: once the checkout state exists, later
// callers stop, which is the accepted asymmetry of moving one shared identity
// instead of copying it.
//
// The live-VM arm covers a legacy identity whose state file was lost while its
// VM survived: the VM is the remaining legacy artifact, and adopting it (rather
// than treating the checkout as new) is what preserves its home volume.
//
// It skips rather than aborts on a corrupt or unreadable *checkout* state: the
// checkout state is the caller's own document, and the caller's recovery path
// (RecoverHomeState, run after this) repairs it, so blocking the run would strand
// the user's live VM and home volume. Skipping leaves legacy VMs and state
// untouched, which is the fail-safe. Reads of the *legacy* state and a legacy
// VM's mounts do fail closed (returned as an error), so an unknown legacy
// document or an unreadable mount can never lead to destroying a legacy VM or
// moving state nobody has read.
//
// A checkout state is normally the settled record that stops adoption. The one
// exception is a state that carries a RecoveredHomeVolume: a prior VM-only
// adoption wrote that reference before tearing the legacy VM down, so if the
// destroy did not finish the trigger stays live and the next call retries it
// (the state move is a no-op then, because there is no legacy state file).
func legacyAdoptionPending(
	ctx context.Context,
	client msb.Client,
	slugs []string,
	checkoutKey state.Key,
) (legacyPendingSource, bool, error) {
	checkout, checkoutErr := state.ReadState(checkoutKey)
	switch {
	case checkoutErr == nil && checkout.RecoveredHomeVolume == "":
		return legacyPendingSource{}, false, nil
	case checkoutErr != nil && !errors.Is(checkoutErr, state.ErrStateNotFound):
		return legacyPendingSource{}, false, nil
	}

	for _, slug := range slugs {
		found, err := state.ReadState(state.Key{Slug: slug, Agent: checkoutKey.Agent})
		if err == nil {
			return legacyPendingSource{
				Slug:       slug,
				HomeVolume: found.HomeVolume,
				FromVM:     false,
			}, true, nil
		}
		if !errors.Is(err, state.ErrStateNotFound) {
			return legacyPendingSource{}, false, err
		}
		volume, exists, vmErr := projectVMHomeVolume(ctx, client, state.Key{Slug: slug, Agent: checkoutKey.Agent})
		if vmErr != nil {
			return legacyPendingSource{}, false, fmt.Errorf("check legacy project VM for %s: %w", slug, vmErr)
		}
		if exists {
			return legacyPendingSource{
				Slug:       slug,
				HomeVolume: volume,
				FromVM:     true,
			}, true, nil
		}
	}
	return legacyPendingSource{}, false, nil
}

// adoptLegacyIdentity destroys the legacy VMs and merges every legacy state
// directory onto the checkout slug. The VM-only arm's durable marker write runs
// first, under the root transaction; the destroy and the move then run holding
// every moved key's per-key locks (see adoptionLockKeys).
func adoptLegacyIdentity(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	slugs []string,
	checkoutKey state.Key,
	source legacyPendingSource,
	txn *state.RootTxn,
	ui termio.UI,
) error {
	// The VM-only arm destroys the only record of its home volume, so the
	// recovered reference must be durable first. See persistAdoptionOwnership.
	if source.FromVM && source.HomeVolume != "" {
		if err := persistAdoptionOwnership(txn, source, checkoutKey); err != nil {
			return err
		}
	}

	keys, err := adoptionLockKeys(slugs, checkoutKey.Slug, checkoutKey.Agent)
	if err != nil {
		return fmt.Errorf("derive adoption lock set: %w", err)
	}
	// Serialize the destroy and the directory move against every per-key state
	// writer and VM creator for the keys the move touches: the root lock (held
	// here) excludes another adoption, but not a per-key writer. Taken after
	// persistAdoptionOwnership so this checkout key's home lock is not held twice
	// in one process.
	return state.WithKeyLocks(keys, func() error {
		if adoptKeysLockedHook != nil {
			adoptKeysLockedHook()
		}
		return migrateLegacyState(ctx, client, identity, slugs, checkoutKey, source, ui)
	})
}

// migrateLegacyState destroys the legacy VMs and merges every legacy state
// directory onto the checkout slug, with the per-key locks for every moved key
// held (see adoptLegacyIdentity).
func migrateLegacyState(
	ctx context.Context,
	client msb.Client,
	identity git.Identity,
	slugs []string,
	checkoutKey state.Key,
	source legacyPendingSource,
	ui termio.UI,
) error {
	primary := slugs[0]

	// Destroy every legacy VM across all sources before moving any state. A
	// destroy failure then aborts the migration with every legacy state file
	// still in place, so the next run re-fires the trigger and resumes. Moving
	// first would create the checkout state file and, on a later destroy
	// failure, leave the trigger permanently satisfied: the retry would adopt
	// the first source's home and silently strand the other source's VM, home
	// volume and state.
	for _, slug := range slugs {
		if err := destroyLegacyVMs(ctx, client, slug, checkoutKey.Agent, ui); err != nil {
			return err
		}
	}

	var skipped []string
	for _, slug := range slugs {
		sourceSkipped, err := state.MoveSlugDir(slug, identity.CheckoutSlug)
		if err != nil {
			return fmt.Errorf("move project state to %s: %w", state.SlugDir(identity.CheckoutSlug), err)
		}
		for _, entry := range sourceSkipped {
			ui.Warnf("left %s in the legacy state directory %s", entry, state.SlugDir(slug))
		}
		skipped = append(skipped, sourceSkipped...)
	}

	if adoptKeysLockedHook != nil {
		adoptKeysLockedHook()
	}

	if err := migrateLegacyConfigDirs(identity, ui); err != nil {
		return err
	}

	// The merge is what makes the state-file trigger a real idempotence record:
	// it always delivers <agent>/state.yaml, because the trigger guarantees no
	// state file exists at the destination. If it did not arrive, continuing
	// would run under the checkout identity with no state, silently provisioning
	// a fresh home volume and losing the agent's auth. Failing loudly is the only
	// outcome that cannot lose the user's volume, so never fall back here. The
	// notice has already promised a move and the legacy VM is gone by now, so the
	// error has to say what is half-done and what to clear to finish it; the read
	// error is wrapped for a non-not-found failure (I/O, parse).
	//
	// A VM-only adoption has no legacy state file to arrive: home resolution
	// writes the recovered checkout state from the preserved volume instead, so
	// the check does not apply.
	if !source.FromVM {
		if _, verifyErr := state.ReadState(checkoutKey); verifyErr != nil {
			primaryKey := state.Key{Slug: primary, Agent: checkoutKey.Agent}
			return fmt.Errorf(
				"migration to the checkout identity did not complete: the legacy state file %s did not arrive at %s (the move skipped [%s], read error: %w); the legacy project VM %s is already gone, so clear those entries under the checkout state directory %s and rerun",
				filepath.Join(state.KeyDir(primaryKey), "state.yaml"),
				filepath.Join(state.KeyDir(checkoutKey), "state.yaml"),
				strings.Join(skipped, ", "),
				verifyErr,
				projectVMName(primaryKey),
				state.SlugDir(identity.CheckoutSlug),
			)
		}
	}
	return nil
}

// destroyLegacyVMs destroys the legacy origin-keyed project VM of every agent
// whose state lives under slug, plus the command's own agent. Destroying the
// whole set matters because the whole slug directory is moved: leaving a sibling
// agent's VM running would let it keep writing the shared home volume while a
// later run recreates that agent under the checkout name over the same volume.
func destroyLegacyVMs(ctx context.Context, client msb.Client, slug, agentName string, ui termio.UI) error {
	toDestroy, err := legacyAgentsToMigrate(slug, agentName)
	if err != nil {
		return err
	}
	for _, name := range toDestroy {
		if err := destroyLegacyVM(ctx, client, state.Key{Slug: slug, Agent: name}, ui); err != nil {
			return err
		}
	}
	return nil
}

// legacyAgentsToMigrate returns the agents whose legacy artifacts the move
// touches: every agent with state under slug (state.StatefulAgentNames), plus the
// command's own agent. The command's agent is included explicitly because a
// legacy VM whose state file was lost is not listed by StatefulAgentNames (issue
// #219), yet it is the VM that would otherwise keep running over the volume
// adoption preserves.
func legacyAgentsToMigrate(slug, agentName string) ([]string, error) {
	names, err := state.StatefulAgentNames(slug)
	if err != nil {
		return nil, fmt.Errorf("list legacy state for %s: %w", slug, err)
	}
	if slices.Contains(names, agentName) {
		return names, nil
	}
	return append(append([]string(nil), names...), agentName), nil
}

// destroyLegacyVM destroys the legacy origin-keyed project VM of one agent if it
// exists, through the shared identity-safe teardown (destroyProjectVMHandle): a
// name that is already gone needs no destroy, but a handle whose identity was
// replaced belongs to a sandbox this run did not create, so adopting it would
// destroy a stranger's VM and the adoption stops instead.
func destroyLegacyVM(ctx context.Context, client msb.Client, repoKey state.Key, ui termio.UI) error {
	name := projectVMName(repoKey)

	handle, found, err := findProjectVM(ctx, client, repoKey)
	if err != nil {
		return fmt.Errorf("check legacy project VM %q: %w", name, err)
	}
	if !found {
		ui.Verbosef("legacy project VM %s is already gone", name)
		return nil
	}
	name = handle.Name()

	alreadyGone, destroyErr := destroyProjectVMHandle(
		ctx,
		handle,
		name,
		"rerun to finish migrating this checkout",
	)
	if destroyErr != nil {
		return destroyErr
	}
	if alreadyGone {
		ui.Verbosef("legacy project VM %s was already gone; continuing with the state move", name)
	} else {
		ui.Verbosef("destroyed legacy project VM %s", name)
	}
	return nil
}

// migrateLegacyConfigDirs preserves a legacy identity's per-slug user config
// directory by renaming it onto the repo slug, the one the launcher reads from
// now. It only applies to a legacy identity that is not already the repo slug (a
// bare root's path-derived slug); a source whose slug is the repo slug already
// reads the right directory. An absent legacy directory, or an existing target,
// is left alone.
func migrateLegacyConfigDirs(identity git.Identity, ui termio.UI) error {
	paths := configpaths.Get()
	for _, slug := range identity.LegacyRepoSlugs {
		if slug == identity.RepoSlug {
			continue
		}
		from := paths.UserSlugConfigDir(slug)
		to := paths.UserSlugConfigDir(identity.RepoSlug)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if _, err := os.Stat(to); err == nil {
			ui.Verbosef("kept existing config dir %s; legacy config dir %s left in place", to, from)
			continue
		}
		if err := renameConfigDir(from, to); err != nil {
			return fmt.Errorf("move config dir %s to %s: %w", from, to, err)
		}
		ui.Verbosef("moved legacy config dir %s to %s", from, to)
	}
	return nil
}

// adoptionNotice renders the whole migration notice as one block: a headline,
// a reassurance line, and the indented detail rows. It is emitted before any
// work starts, so its rows are prospective ("destroyed", "recreated") rather
// than a receipt, and one Warnf call carries it because the notice must survive
// --quiet — it changes where the project's state lives.
func adoptionNotice(identity git.Identity, agentName, legacyVolume string, apply bool) string {
	headline := "Migrating this checkout to its own project identity (repository + checkout slugs)."
	if !apply {
		headline = "Would migrate this checkout to its own project identity (repository + checkout slugs)."
	}
	homeVolume := legacyVolume
	if homeVolume == "" {
		homeVolume = "(none recorded)"
	}

	var extraStateRows, configRow strings.Builder
	for _, slug := range identity.LegacyRepoSlugs {
		if slug == identity.CheckoutSlug {
			continue
		}
		fmt.Fprintf(
			&extraStateRows,
			"\n                %s  ->  %s",
			state.SlugDir(slug),
			state.SlugDir(identity.CheckoutSlug),
		)
	}
	if len(identity.LegacyRepoSlugs) > 0 {
		paths := configpaths.Get()
		fmt.Fprintf(
			&configRow,
			"\n  config dir    %s  ->  %s   (if present)",
			paths.UserSlugConfigDir(identity.LegacyRepoSlugs[0]),
			paths.UserSlugConfigDir(identity.RepoSlug),
		)
	}

	return fmt.Sprintf(`%s
No action needed: files, credentials and the home volume are kept; only the project VM is recreated.

  state dir     %s  ->  %s%s
  home volume   %s   (kept as-is)
  VM            %s   (destroyed)
                %s   (recreated)
  runner image  %s  ->  %s   (rebuilt)%s`,
		headline,
		state.SlugDir(identity.RepoSlug), state.SlugDir(identity.CheckoutSlug), extraStateRows.String(),
		homeVolume,
		projectVMName(state.Key{Slug: identity.RepoSlug, Agent: agentName}),
		projectVMName(state.Key{Slug: identity.CheckoutSlug, Agent: agentName}),
		naming.DockerImageRef(identity.RepoSlug, agentName),
		naming.DockerImageRef(identity.CheckoutSlug, agentName),
		configRow.String(),
	)
}
