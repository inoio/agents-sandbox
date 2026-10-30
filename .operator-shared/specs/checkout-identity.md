---
description: Contract for splitting project identity in two (issue #144): a repo-scoped slug (origin-derived) driving the per-slug user config dir and the sandbox grouping label, and a checkout-scoped slug (resolved git common dir) driving state, VM name, home volume, locks/claims, and the runner image tag. Two clones of one remote get separate VMs/volumes; a checkout and the worktrees inside it share one. Includes the automatic, noticed migration that adopts the legacy origin-keyed identity (destroy the legacy VM, then move the state dir), the prune home-volume reference guard, the side-effect-free dry-run, and the rule that a worktree outside its checkout gets its own identity.
read_if: Implementing or reviewing issue #144 (two checkouts sharing one VM), the repo-vs-checkout slug derivation, the runner-image tag identity, the `config paths` command, the checkout-identity migration and its notice, the prune reference guard, or the identity of a worktree outside its checkout.
---

# Per-checkout project identity (issue #144)

Status: implemented (PR #244; the multi-agent/concurrency hardening, the lost-state recovery, the corrupt-state skip, and the shared-legacy volume claim are described below).
Date: 2026-10-19.

## Problem

`git.ProjectSlug()` hashes the **origin URL**, so every clone of one remote collapses to the same
`<repo>-<hash>` slug. That single slug keys the state dir, the VM name, the home volume, locks/claims,
the runner image tag, and the per-slug user config. Two separate checkouts therefore resolve to one
`state.Key{Slug, Agent}`: the second `agents-sandbox` finds the first checkout's VM, attaches to it,
and inherits its `/workspace` bind (the mount is fixed at VM creation). Files written in the second
checkout land in the first.

The origin-derived slug is also the *only* slug, so the config namespace, which should be shared by
all clones of a remote, is inseparable from the stateful artifacts, which should not be.

`bare-worktree-layout.md` (#145) deliberately kept "one origin, one VM" for worktrees. That premise
is wrong for independent clones and is corrected here; the worktree behavior it introduced (bind the
smallest enclosing checkout, guest CWD derivation) is preserved.

## Decision: two slugs, one derivation each

Project identity splits into two `<folder>-<14-char base36 hash>` slugs, each governing the artifacts
whose correct unit it matches.

- **repo slug** — derived from the origin remote URL. Exactly today's `ProjectSlug()` value.
- **checkout slug** — derived from the resolved git **common dir** (the shared `.git` of a checkout and
  all its worktrees). New value.

The name parsers (`naming.ParseVMName`, `ParseHomeVolumeName`, `ParseImageTag`) are untouched: both
slugs keep the `<folder>-<hash>` shape they already parse.

## Derivation

- **repo slug** = `sanitize(origin repo last path segment) + "-" + HashID(origin URL)`; falls back to
  the checkout folder/basename when there is no origin, and to the CWD path when not in a repo. This is
  the current `projectSlug`/`identitySlugs` behavior, unchanged. **Exception:** a bare root (a bare repository, which
  has no worktree) records the old path-derived slug as an additional legacy identity
  (`Identity.LegacyRepoSlugs`) instead of dropping it, because that is where its pre-split state and
  per-slug config live; see Migration.
- **checkout slug** = `sanitize(origin repo last path segment, or checkout basename when no origin) +
  "-" + HashID(resolved git common dir path)`. The folder name still comes from the origin so two
  clones read `foo-<hashA>`, `foo-<hashB>`. The hash input is the symlink-resolved common dir, so all
  worktrees of one checkout (nested, or the bare `<root>/.git` layout) share a checkout slug, and two
  clones differ. The derivation resolves symlinks **internally**, so every entry point (run, stop, kill,
  volume ops, build) hashes the same path — otherwise `/tmp` vs `/private/tmp` on macOS would make `run`
  and `stop` compute different checkout slugs.
- **non-repo folder**: both slugs are `<folder>-<hash(cwd)>` (today's fallback); behavior unchanged.
- **subdirectory of a checkout**: the checkout slug is the checkout root's, matching the `/workspace`
  bind root from #145.
- **outside worktree**: its own identity (below) — it cannot share its checkout's VM, so it gets one of its own.
- **linked worktree vs plain `.git` file**: a linked worktree is recognized only by a `.git` file whose
  recorded gitdir names a registration directory `<shared>/worktrees/<name>` (one final path segment,
  validated on the raw text before it is made absolute) that carries a `commondir` marker. A
  `--separate-git-dir` checkout and a submodule are `.git` files too — and `<ancestor>/worktrees/<name>.git`
  even matches a registration's shape — but lack the marker, so they are ordinary checkouts that bind their
  own worktree, never outside worktrees.

`ResolveIdentity` folds the former `ResolveWorkspaceLayout` and `projectSlugAt` into one pass and exposes
`WorktreeRoot` and `Bare` (read from the common git dir's `core.bare`) so the bare project-config resolver
(`configpaths.resolveProjectConfig`, #247) can consume it. It returns `(Identity, error)` and refuses an
unresolvable cwd or git dir instead of hashing a fallback path — the raw input or the unresolved absolute
path — which would silently mint a new checkout identity (state, VM, home volume) for the same physical
project (#354). The cwd-derived test accessors `MustRepoSlug`/`MustCheckoutSlug` panic rather than return
such a slug; production reads the `Identity` fields. `configpaths.resolveProjectConfig` is the one caller
that stays tolerant: it degrades to the relative base on that error, because the project config directory
is not identity-keyed.

## Which artifact uses which slug

| Artifact | Identity |
|---|---|
| Per-slug user config dir `$XDG_CONFIG_HOME/agents-sandbox/<slug>/` | **repo** |
| Sandbox grouping label `org.agents-sandbox.project` | **repo** |
| State dir `$XDG_STATE_HOME/agents-sandbox/<slug>/…` (state, `claims/`, `<agent>/clients/`) | **checkout** |
| VM name `agents-sandbox-vm-<slug>-<agent>` | **checkout** |
| Home volume `agents-sandbox-home-<slug>-<agent>-<ts>` | **checkout** |
| Locks under `<state>/locks/` keyed by project | **checkout** |
| Runner image `agents-sandbox/runner-<slug>:<agent>-latest` | **checkout** |
| Notify dedup claims (via `state.AcquireClaim`) | **checkout** |

The `org.agents-sandbox.project` label keeps the **repo** slug so `list --label project=<repo>` groups
every clone's sandbox; the sandbox **name** carries the checkout slug and is the exact identifier.
Nothing keys ownership off this label (prune parses names), so the change is safe.

## Runner image

The runner image is built from checkout-local inputs (the project Dockerfile and project config in
that checkout), so a checkout-scoped tag is the correct identity: two clones on different commits can
hold different images without fighting over one tag.

"Reuse across clones" becomes best-effort, not a shared tag: two clones with identical inputs each
compute the same content identity, so the second build is a **cache hit** against the shared BuildKit
cache, and the microsandbox content store should hold one blob behind two tags (containerd's store is
content-addressed; verify msb keys by digest). This trades the guaranteed single build of a shared tag
for correctness, and it means an identical pair costs one extra cached build rather than zero.

Consequence for upgrades: the msb seam exposes no image tag/retag operation (`ImageLoad`,
`ImageRemove`, `ImageInspect`, `ImageInspectDetail`, `ImageList`, `ImagePrune`, `ImageGet` only), so a migrated project
cannot re-point its existing image at the new tag — it rebuilds. Cache-assisted, but it needs the
builder working and, on a cold cache or offline, network for the base/agent layers.

## Config

Config stays on the **repo slug**, so its path is byte-identical to today and is shared by every clone
of a remote. No config migration — except for a bare root, whose pre-split per-slug config directory
(the path-derived slug) is renamed onto the repo slug during adoption, and only when the target does not
already exist (see Migration). Because the launcher-config resolver is built *before* adoption runs, the
resolver reads the **legacy** per-slug dir when it holds a config file and the repo-slug dir does not
(`effectiveConfigSlug` in `cmd/agents-sandbox`); otherwise the triggering run would load defaults instead
of the user's saved settings. That fallback is read-only, so `--dry-run` stays side-effect-free.

The only real gap is discoverability: nothing today prints the slug or the per-slug dir. Add
`agents-sandbox config paths` (plural) printing every config layer in precedence order, including the
concrete per-slug user dir (the effective layer, i.e. a bare root's legacy dir while it is still pending)
and the resolved repo slug, and stating that repo-wide knobs belong in the committed project config
(`.agents-sandbox/config.yaml`) or the generic user config. A `config edit` helper to create/open the
per-slug `config.yaml` is optional. The ordered-multi-dir resolver machinery from
`opencode2-config-fork.md` is available but not required, since nothing renames.

## Outside worktrees: their own identity

#145 warned and bound CWD for a worktree outside its checkout. Under checkout identity that is not enough:
an outside worktree and its main clone share the same git common dir, so hashing the common dir would give
them **one** checkout slug and therefore one VM whose `/workspace` bind can only be one of the two — the
#144 collision. Refusing would be deterministic, but it would take the tool away from every worktree that
lives outside its checkout, which includes the ones opencode creates under
`~/.local/share/opencode/worktree/` and the sibling worktrees of a bare clone.

So an outside worktree becomes its own checkout: `ResolveIdentity` hashes the **worktree root** instead of
the git common dir, binding that worktree root at `/workspace` with its own state, VM, home volume and image
tag. The bind root does not follow the cwd: a run from a subdirectory of the worktree binds the worktree
root and starts the agent in the subdirectory, exactly as an inside-worktree does. #145's rule is "bind the
smallest enclosing checkout that contains the CWD", and under this change the outside worktree is that
checkout — binding the cwd instead would hand the agent a workspace with no repository above it, and would
make the mount source (and so the reconfig fingerprint) depend on which directory the run started in.
`OutsideWorktree` stays a reported fact on the identity, naming the distinction, but nothing refuses on it
and no caller has to special-case it.

What this costs: an outside worktree no longer shares state or a home volume with its checkout or with its
sibling worktrees, so it onboards and authenticates on its own. "One VM per checkout" therefore means "one
per enclosing checkout root, or one per detached worktree". The #145 layout is still the cheaper one:
worktrees kept *inside* the checkout share its VM, state, volume and image.

An outside worktree's *legacy* identity was the origin-derived repo slug, so adoption applies to it like any
other checkout — and it therefore suffers the same asymmetry as a second clone: whichever cwd adopts first
takes the legacy state, and the others start fresh.

## Migration (automatic, with a notice)

**Trigger**: in a repo, a legacy **state file** exists for the agent under one of the identity's legacy slugs
(the repo slug, plus any `Identity.LegacyRepoSlugs` — a bare root's path-derived slug) at
`<state>/<legacySlug>/<agent>/state.yaml` and no checkout-keyed **state file** exists at
`<state>/<checkoutSlug>/<agent>/state.yaml`. The predicate is state-**file** absence, not directory absence,
so a stray or aborted run that creates the empty checkout directory cannot permanently skip migration.
**Or** the agent's legacy **project VM** is still live under a legacy slug while no checkout-keyed state
file exists — the lost-state case of `home-volume-recovery.md` (#219), where the VM is the only remaining
legacy artifact. Treating that as "brand new" would mint a fresh home volume and orphan the previous one
(credentials, files), so the VM arm adopts instead. Because destroying the VM removes the only record of its
volume, adoption first writes the mounted volume into the checkout state's `recovered_home_volume`, then
destroys the VM; home resolution (`volume.Manager.ResolveHomeVolume`) recovers that field into `home_volume`
with the current digest and fingerprints. The durable reference is what makes adoption safe from any entry
point: `stop`, `kill`, `build` and the volume commands discard the adoption result, and a `run` can still
fail after adoption but before home resolution, yet the next run recovers the volume from the state instead
of minting a fresh one. While that field is set the trigger stays live, so a legacy VM left behind by a
failed destroy is torn down on the retry (the state move is then a no-op, since no legacy state file
exists), and a `volume migrate`/`reset`/`edit` accepts the pending volume as its source and clears the
marker when it commits, so the first volume op is not wedged behind an unset `home_volume` and cannot undo
itself. A shared legacy identity has one VM and therefore one home volume, so the adopting checkout first
**claims** the volume under the legacy identity in the launcher-global `RootState` (`adopted_home_volumes`,
keyed `<legacySlug>/<agent>`) and commits the claim, then writes the marker; both run under the same root
lock and before the destroy, so each survives a destroy failure and a failed marker write cannot leave the
volume recorded without an owner. A second clone that adopts the same surviving VM sees the claim, starts with its own fresh
volume instead of recovering one volume, and — before it destroys that VM — writes the **owner's** recovery marker
from the claim, so the owner's volume stays reachable even if the owner never runs again (failing the migration
closed if it cannot write it). The claim is retired once the owner folds the volume into its own state. A claim
naming a *different* volume than the live VM mounts — its own or another checkout's — is a state adoption cannot
reconcile, so it fails closed rather than destroying the VM. A corrupt or unreadable *checkout*
state makes the trigger skip migration rather than abort the run, leaving legacy VMs and state untouched
so the caller's recovery path (`RecoverHomeState`) can repair the document; reads of the *legacy* state and
a legacy VM's mounts still fail closed. A VM whose mounts cannot be read fails the adoption closed rather
than destroying it blind. Re-evaluated on each run until it fires.

**Entry points: uniform.** Every cwd-derived command that resolves the checkout slug attempts adoption —
`run`, `shell`, `stop`, `kill`, the `volume …` ops and `build` — not only the paths that write state. A
narrower set breaks in two ways: a first post-upgrade `stop` would report "no project VM found" while the
legacy VM keeps running, and a first `volume reset` or `build` would create the checkout state file,
permanently un-match the trigger, and orphan the legacy auth and home volume.

**Serialization.** Adoption runs under the host-only root flock (`state.WithRootState`,
`internal/sandbox/state/root_txn.go`), the existing precedent for a launcher-global operation that does msb
I/O (`internal/sandbox/runtimemigration/copy.go`), so two concurrent first-post-upgrade runs cannot
both destroy and move. The trigger is re-checked while the lock is held: a run that loses the race to
another checkout sees the state already moved and continues under the checkout identity instead of tripping
the incomplete-migration failure on a source that is already gone. Beyond the root lock, adoption holds
`WithKeyLocks` for every key the move touches — each legacy slug's enumerated agents plus the command's agent,
under both the legacy and the checkout slug — across the destroy and the move, so a concurrent per-key state
write or VM creation cannot interleave with them (#333).

**Actions**

1. **Destroy every legacy VM first, across all sources, before moving any state.** For every legacy slug,
   enumerate the agents whose state lives there (`state.StatefulAgentNames`), **plus the command's own agent**
   (so a lost-state VM that `StatefulAgentNames` cannot see is torn down too), and destroy
   `projectVMName({Slug: legacySlug, Agent})` identity-safely (best-effort; a gone name is fine). Doing all
   destroys before any move makes a destroy failure abort with every legacy state file still in place, so
   the next run re-fires the trigger and resumes; moving first would create the checkout state file and, on
   a later destroy failure, leave the trigger permanently satisfied — the retry would adopt the first
   source's home and strand the other source's VM, home volume and state. The move carries every agent's
   state, so a sibling agent's VM left running would keep writing the shared home volume while its next run
   recreates it under the checkout name over the same volume — and would become a `ToPrune[<legacySlug>]`
   entry (see Prune safety).
2. **Then move** `<state>/<legacySlug>/` to `<state>/<checkoutSlug>/` (project state, `claims/`,
   `<agent>/clients/`) as a **recursive merge that never overwrites an existing file**: a free destination
   name is renamed, both-sides-directories recurses, and a colliding file or a file/directory type mismatch
   is left in the legacy location and reported. A plain rename of the whole directory fails with
   `ENOTEMPTY` whenever an aborted run left an `ensure-vm.lock` or a `clients/` entry behind — exactly the
   state the state-*file* predicate deliberately tolerates — and a one-level refusal is worse than that: if
   the collision is the `<agent>/` directory itself, the legacy state file never arrives, the predicate
   re-fires on every run, and the pipeline proceeds under the checkout identity with no state at all, which
   means a fresh home volume and silently lost agent auth. The merge is what makes the state-file predicate
   a real idempotence record: the state file always arrives, because the predicate guarantees none exists
   at the destination. An existing destination entry of any kind is never displaced — a dangling symlink
   counts as occupied, and a merged-into destination directory keeps its own permissions and contents.
   Adoption does not trust the merge silently: it re-reads the checkout-keyed state afterwards and fails
   loudly, naming the skipped paths and the legacy state file, when the state file did not arrive — the
   legacy VM is already destroyed by then, so continuing with no state (a fresh home volume, silently lost
   auth) and falling back to the legacy slug are both worse.
3. Home volume: unchanged. Its name is stored in the state's `home_volume`, so the preserved volume is
   re-resolved by name with no rename. In the VM-only arm there is no legacy state to carry the name, so
   adoption writes it into `recovered_home_volume` on the checkout state *before* the destroy, and home
   resolution (`volume.Manager.ResolveHomeVolume`) recovers it into `home_volume` with the current digest
   and fingerprints. That marker is the durable record: it survives an adoption result the caller discards
   and a run that fails before home resolution, and a `volume migrate`/`reset`/`edit` reads it as its
   source and clears it when it commits. The state-file arrival check is skipped there, because
   there is no legacy state file to arrive.
4. VM: recreated once under the new name over the preserved home volume.
5. Runner image: rebuilt under the new checkout tag (cache-assisted, see above).

Destroy-first is crash-safe: a crash between the destroy and the move leaves the legacy state in place,
so the next run re-adopts and simply recreates the VM. Move-first would leave an untracked legacy VM
that prune could then reclaim alongside the volume.

**Dry run skips adoption entirely.** Both `--dry-run` and `--dry-run-vm` skip it, because it is
all-or-nothing: a move without the destroy would orphan the legacy VM (still running against the shared home
volume, and a `ToPrune[<repoSlug>]` entry). Dry run resolves read-only against the legacy location — the rest
of the pipeline keeps the legacy identity, and the returned slug is the one the pending state was actually
found under (a bare root's path-derived slug, not always the repo slug), so nothing is presented as if the
migration had run against the wrong state — and prints the notice as a "would", with the destination paths.
Adoption is re-evaluated on every run, so deferring it costs nothing.

**Notice**: emitted once, and **explicit about everything that moved, with absolute paths** — the state
directory (old → new), the destroyed legacy VM name, the preserved home volume name, the old → new VM
name, and the old → new image tag. With an extra legacy identity (a bare root), the block adds a second
state-dir row and a `config dir` row for the per-slug config directory renamed onto the repo slug. The move
is the idempotence record: the legacy state file is gone afterward, so the notice does not re-fire. A legacy
identity is shared by every clone of one origin, so whichever clone runs first adopts it; the others then
find no legacy state, start with their own fresh state and home volume, and get **no notice at all** — from
their side there was nothing to migrate. That asymmetry is inherent in moving one shared identity rather
than copying it: warning the later clones would need the adoption to record something they can read.

## Prune safety

A migrated project keeps a home volume whose name embeds the **legacy** repo slug (`home_volume` rides
along), while the legacy slug is now stale. Because `pruneHomeVolumes` removes any volume whose
name-parsed key is in `ToPrune`, and the just-upgraded legacy VM can land in `ToPrune[<repoSlug>]`, a
`prune` run *before* the first post-upgrade `run` would otherwise delete the preserved volume and then
`state.RemoveHomeState(<repoSlug>)` the very state adoption needs — losing auth and state.

Fix: `pruneHomeVolumes` must **fail closed on any volume name referenced by an existing state file**
(via `state.HomeVolumeReferences()`, as `volume remove` already does), so prune never deletes a volume a
state file points at. Failing closed means **skip that volume and report it**, not abort the prune — the
treatment prune already gives a replaced sandbox (`internal/sandbox/pruning/sandboxes.go:33-40`); the
skip happens before the dry-run branch, so a dry run reports the same kept set — and a
skipped volume's key must not reach `removeStateForGoneKeys`
(`internal/sandbox/pruning/volumes.go`), which would otherwise delete the very state adoption needs.
An unreadable state file surfaces as a `HomeVolumeReferences()` error, which aborts the volume pass as it
does in `volume remove`. `HomeVolumeReferences()` also reports a pending `recovered_home_volume` (the
VM-only adoption arm), so prune protects the volume during the window between adoption and the first
recovery. Images already have their own removal guard. Prune cannot trigger adoption
(recovery needs the checkout context, which prune is cwd-agnostic about and which state files do not
record), so the reference guard is the whole fix. Reclaiming the legacy **VM** by prune is accepted:
its absence does not block adoption, which recreates it over the preserved volume.

## Backwards compatibility

- Per-slug user config: unchanged path, shared across clones, no migration.
- Home volume and its auth/data: preserved; only the VM is recreated.
- State: preserved by the directory move (including fingerprints and upgrade/provenance fields).
- Image: rebuilt once under the new tag (cache-assisted); cannot be re-tagged.
- Non-repo folders: unchanged.
- Nested/bare worktrees: unchanged sharing (one VM per checkout).
- Outside worktrees: own identity (was: warned and bound CWD — a worktree of a checkout no longer shares that checkout's VM, state or volume).

## Non-goals

- Changing the #145 mount rule or guest-CWD derivation beyond giving an outside worktree its own identity.
- An `asb worktree` command or any change to `--worktree` / daemon worktrees.
- A git-dir identity marker: identity is path-derived (see limitations).

## Known limitations

- **Moving a checkout changes checkout identity.** The common dir path is the hash input, so a moved
  or renamed checkout gets a new checkout slug, a fresh VM/volume/image, and does not find the state it
  adopted under the old path. (Today a move keeps the origin slug.) A git-dir identity marker would fix
  this and was deliberately deferred.
- **A symlinked path resolves once, for good.** Identity derivation resolves symlinks internally, so a
  project **without an origin** that is reached through a symlink hashes the physical path from now on and
  gets a new repo slug — hence a new per-slug user config dir and a new state directory — the first time it
  runs after this change. Projects with an origin are unaffected: their slug hashes the origin URL.
- **A first-post-upgrade run needs the builder.** The image tag changes, so a cold-cache or offline
  first run rebuilds.
- Two clones with divergent Dockerfiles each build their own image (by design).

## Settled open decisions

The six questions this section used to leave open, settled 2026-10-20. The
adoption, dry-run, prune and outside-worktree refinements from the same design review live in their topical
sections above.

- **Identity API.** `git.ProjectSlug()` retires. `internal/git` gains `ResolveIdentity(cwd) (Identity, error)`
  returning the bind root, checkout root, worktree root, outside-worktree flag, bare flag, repo slug,
  checkout slug and `LegacyRepoSlugs`, plus the cwd-derived test accessors `MustRepoSlug()` and
  `MustCheckoutSlug()` (they panic on an unresolvable cwd). Explicit names, because after the
  split every call site is a judgement call. `internal/git` stays a leaf — hashing and layout inspection
  only, no knowledge of state paths or config dirs — which is what keeps `cmd`/`session`/`vm` cycle-free.
- **One git-inspection pass.** `ResolveIdentity` folds the former `ResolveWorkspaceLayout` and `projectSlugAt`
  into one pass (`internal/git/identity.go`) and resolves symlinks internally, so `run` and
  `stop` cannot compute different checkout slugs on macOS (`/tmp` vs `/private/tmp`). `ResolveWorkspaceLayout`
  and `WorkspaceLayout` are deleted; `configpaths.resolveProjectConfig` (#247) consumes `Identity.Bare`/
  `WorktreeRoot` instead.
- **Adoption lives in `internal/sandbox/vm`**, reachable from every entry point (`vm` imports
  `git`/`state`/`msb`/`naming` and is imported by `cmd` and `session`). The destroy half reuses
  `GetSandbox` → `SandboxHandle.Destroy` with the `IsReplaced` fail-closed handling of
  `destroyProjectVMHandle` (`internal/sandbox/vm/vm_lifecycle.go`). The state-directory move is new code: nothing today
  renames a state directory (`RemoveHomeState` deletes, `RecoverHomeState` rewrites in place).
- **No `migrations` registry entry.** The directory move is the idempotence record — the legacy state file is
  gone afterwards, so adoption cannot re-fire. `state.Migration`'s `Copied`/`Kept`/`Excluded` fields are
  copy-shaped and would sit unused. The root state *is* used for the lost-state home-volume ownership claim
  (`adopted_home_volumes`), which needs a launcher-global record a second clone can read.
- **The grouping label comes from the identity, not from `state.Key`.** `createProjectVM` sets
  `org.agents-sandbox.project` from the repo slug it is passed, never from `k.Slug`: `state.Key` keeps a
  single slug (the checkout slug, which names the VM), while the label is the repo slug, threaded in from the
  identity struct.
- **Migration notice**: one `Warnf` call carrying the whole block — yellow headline, indented detail rows,
  exactly as below — emitted *before* the work starts, so the notice explains the pause rather than confirming
  after the fact. The rows are therefore prospective ("destroyed", "recreated"), not a receipt. `--dry-run`
  prints the same block under the "would" headline. `Warnf` is not quiet-suppressed, so the notice survives
  `--quiet` — which is right for an event that changes where the project's state lives.

  ```
  Migrating this checkout to its own project identity (repository + checkout slugs).
  No action needed: files, credentials and the home volume are kept; only the project VM is recreated.

    state dir     <old state dir>  ->  <new state dir>
    home volume   <home volume name>   (kept as-is)
    VM            <legacy VM name>   (destroyed)
                  <new VM name>   (recreated)
    runner image  <legacy image tag>  ->  <new image tag>   (rebuilt)
  ```

- **`config paths`** prints the config layers in precedence order (lowest → highest, matching the resolver's
  precedence comment in `internal/viperconfig`) to stdout via `Outf`, and the "where to put what"
  hint to stderr via `Infof`, naming the committed project config (`.agents-sandbox/config.yaml`) and the
  generic user config as the homes for repo-wide knobs. `--format json` is deferred until asked for. It uses
  the `configpaths` accessor for the per-slug user dir (`UserSlugConfigDir`). One `viperconfig.ConfigLayers(slug)` list drives both the display and `NewResolver`'s
  file-layer merge, so the reported order and the merged order cannot drift; a file layer shows its dir with a
  `(missing)` marker when no config file is there, the block ends with a `repo slug:` line, and the per-slug
  layer reads the *effective* config slug while the label stays the repo slug. `--quiet` suppresses the `Outf`
  layer block but not the `Infof` hint.
