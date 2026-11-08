package pruning

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// RemovalPlan is the ordered, deduped set of artifacts a targeted purge removes
// for one project: the slugs, the project/agent keys, and the sandboxes, home
// volumes, runner images, state files, claims and root-state adoption claims
// that belong to them. It is a plan, not a promise: Execute re-resolves the
// live sandboxes, so a resource that disappears between planning and execution
// is a no-op rather than an error.
//
// The slices are sorted for stable output. Claims are keyed "slug/token" and
// adoption claims are keyed "slug/agent" (the root-state claim key), so both
// stay unambiguous when a plan spans more than one slug.
//
//exhaustruct:ignore
type RemovalPlan struct {
	Slugs          []string
	Keys           []state.Key
	Sandboxes      []string
	Volumes        []string
	Images         []string
	StateFiles     []string
	Claims         []string
	AdoptionClaims []string

	// scope is the slug/agent selection the plan targets. Execute re-derives
	// home-volume ownership from it under the root lock, so a volume another
	// checkout adopts while the confirmation prompt is open is refused.
	scope purgeScope

	// hasRunningSandbox records whether any planned sandbox was active at plan
	// time. HasRunningSandbox reports it so a caller can fail fast before
	// prompting; Execute re-checks the live status before deleting anything.
	hasRunningSandbox bool
}

// purgeScope is the slug/agent selection one purge targets: the project slugs
// to remove and an optional agent narrowing each to a single key.
type purgeScope struct {
	slugs map[string]struct{}
	agent string
}

// ownsSlug reports whether a project slug is inside the purge.
func (s purgeScope) ownsSlug(slug string) bool {
	_, ok := s.slugs[slug]
	return ok
}

// ownsKey reports whether a project/agent key falls inside the purge: its slug
// is targeted and, when an agent is given, its agent matches (an empty agent
// matches every agent, including the legacy agent-less key).
func (s purgeScope) ownsKey(k state.Key) bool {
	if !s.ownsSlug(k.Slug) {
		return false
	}
	return s.agent == "" || s.agent == k.Agent
}

// PlanRemoval enumerates everything one project owns. slugs are the on-disk
// project slugs to purge; agent selects a single key ("", all keys of every
// slug). Enumeration is the artifact union — state directories, home volumes,
// runner images, launcher-owned sandboxes and root-state adoption claims — so a
// slug whose state directory was deleted by hand or whose state was lost is
// still planned from its leftover volume, image or claim. Foreign artifacts are
// never planned: volumes, images and sandboxes are filtered through the naming
// ownership predicates. The result is deduped and sorted for stable output.
//
// Home-volume ownership is not the name's alone: a legacy identity adoption
// moves the state to a checkout slug without renaming its HomeVolume, and a
// root-state adoption claim records the checkout that owns a lost-state volume.
// A volume is therefore planned when its name parses into the scope, when a
// state key in scope references it, or when an in-scope checkout's adoption
// claim references it, and it is refused (with a warning through ui) when a state
// key or checkout outside the scope still owns it, so a purge can never drop
// another project's saved home.
func PlanRemoval(ctx context.Context, slugs []string, agent string, ui termio.Logger) (*RemovalPlan, error) {
	scope := purgeScope{slugs: slugSetOf(slugs), agent: agent}
	keys := make(map[state.Key]struct{})
	addKey := plannedKeyAdder(keys, agent)

	if err := planStateKeys(scope.slugs, addKey); err != nil {
		return nil, err
	}

	root, err := state.ReadRootState()
	if err != nil {
		return nil, err
	}

	client := msb.Get()
	volumes, err := planVolumes(ctx, client, scope, root, addKey, ui)
	if err != nil {
		return nil, err
	}
	images, err := planImages(ctx, client, scope.slugs, agent, addKey)
	if err != nil {
		return nil, err
	}
	sandboxes, hasRunning, err := planSandboxes(ctx, client, scope.slugs, agent, addKey)
	if err != nil {
		return nil, err
	}
	adoptionClaims := planAdoptionClaims(scope, root, addKey)
	stateFiles, err := planStateFiles(keys)
	if err != nil {
		return nil, err
	}
	claims, err := planClaims(scope.slugs)
	if err != nil {
		return nil, err
	}

	return &RemovalPlan{
		Slugs:             sortedStrings(scope.slugs),
		Keys:              sortedKeys(keys),
		Sandboxes:         sortedStrings(sandboxes),
		Volumes:           sortedStrings(volumes),
		Images:            sortedStrings(images),
		StateFiles:        sortedStrings(stateFiles),
		Claims:            sortedStrings(claims),
		AdoptionClaims:    sortedStrings(adoptionClaims),
		scope:             scope,
		hasRunningSandbox: hasRunning,
	}, nil
}

// HasRunningSandbox reports whether any planned sandbox was active when the
// plan was built. A caller uses it to fail fast before prompting; Execute
// re-checks the live status and refuses to delete anything until --force.
func (p *RemovalPlan) HasRunningSandbox() bool {
	return p.hasRunningSandbox
}

// Execute performs the purge. The order is binding because it must never orphan
// a home volume or drop the last pointer to one:
//
//  1. stale state, unheld claims and stale client leases for the keys — an
//     unreadable or corrupt state aborts here, before anything is deleted;
//  2. the slug's runner images;
//  3. the sandboxes, a running one stopped first (which requires force);
//  4. the home volumes and the adoption claims of the ones removed, under the
//     root lock, re-deriving ownership so a volume another checkout adopted
//     since planning is refused and a surviving volume keeps its claim;
//  5. the remaining state, except a key whose state still records a volume that
//     was not removed (a failed removal, or one the plan never enumerated);
//  6. the slug's now-empty state directories.
//
// dryRun performs no deletion and returns nil (the caller renders the plan). A
// running sandbox without force fails fast and deletes nothing. A failed volume
// removal warns, keeps the key's state file (the pointer survives), joins the
// error, and lets the other keys continue.
func (p *RemovalPlan) Execute(ctx context.Context, force, dryRun bool, ui termio.Logger) error {
	if p == nil {
		return nil
	}
	if dryRun {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	client := msb.Get()

	// Resolve the planned sandboxes up front so both guards — the running-VM
	// refusal and the fail-closed state read — run before any deletion.
	handles, err := p.resolveSandboxes(ctx, client)
	if err != nil {
		return err
	}
	if !force {
		if err := refuseRunningSandbox(handles); err != nil {
			return err
		}
	}

	// Step 1: stale state, unheld claims and stale leases. Fail closed.
	if err := p.sweepStaleState(); err != nil {
		return err
	}

	// Steps 2-7, joining a per-artifact failure so the rest still complete.
	var errs []error
	errs = append(errs, p.removeImages(ctx, client, ui)...)
	errs = append(errs, removeSandboxes(ctx, handles, ui)...)

	removedVolumes, volumeErrs := p.removeVolumes(ctx, client, ui)
	errs = append(errs, volumeErrs...)
	errs = append(errs, p.removeState(removedVolumes, ui)...)

	errs = append(errs, p.removeEmptyDirs(ui)...)

	return errors.Join(errs...)
}

// slugSetOf dedupes the requested slugs, dropping the empty string.
func slugSetOf(slugs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		if slug != "" {
			set[slug] = struct{}{}
		}
	}
	return set
}

// plannedKeyAdder returns an add-to-set function that skips the keys of agents
// other than agent; an empty agent keeps every key.
func plannedKeyAdder(keys map[state.Key]struct{}, agent string) func(state.Key) {
	return func(k state.Key) {
		if agent != "" && k.Agent != agent {
			return
		}
		keys[k] = struct{}{}
	}
}

// planStateKeys adds the keys found under each slug's state directory: its
// per-agent key directories and the legacy agent-less file.
func planStateKeys(slugSet map[string]struct{}, addKey func(state.Key)) error {
	for slug := range slugSet {
		agents, err := state.StatefulAgentNames(slug)
		if err != nil {
			return err
		}
		for _, name := range agents {
			addKey(state.Key{Slug: slug, Agent: name})
		}
		legacyExists, err := stateFileExists(state.Key{Slug: slug, Agent: ""})
		if err != nil {
			return err
		}
		if legacyExists {
			addKey(state.Key{Slug: slug, Agent: ""})
		}
	}
	return nil
}

// planVolumes returns the home volumes the purge owns. A volume is owned when
// its name parses into the scope, when a state key in the scope references it,
// or when an in-scope checkout holds the adoption claim for it. A volume an
// out-of-scope state key or checkout still owns is refused with a warning, so
// purging a legacy slug can never delete another checkout's saved home and
// purging a checkout reclaims the legacy-named volume its state points at.
// agent scopes the deletion set exactly as it scopes the keys.
func planVolumes(
	ctx context.Context,
	client msb.Client,
	scope purgeScope,
	root *state.RootState,
	addKey func(state.Key),
	ui termio.Logger,
) (map[string]struct{}, error) {
	handles, err := client.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	references, err := state.HomeVolumeReferences()
	if err != nil {
		return nil, err
	}
	ownedInScope, ownedElsewhere := classifyVolumeOwners(scope, references, root)
	volumes := make(map[string]struct{})
	for _, handle := range handles {
		name := handle.Name()
		artifact := naming.ParseHomeVolumeName(name)
		nameOwned := artifact.Slug != "" && scope.ownsSlug(artifact.Slug) &&
			(scope.agent == "" || artifact.Agent == scope.agent)
		if !nameOwned {
			if _, referenced := ownedInScope[name]; !referenced {
				continue
			}
		}
		if _, blocked := ownedElsewhere[name]; blocked {
			ui.Warnf("keeping home volume %s: still owned by another project", name)
			continue
		}
		if nameOwned {
			addKey(state.Key{Slug: artifact.Slug, Agent: artifact.Agent})
		}
		volumes[name] = struct{}{}
	}
	return volumes, nil
}

// classifyVolumeOwners maps every volume name to the scope that owns it: a
// state key in scope or an in-scope checkout's adoption claim puts a name in
// owned, while an out-of-scope state key or checkout puts it in elsewhere. A
// name can be in both, and elsewhere always wins the removal decision, so an
// in-scope pointer never overrides an outside owner.
func classifyVolumeOwners(
	scope purgeScope,
	references map[string][]state.Key,
	root *state.RootState,
) (map[string]struct{}, map[string]struct{}) {
	owned := map[string]struct{}{}
	elsewhere := map[string]struct{}{}
	for name, keys := range references {
		for _, key := range keys {
			if scope.ownsKey(key) {
				owned[name] = struct{}{}
			} else {
				elsewhere[name] = struct{}{}
			}
		}
	}
	for claimKey, claim := range root.AdoptedHomeVolumes {
		if claim.Volume == "" {
			continue
		}
		// The claim's owner is the checkout, not the legacy identity the claim
		// is keyed under: purging the checkout reclaims the volume, purging the
		// legacy slug leaves the checkout's saved home untouched.
		if scope.ownsClaim(claimKey, claim) {
			owned[claim.Volume] = struct{}{}
		} else {
			elsewhere[claim.Volume] = struct{}{}
		}
	}
	return owned, elsewhere
}

// ownsClaim reports whether the scope owns an adoption claim: the claim holds
// the target checkout and, when an agent is given, the claim's agent matches.
// The legacy identity the claim is keyed under does not scope ownership — a
// checkout's claim belongs to that checkout and to the key it names.
func (s purgeScope) ownsClaim(claimKey string, claim state.AdoptedHomeVolume) bool {
	if !s.ownsSlug(claim.CheckoutSlug) {
		return false
	}
	if s.agent == "" {
		return true
	}
	_, claimAgent, ok := strings.Cut(claimKey, "/")
	return ok && claimAgent == s.agent
}

// planImages returns the slug set's runner-image references; a reference that is
// not a launcher runner image is ignored. As with planVolumes, agent scopes the
// deletion set so a single-key purge never deletes another agent's image.
func planImages(
	ctx context.Context,
	client msb.Client,
	slugSet map[string]struct{},
	agent string,
	addKey func(state.Key),
) (map[string]struct{}, error) {
	handles, err := client.ImageList(ctx)
	if err != nil {
		return nil, err
	}
	images := make(map[string]struct{})
	for _, handle := range handles {
		ref := handle.Reference()
		if !naming.IsRunnerImageRef(ref) {
			continue
		}
		artifact := naming.ParseImageTag(ref)
		if _, owned := slugSet[artifact.Slug]; artifact.Slug == "" || !owned {
			continue
		}
		if agent != "" && artifact.Agent != agent {
			continue
		}
		addKey(state.Key{Slug: artifact.Slug, Agent: artifact.Agent})
		images[ref] = struct{}{}
	}
	return images, nil
}

// planSandboxes returns the slug set's launcher-owned sandboxes and whether any
// is active. A foreign sandbox whose name carries no launcher slug hash is
// ignored; a task sandbox of the selected agent is included.
func planSandboxes(
	ctx context.Context,
	client msb.Client,
	slugSet map[string]struct{},
	agent string,
	addKey func(state.Key),
) (map[string]struct{}, bool, error) {
	handles, err := client.ListSandboxes(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	sandboxes := make(map[string]struct{})
	hasRunning := false
	for _, handle := range handles {
		labels := sandboxLabels(handle)
		if !naming.IsLauncherSandbox(handle.Name(), labels) {
			continue
		}
		identity := naming.SandboxIdentity(handle.Name(), labels)
		if _, owned := slugSet[identity.Slug]; identity.Slug == "" || !owned {
			continue
		}
		if agent != "" && identity.Agent != agent {
			continue
		}
		addKey(state.Key{Slug: identity.Slug, Agent: identity.Agent})
		sandboxes[handle.Name()] = struct{}{}
		if msb.IsSandboxActive(handle.Status()) {
			hasRunning = true
		}
	}
	return sandboxes, hasRunning, nil
}

// planAdoptionClaims returns the root-state adoption claims the purge owns. A
// claim is keyed by the legacy identity but owned by the checkout that adopted
// the volume, so it is planned when the purge owns that checkout (and, under an
// agent scope, that agent's claim). This is what keeps purging a legacy slug
// from deleting a live checkout's recovery marker, and what lets purging a
// checkout retire its own claims.
func planAdoptionClaims(
	scope purgeScope,
	root *state.RootState,
	addKey func(state.Key),
) map[string]struct{} {
	claims := make(map[string]struct{})
	for claimKey, claim := range root.AdoptedHomeVolumes {
		if !scope.ownsClaim(claimKey, claim) {
			continue
		}
		slug, claimAgent, ok := strings.Cut(claimKey, "/")
		if !ok {
			continue
		}
		if scope.ownsKey(state.Key{Slug: slug, Agent: claimAgent}) {
			addKey(state.Key{Slug: slug, Agent: claimAgent})
		}
		claims[claimKey] = struct{}{}
	}
	return claims
}

// planStateFiles returns the paths of the keys' state files that exist on disk.
func planStateFiles(keys map[state.Key]struct{}) (map[string]struct{}, error) {
	files := make(map[string]struct{})
	for key := range keys {
		exists, err := stateFileExists(key)
		if err != nil {
			return nil, err
		}
		if exists {
			files[stateFilePath(key)] = struct{}{}
		}
	}
	return files, nil
}

// planClaims returns the slug set's claim tokens, keyed "slug/token".
func planClaims(slugSet map[string]struct{}) (map[string]struct{}, error) {
	claims := make(map[string]struct{})
	for slug := range slugSet {
		tokens, err := state.ClaimTokens(slug)
		if err != nil {
			return nil, err
		}
		for _, token := range tokens {
			claims[slug+"/"+token] = struct{}{}
		}
	}
	return claims, nil
}

// resolveSandboxes resolves the planned sandbox names to live handles,
// skipping one that no longer exists.
func (p *RemovalPlan) resolveSandboxes(ctx context.Context, client msb.Client) ([]msb.SandboxHandle, error) {
	handles := make([]msb.SandboxHandle, 0, len(p.Sandboxes))
	for _, name := range p.Sandboxes {
		handle, err := client.GetSandbox(ctx, name)
		if err != nil {
			if msb.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("resolve sandbox %s: %w", name, err)
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

// refuseRunningSandbox returns an error naming --force when any handle is
// active, so the purge deletes nothing.
func refuseRunningSandbox(handles []msb.SandboxHandle) error {
	for _, handle := range handles {
		if msb.IsSandboxActive(handle.Status()) {
			return fmt.Errorf(
				"sandbox %q is running; stop it first or pass --force to purge it",
				handle.Name(),
			)
		}
	}
	return nil
}

// sweepStaleState reclaims the safe-tier internal state of the planned keys:
// unheld claims for each slug, stale client leases per key, and a state file
// that records no home volume. An unreadable or unparsable state is returned as
// an error so the purge fails closed before any deletion.
func (p *RemovalPlan) sweepStaleState() error {
	for _, slug := range p.Slugs {
		tokens, err := state.ClaimTokens(slug)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if _, err := state.RemoveUnheldClaim(slug, token); err != nil {
				return err
			}
		}
	}
	for _, key := range p.Keys {
		state.SweepClientLeases(key)
		if err := state.RemoveHomeStateIf(key, stateHasNoHomeVolume); err != nil {
			return err
		}
	}
	return nil
}

// removeImages removes the planned runner images, warning and joining a failure.
func (p *RemovalPlan) removeImages(
	ctx context.Context,
	client msb.Client,
	ui termio.Logger,
) []error {
	var errs []error
	for _, ref := range p.Images {
		if err := client.ImageRemove(ctx, ref, true); err != nil {
			ui.Warnf("failed to remove runner image %s: %v", ref, err)
			errs = append(errs, err)
		}
	}
	return errs
}

// removeSandboxes removes the resolved sandboxes, stopping a running one first.
func removeSandboxes(ctx context.Context, handles []msb.SandboxHandle, ui termio.Logger) []error {
	var errs []error
	for _, handle := range handles {
		if msb.IsSandboxActive(handle.Status()) {
			if err := handle.Stop(ctx, msb.LifecycleOptions{}); err != nil {
				ui.Warnf("failed to stop sandbox %s: %v", handle.Name(), err)
				errs = append(errs, err)
				continue
			}
		}
		if err := handle.Remove(ctx); err != nil {
			ui.Warnf("failed to remove sandbox %s: %v", handle.Name(), err)
			errs = append(errs, err)
		}
	}
	return errs
}

// removeVolumes removes the planned home volumes and prunes the adoption claims
// of the ones it removed, returning the names removed successfully. Ownership is
// re-derived under the launcher-global root lock — the lock an identity adoption
// serializes on — so a volume another checkout adopts while the confirmation
// prompt is open is refused instead of deleted, and the lock is held across the
// removals so no adoption can interleave. A failed removal joins the error and
// continues; the name is absent from the returned set, so its key's state file
// and its adoption claim (the surviving home's owner record) are kept.
func (p *RemovalPlan) removeVolumes(
	ctx context.Context,
	client msb.Client,
	ui termio.Logger,
) (map[string]struct{}, []error) {
	removed := make(map[string]struct{})
	var errs []error
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		references, err := state.HomeVolumeReferences()
		if err != nil {
			return err
		}
		_, ownedElsewhere := classifyVolumeOwners(p.scope, references, txn.State())
		for _, name := range p.Volumes {
			if _, blocked := ownedElsewhere[name]; blocked {
				ui.Warnf("keeping home volume %s: adopted by another project while confirming", name)
				continue
			}
			if err := client.RemoveVolume(ctx, name); err != nil {
				ui.Warnf("failed to remove home volume %s: %v", name, err)
				errs = append(errs, err)
				continue
			}
			removed[name] = struct{}{}
		}
		if p.pruneSurvivingClaims(txn.State(), removed) {
			return txn.Commit()
		}
		return nil
	})
	if err != nil {
		ui.Warnf("failed to update home-volume ownership: %v", err)
		errs = append(errs, err)
	}
	return removed, errs
}

// pruneSurvivingClaims deletes the planned adoption claims whose volume this run
// removed or whose volume no longer exists, and keeps a claim whose volume
// survived a failed removal. The kept claim is the surviving home's only owner
// record when the checkout's state is gone, so dropping it would let another
// checkout claim a home that still exists. It reports whether the document
// changed.
func (p *RemovalPlan) pruneSurvivingClaims(
	root *state.RootState,
	removed map[string]struct{},
) bool {
	planned := make(map[string]struct{}, len(p.Volumes))
	for _, name := range p.Volumes {
		planned[name] = struct{}{}
	}
	changed := false
	for _, claimKey := range p.AdoptionClaims {
		claim, ok := root.AdoptedHomeVolumes[claimKey]
		if !ok {
			continue
		}
		if claim.Volume != "" {
			_, wasPlanned := planned[claim.Volume]
			_, wasRemoved := removed[claim.Volume]
			if wasPlanned && !wasRemoved {
				// The volume survived the purge: keep its owner record.
				continue
			}
		}
		delete(root.AdoptedHomeVolumes, claimKey)
		changed = true
	}
	return changed
}

// removeState removes the remaining state files. A state is kept whenever it
// still records a home volume that was not removed in this run — whether the
// removal failed or the volume was never in the plan — so the purge never drops
// the last pointer to a surviving volume.
func (p *RemovalPlan) removeState(removedVolumes map[string]struct{}, ui termio.Logger) []error {
	shouldRemove := stateRemovableAfterVolumes(removedVolumes)
	var errs []error
	for _, key := range p.Keys {
		if err := state.RemoveHomeStateIf(key, shouldRemove); err != nil {
			ui.Warnf("failed to remove state for key (slug %s, agent %s): %v", key.Slug, key.Agent, err)
			errs = append(errs, err)
		}
	}
	return errs
}

// removeEmptyDirs reclaims the slug's now-empty directory.
func (p *RemovalPlan) removeEmptyDirs(ui termio.Logger) []error {
	var errs []error
	for _, slug := range p.Slugs {
		if err := removeEmptyClaimsDir(slug); err != nil {
			ui.Warnf("failed to remove empty claims dir for %s: %v", slug, err)
			errs = append(errs, err)
		}
		if err := state.RemoveSlugDirIfEmpty(slug); err != nil {
			ui.Warnf("failed to remove empty state dir for %s: %v", slug, err)
			errs = append(errs, err)
		}
	}
	return errs
}

// stateRemovableAfterVolumes returns the tear-down predicate for step 5: a state
// is removable only when it records no home volume, or the volume it records was
// removed in this run's set. A state that still names a surviving volume — a
// failed removal, or a volume the plan never enumerated — is kept, so the purge
// never drops the last pointer to a volume that still exists.
func stateRemovableAfterVolumes(removedVolumes map[string]struct{}) func(*state.HomeState) bool {
	return func(cur *state.HomeState) bool {
		switch {
		case cur.HomeVolume != "":
			_, removed := removedVolumes[cur.HomeVolume]
			return removed
		case cur.RecoveredHomeVolume != "":
			_, removed := removedVolumes[cur.RecoveredHomeVolume]
			return removed
		default:
			return true
		}
	}
}

// stateFileExists reports whether a key has a state file on disk. A missing file
// is absence, not an error.
func stateFileExists(key state.Key) (bool, error) {
	if _, err := state.StateFileModTime(key); err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// stateFilePath is the path of a key's state file, including the legacy
// agent-less <slug>/state.yaml.
func stateFilePath(key state.Key) string {
	return filepath.Join(state.KeyDir(key), "state.yaml")
}

// sortedStrings returns the members of set in lexicographic order.
func sortedStrings(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
