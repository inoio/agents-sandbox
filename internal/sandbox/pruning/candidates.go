package pruning

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// Candidate is one project slug discovered from the artifact union, with the
// keys and signals that decide whether it is retired. Candidate slugs come from
// the union rather than state directories alone, so a project whose state
// directory was deleted by hand, whose state was lost, or that never reached a
// state write still surfaces through its volume, image, sandbox or adoption
// claim. See specs/volumes/runtime-management-commands.md ("Lists retired projects").
//
//exhaustruct:ignore
type Candidate struct {
	Slug           string
	Keys           []state.Key
	NewestActivity time.Time
	HasKeptSandbox bool
	HasRunningTask bool
}

// Retired reports whether the project is retired: it has no kept sandbox and no
// running task, and its newest activity is older than age. A project whose
// activity is exactly the threshold is not retired.
func (c Candidate) Retired(now time.Time, age time.Duration) bool {
	if c.HasKeptSandbox || c.HasRunningTask {
		return false
	}
	return now.Sub(c.NewestActivity) > age
}

// candidateBuilder folds every union source's signal for one slug.
//
//exhaustruct:ignore
type candidateBuilder struct {
	keys           map[state.Key]struct{}
	newestActivity time.Time
	hasKeptSandbox bool
	hasRunningTask bool
}

// EnumerateCandidates returns every project slug the launcher can still observe
// across its state directories, home volumes, runner images, sandboxes and
// root-state adoption claims, deduped by slug and ordered by slug. age is the
// staleness threshold the liveness signals are compared against. A list failure
// from the runtime is returned; foreign artifacts that do not parse to a
// launcher slug are ignored.
func EnumerateCandidates(ctx context.Context, age time.Duration) ([]Candidate, error) {
	builders := map[string]*candidateBuilder{}
	builderFor := func(slug string) *candidateBuilder {
		builder, ok := builders[slug]
		if !ok {
			builder = &candidateBuilder{keys: map[state.Key]struct{}{}}
			builders[slug] = builder
		}
		return builder
	}

	if err := enumerateStateCandidates(builderFor); err != nil {
		return nil, err
	}
	references, err := state.HomeVolumeReferences()
	if err != nil {
		return nil, err
	}
	if err := enumerateVolumeCandidates(ctx, references, builderFor); err != nil {
		return nil, err
	}
	if err := enumerateImageCandidates(ctx, builderFor); err != nil {
		return nil, err
	}
	if err := enumerateSandboxCandidates(ctx, age, builderFor); err != nil {
		return nil, err
	}
	if err := enumerateAdoptionClaims(builderFor); err != nil {
		return nil, err
	}

	candidates := make([]Candidate, 0, len(builders))
	for slug, builder := range builders {
		candidates = append(candidates, Candidate{
			Slug:           slug,
			Keys:           sortedKeys(builder.keys),
			NewestActivity: builder.newestActivity,
			HasKeptSandbox: builder.hasKeptSandbox,
			HasRunningTask: builder.hasRunningTask,
		})
	}
	slices.SortFunc(candidates, func(a, b Candidate) int { return strings.Compare(a.Slug, b.Slug) })
	return candidates, nil
}

// enumerateStateCandidates adds every slug with a state directory and the keys
// its state files name. The newest state-file mtime is an activity signal.
func enumerateStateCandidates(builderFor func(string) *candidateBuilder) error {
	slugs, err := state.ProjectSlugs()
	if err != nil {
		return err
	}
	for _, slug := range slugs {
		builder := builderFor(slug)
		agents, err := state.StatefulAgentNames(slug)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			if err := noteStateFile(builder, state.Key{Slug: slug, Agent: agent}); err != nil {
				return err
			}
		}
		// A legacy agent-less state file lives directly under the slug dir.
		if err := noteStateFile(builder, state.Key{Slug: slug, Agent: ""}); err != nil {
			return err
		}
	}
	return nil
}

// noteStateFile records a key and its state-file mtime when the state file
// exists. A state file removed between enumeration and the stat is absence, not
// an error. It is a variable so tests can drive the per-agent stat failure that
// StatefulAgentNames' preceding stat of the same path makes unreachable without
// a filesystem race.
var noteStateFile = func(builder *candidateBuilder, key state.Key) error {
	mod, err := state.StateFileModTime(key)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return nil
		}
		return err
	}
	builder.keys[key] = struct{}{}
	builder.noteActivity(mod)
	return nil
}

// enumerateVolumeCandidates adds each home volume under the keys that own it,
// with its creation time as activity. A volume's owner is the state keys that
// reference it, not the slug its name embeds: a legacy identity adoption moves
// the state to a checkout slug without renaming the volume, so a legacy-named
// volume a live checkout adopted must surface as that checkout, never as the
// now-ownerless legacy slug (whose purge could not remove it). A volume no state
// references is an orphan and surfaces under its name-parsed key; a foreign
// volume name parses to no slug and is skipped. A volume a state references
// contributes activity to every referencing key.
func enumerateVolumeCandidates(
	ctx context.Context,
	references map[string][]state.Key,
	builderFor func(string) *candidateBuilder,
) error {
	handles, err := msb.Get().ListVolumes(ctx)
	if err != nil {
		return err
	}
	for _, handle := range handles {
		name := handle.Name()
		owners := references[name]
		if len(owners) == 0 {
			artifact := naming.ParseHomeVolumeName(name)
			if artifact.Slug == "" {
				continue
			}
			owners = []state.Key{{Slug: artifact.Slug, Agent: artifact.Agent}}
		}
		for _, key := range owners {
			builder := builderFor(key.Slug)
			builder.keys[key] = struct{}{}
			builder.noteActivity(handle.CreatedAt())
		}
	}
	return nil
}

// enumerateImageCandidates adds the parsed slug and agent of every launcher
// runner-image ref, with its last use (falling back to creation) as activity. A
// reference that is not a runner-image ref is foreign and skipped.
func enumerateImageCandidates(ctx context.Context, builderFor func(string) *candidateBuilder) error {
	handles, err := msb.Get().ImageList(ctx)
	if err != nil {
		return err
	}
	for _, handle := range handles {
		ref := handle.Reference()
		if !naming.IsRunnerImageRef(ref) {
			continue
		}
		artifact := naming.ParseImageTag(ref)
		builder := builderFor(artifact.Slug)
		builder.keys[state.Key{Slug: artifact.Slug, Agent: artifact.Agent}] = struct{}{}
		builder.noteActivity(imageActivity(handle))
	}
	return nil
}

// imageActivity returns the newest activity signal an image carries: its last
// use, falling back to its creation time when it has never been used.
func imageActivity(handle msb.ImageHandle) time.Time {
	used := handle.LastUsedAt()
	if used.IsZero() {
		used = handle.CreatedAt()
	}
	return used
}

// enumerateSandboxCandidates adds each launcher-owned sandbox's parsed slug and
// agent; a foreign sandbox whose name carries no launcher slug hash is skipped.
// A launcher-prefixed name that does not parse to a slug — a malformed legacy
// name like "agents-sandbox-vm-" — is skipped too, so it never becomes an
// empty-slug candidate a purge could target. A project VM's UpdatedAt is
// activity, and a non-prunable VM is a kept sandbox. A running task sandbox
// marks the project as having a running task; its timestamps are not activity
// and it never counts as a kept sandbox.
func enumerateSandboxCandidates(
	ctx context.Context,
	age time.Duration,
	builderFor func(string) *candidateBuilder,
) error {
	handles, err := msb.Get().ListSandboxes(ctx, nil)
	if err != nil {
		return err
	}
	for _, handle := range handles {
		labels := sandboxLabels(handle)
		if !naming.IsLauncherSandbox(handle.Name(), labels) {
			continue
		}
		identity := naming.SandboxIdentity(handle.Name(), labels)
		if identity.Slug == "" {
			continue
		}
		builder := builderFor(identity.Slug)
		builder.keys[state.Key{Slug: identity.Slug, Agent: identity.Agent}] = struct{}{}
		if identity.Task {
			if !msb.IsSandboxInactive(handle.Status()) {
				builder.hasRunningTask = true
			}
			continue
		}
		builder.noteActivity(handle.UpdatedAt())
		if !prunable(handle, age) {
			builder.hasKeptSandbox = true
		}
	}
	return nil
}

// enumerateAdoptionClaims adds the owning checkout of every root-state adoption
// claim, so a checkout that adopted a legacy identity's lost-state volume still
// surfaces even when nothing else records it. The claim is keyed by the legacy
// identity but owned by the checkout that adopted the volume, so the checkout —
// the identity whose purge can actually remove the claim and its volume — is the
// candidate, not the legacy key. A claim with no recorded checkout falls back to
// its legacy key.
func enumerateAdoptionClaims(builderFor func(string) *candidateBuilder) error {
	root, err := state.ReadRootState()
	if err != nil {
		return err
	}
	for claimKey, claim := range root.AdoptedHomeVolumes {
		legacySlug, claimAgent, ok := strings.Cut(claimKey, "/")
		if !ok {
			continue
		}
		ownerSlug := claim.CheckoutSlug
		if ownerSlug == "" {
			ownerSlug = legacySlug
		}
		builderFor(ownerSlug).keys[state.Key{Slug: ownerSlug, Agent: claimAgent}] = struct{}{}
	}
	return nil
}

func (b *candidateBuilder) noteActivity(at time.Time) {
	if at.After(b.newestActivity) {
		b.newestActivity = at
	}
}

// sortedKeys returns the builder's keys in a stable order.
func sortedKeys(keys map[state.Key]struct{}) []state.Key {
	result := make([]state.Key, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	slices.SortFunc(result, compareStateKey)
	return result
}
