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

	// hasRunningSandbox records whether any planned sandbox was active at plan
	// time. HasRunningSandbox reports it so a caller can fail fast before
	// prompting; Execute re-checks the live status before deleting anything.
	hasRunningSandbox bool
}

// PlanRemoval enumerates everything one project owns. slugs are the on-disk
// project slugs to purge; agent selects a single key ("", all keys of every
// slug). Enumeration is the artifact union — state directories, home volumes,
// runner images, launcher-owned sandboxes and root-state adoption claims — so a
// slug whose state directory was deleted by hand or whose state was lost is
// still planned from its leftover volume, image or claim. Foreign artifacts are
// never planned: volumes, images and sandboxes are filtered through the naming
// ownership predicates. The result is deduped and sorted for stable output.
func PlanRemoval(ctx context.Context, slugs []string, agent string) (*RemovalPlan, error) {
	slugSet := slugSetOf(slugs)
	keys := make(map[state.Key]struct{})
	addKey := plannedKeyAdder(keys, agent)

	if err := planStateKeys(slugSet, addKey); err != nil {
		return nil, err
	}

	client := msb.Get()
	volumes, err := planVolumes(ctx, client, slugSet, addKey)
	if err != nil {
		return nil, err
	}
	images, err := planImages(ctx, client, slugSet, addKey)
	if err != nil {
		return nil, err
	}
	sandboxes, hasRunning, err := planSandboxes(ctx, client, slugSet, agent, addKey)
	if err != nil {
		return nil, err
	}
	adoptionClaims, err := planAdoptionClaims(slugSet, agent, addKey)
	if err != nil {
		return nil, err
	}
	stateFiles, err := planStateFiles(keys)
	if err != nil {
		return nil, err
	}
	claims, err := planClaims(slugSet)
	if err != nil {
		return nil, err
	}

	return &RemovalPlan{
		Slugs:             sortedStrings(slugSet),
		Keys:              sortedKeys(keys),
		Sandboxes:         sortedStrings(sandboxes),
		Volumes:           sortedStrings(volumes),
		Images:            sortedStrings(images),
		StateFiles:        sortedStrings(stateFiles),
		Claims:            sortedStrings(claims),
		AdoptionClaims:    sortedStrings(adoptionClaims),
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
//  4. the home volumes;
//  5. the remaining state, except a key whose volume failed to remove;
//  6. the root-state adoption claims;
//  7. the slug's now-empty state directories.
//
// dryRun performs no deletion and returns nil (the caller renders the plan). A
// running sandbox without force fails fast and deletes nothing. A failed volume
// removal warns, keeps the key's state file (the pointer survives), joins the
// error, and lets the other keys continue.
func (p *RemovalPlan) Execute(ctx context.Context, force, dryRun bool, ui termio.UI) error {
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

	failedVolumes, volumeErrs := p.removeVolumes(ctx, client, ui)
	errs = append(errs, volumeErrs...)
	errs = append(errs, p.removeState(failedVolumes, ui)...)

	if err := p.removeAdoptionClaims(); err != nil {
		ui.Warnf("failed to remove root-state adoption claims: %v", err)
		errs = append(errs, err)
	}
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

// planVolumes returns the home volumes of the slug set; a foreign volume whose
// name does not parse to a launcher slug is ignored.
func planVolumes(
	ctx context.Context,
	client msb.Client,
	slugSet map[string]struct{},
	addKey func(state.Key),
) (map[string]struct{}, error) {
	handles, err := client.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	volumes := make(map[string]struct{})
	for _, handle := range handles {
		artifact := naming.ParseHomeVolumeName(handle.Name())
		if _, owned := slugSet[artifact.Slug]; artifact.Slug == "" || !owned {
			continue
		}
		addKey(state.Key{Slug: artifact.Slug, Agent: artifact.Agent})
		volumes[handle.Name()] = struct{}{}
	}
	return volumes, nil
}

// planImages returns the slug set's runner-image references; a reference that is
// not a launcher runner image is ignored.
func planImages(
	ctx context.Context,
	client msb.Client,
	slugSet map[string]struct{},
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

// planAdoptionClaims returns the root-state adoption claims of the slug set; the
// claim's legacy identity also contributes its key.
func planAdoptionClaims(
	slugSet map[string]struct{},
	agent string,
	addKey func(state.Key),
) (map[string]struct{}, error) {
	root, err := state.ReadRootState()
	if err != nil {
		return nil, err
	}
	claims := make(map[string]struct{})
	for claimKey := range root.AdoptedHomeVolumes {
		slug, claimAgent, ok := strings.Cut(claimKey, "/")
		if !ok {
			continue
		}
		if _, owned := slugSet[slug]; !owned {
			continue
		}
		if agent != "" && claimAgent != agent {
			continue
		}
		addKey(state.Key{Slug: slug, Agent: claimAgent})
		claims[claimKey] = struct{}{}
	}
	return claims, nil
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
			if err := handle.Stop(ctx); err != nil {
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

// removeVolumes removes the planned home volumes. A failed removal returns the
// volume's key so its state file is kept, so the pointer to a surviving volume
// is never dropped.
func (p *RemovalPlan) removeVolumes(
	ctx context.Context,
	client msb.Client,
	ui termio.Logger,
) (map[state.Key]struct{}, []error) {
	failed := make(map[state.Key]struct{})
	var errs []error
	for _, name := range p.Volumes {
		if err := client.RemoveVolume(ctx, name); err != nil {
			ui.Warnf("failed to remove home volume %s: %v", name, err)
			errs = append(errs, err)
			if artifact := naming.ParseHomeVolumeName(name); artifact.Slug != "" {
				failed[state.Key{Slug: artifact.Slug, Agent: artifact.Agent}] = struct{}{}
			}
		}
	}
	return failed, errs
}

// removeState removes the remaining state files, skipping a key whose volume
// failed to remove.
func (p *RemovalPlan) removeState(failedVolumes map[state.Key]struct{}, ui termio.Logger) []error {
	var errs []error
	for _, key := range p.Keys {
		if _, failed := failedVolumes[key]; failed {
			continue
		}
		if err := state.RemoveHomeStateIf(key, alwaysRemoveState); err != nil {
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

// removeAdoptionClaims drops the slug's root-state adoption claims, and only
// those: the launcher-global migrations and updates entries are never touched.
func (p *RemovalPlan) removeAdoptionClaims() error {
	if len(p.AdoptionClaims) == 0 {
		return nil
	}
	_, err := state.WithRootState(true, func(txn *state.RootTxn) error {
		before := len(txn.State().AdoptedHomeVolumes)
		for _, claimKey := range p.AdoptionClaims {
			delete(txn.State().AdoptedHomeVolumes, claimKey)
		}
		if len(txn.State().AdoptedHomeVolumes) == before {
			return nil
		}
		return txn.Commit()
	})
	return err
}

// alwaysRemoveState is the tear-down predicate for step 5: the volume is already
// gone (or its removal failed and the key was skipped), so the state file may be
// removed unconditionally.
func alwaysRemoveState(*state.HomeState) bool { return true }

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
