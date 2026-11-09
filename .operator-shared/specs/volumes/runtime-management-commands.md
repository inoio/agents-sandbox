---
description: Contract for agents-sandbox's microsandbox management surface — volume/image/sandbox inspection, image and sandbox removal, sandbox disk compaction, a top-level `df` plus a `storage` group alias, and uniform `-q`/`--format json`/`--yes`/`--older-than` conventions — deliberately bounded, not a mirror of msb's lifecycle, data-portability, registry, cloud, or self-install surface.
read_if: Volume/image/sandbox inspection, removal, and disk-compaction commands, the `df`/`storage` accounting commands, prune confirmation/format flags, or command-surface consistency conventions.
---

# Runtime management commands

## Intent

agents-sandbox owns its microsandbox runtime in a private home (`$MSB_HOME`, `specs/volumes/runtime-home-decoupling.md`) and no
longer installs or advertises a user-facing `msb`. That removes the user's only way to inspect and clean up the runtime's
own objects (volumes, images, sandboxes) with msb's own tooling; the `volume remove` command is the first piece of the
replacement.

This spec defines the rest of the **management** surface agents-sandbox provides so a user can inspect and reclaim its
runtime objects without an `msb` binary: object inspection, object removal, storage accounting, and uniform prune
controls. It is a deliberately bounded slice of msb's CLI — not a front-end to msb, and not a mirror of its lifecycle or
portability features.

## Goals

- Provide read-only inspection of the objects agents-sandbox manages: home
  volumes, cached runner images, and project sandboxes/VMs.
- Provide explicit removal of volumes and images by name.
- Provide opt-in compaction of project VM disks.
- Provide storage accounting over the private home (a `df` equivalent) and a
  `storage` command group.
- Make the listing/prune surface consistent: machine-readable output, quiet
  names-only output, non-interactive confirmation, and a uniform age flag.
- Keep all behavior scoped to the private runtime home and agents-sandbox's
  per-project/per-agent model.

## Non-goals

- Reproducing msb's full sandbox lifecycle (`create`/`start`/`restart`/`pause`/
  `resume`/`wait`), command execution (`exec`/`logs`/`metrics`), or arbitrary
  sandbox management. agents-sandbox's VMs are derived from a project + agent,
  not created ad hoc.
- Data-portability commands (`image pull`/`load`/`save`).
- Registry-credential management (`registry`/`registries`).
- Any cloud-backend surface (`context`) or msb self-management
  (`self`/`install`/`uninstall`/`downgrade`).
- Forks, snapshots, or SSH.

## Constraints

- Private runtime home: every command operates on `$MSB_HOME` and the
  agents-sandbox state roots, never on a user's `~/.microsandbox`.
- Project-scoped identity: sandbox/volume/image identity is derived from project
  slug, agent, and the recorded state, not arbitrary user-chosen names, except
  where an object is addressed by its own name for a by-name operation
  (`volume remove`, `image remove`).
- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Follow the existing command conventions: Cobra groups with aliases, the tree
  annotations (`also: sandbox X`), and `termio` for all output.

## Command surface contract

`volume`:

- `volume list` — table default; `-q/--quiet` (names only), `--format json`.
- `volume inspect <name>` — name, kind, host path (`Path()`), quota, used,
  capacity, disk format, disk fstype, labels, creation time.
- `volume remove <name>...` (`rm`) — by-name deletion with the fail-closed
  project-reference guard and `-f/--force`; confirms under `--yes`.
- `volume prune` — orphaned home volumes (no project state references them) above `--older-than`. It never removes the
  volume a project's state records, so a whole project's volumes go only through the purge path.
- `volume create` — excluded: home volumes come from the runner image via
  `volume migrate`/`reset`/`edit`; a raw create would bypass that model.

`image`:

- `image list` — table default; `-q/--quiet` (references only), `--format json`.
- `image inspect <ref>` — manifest/layers/config; reuse the existing
  `image.inspect` path plus `msb.Image.Inspect` (already used for digest
  resolution).
- `image remove <ref>...` (`rm`) — by-reference deletion; a non-runner reference
  is a hard ownership refusal that `--force` does not override, and an in-use
  runner image is refused by the reference guard unless `-f/--force`, naming the
  referencing projects.
- `image prune` — keeps a live project's variants used within the prune age and
  in-use images; supports `--yes`, `--format json`, and `-q`. Its Docker-image
  pass honors `--older-than`, is previewed by `--dry-run`, and reports each as a
  `docker-image` detail; an msb image's `stale_for_seconds` is populated from its
  last use (falling back to its creation time).
- `image pull`/`load`/`save` — excluded: runner images are built, not moved.

`sandbox`:

- `sandbox list`, `sandbox run`, `sandbox shell`, `sandbox stop`, `sandbox kill`
  exist. Here `-f/--force` is a remove-state toggle, not a guard override: on an
  inactive VM (`Created`/`Stopped`/`Crashed`) it skips the stop/kill and removes
  the persisted state, and `--dry-run` removes nothing (#161).
- `sandbox inspect <name>` — status, image, labels, mounts, resources, `ID`, and
  backend kind.
- `sandbox remove <name>` (`rm`) — remove an agents-sandbox project VM by name;
  refuses a running VM unless `-f/--force` (stops it first). A hard name-shape
  classification guard that `--force` cannot override gates the name: a legacy
  `agents-sandbox-vm-*` name is accepted, and otherwise the name must not parse as
  a task sandbox or a home-volume/runner-image name. This is a project-vs-task
  classification, not artifact ownership (which is positional; see
  `specs/identity/artifact-naming.md`). It does **not** drop the persisted state, so the
  recorded home volume survives and the next run recreates the VM against it.
  With `--purge [<target>]` it becomes the project-removal path (same engine as
  `prune --purge`, see below): the optional target is a slug or a `<slug>-<agent>`
  key, defaulting to the cwd, and the project's state, home volumes, runner images
  and sandboxes are removed after a single confirmation; a running sandbox fails
  fast until `--force`.
- `sandbox compact [name]` — merge the oldest sealed copy-on-write disk layers of
  every project VM's disks (or only the named one), briefly pausing a running VM.
  Opt-in only: it never runs on the automatic prune path, and named volumes such
  as the home volume are never compacted. Flags: `--dry-run`, `--disk` (one guest
  mount, mutually exclusive with `--root-only`), `--root-only`, `--layers` (the
  oldest N sealed layers, minimum 2; default all), `-q/--quiet` (suppresses stdout),
  and `--format json`.
- `sandbox copy`/`cp` — dropped: host↔VM transfer is the excluded
  data-portability surface, not part of the bounded management surface.
- `sandbox start`/`restart`/`exec`/`logs`/`metrics`/`pause`/`resume`/`wait` —
  excluded: lifecycle/exec surface agents-sandbox does not expose.
- `sandbox modify`/`fork`/`snap`/`ssh`/`install` — excluded: no fit for the
  project-VM model (reconfiguration flows through reprovision, not `msb modify`).

`prune` (batch; also `storage prune`):

- **Safe tier only.** Reclaims stale project VMs, runner-image refs and native
  image data, dangling Docker images, runtime RAM, and stale internal state (a
  `state.yaml` recording no home volume, unheld `claims/*.claim`, stale
  `clients/*.lock` leases, and empty slug subtrees — an emptied `clients/` and
  agent dir as well as the slug dir). It **never removes a home volume** or
  the state that points at a surviving one — a state recording a home volume and
  root-state adoption claims are reclaimed only by
  `prune --purge`/`sandbox remove --purge` — so `prune --yes` cannot destroy
  agent state. `locks/` is deliberately excluded.
- **Lists retired projects, never deletes them.** A project is retired when it
  has no kept sandbox (no running VM and no inactive VM within the age, task
  sandboxes not counting) and its newest activity — VM `UpdatedAt`, newest home
  volume `CreatedAt`, newest image `LastUsedAt`/`CreatedAt`, newest state-file
  mtime — is older than `--older-than`. Batch `prune` reports them as a read-only
  `retired_projects` section with a hint to purge. Candidates come from the
  artifact union (state dirs, volume names, image refs, sandbox identities, root
  adoption claims), so a state-less slug with a leftover volume still surfaces;
  foreign artifacts are ignored. Attribution follows ownership, not names: a home
  volume is attributed to the state keys that reference it and to the checkout of
  any adoption claim that names it, so a legacy-named volume a checkout adopted —
  even a lost-state adoption whose only record is the claim — surfaces as that
  checkout (the identity whose purge can remove it), never as the now-ownerless
  legacy slug, and only a volume with neither pointer surfaces under its name; a
  root-state adoption claim likewise surfaces its owning checkout.
- **`prune --purge [<target>]` and `sandbox remove --purge [<target>]` share one
  engine.** They remove one project's artifacts — sandboxes (a running one
  stopped first under `--force`), home volumes, runner images, state files,
  claims and root adoption claims — in an order that never orphans a volume or
  drops the last pointer to one; a volume that fails to remove keeps its state
  file. Home-volume and adoption-claim ownership is not the name's alone: a
  legacy identity adoption moves the state to a checkout slug without renaming
  its volume, and a root-state adoption claim records the checkout that owns a
  lost-state volume. A volume is therefore removed when its name parses into the
  target, a state key in the target references it, or an in-target checkout holds
  its adoption claim, and is refused (with a warning) when an out-of-target state
  key or checkout still owns it — so purging a legacy slug can never delete
  another checkout's saved home, and purging a checkout reclaims the
  legacy-named volume its state points at. Ownership is re-derived at execution
  under the launcher-global root lock — the lock an identity adoption serializes
  on — so a volume another checkout adopts while the confirmation prompt is open
  is refused rather than deleted. An adoption claim is owned by its
  `checkout_slug`, so purging a checkout retires its own claims while purging a
  legacy slug leaves a live checkout's claim intact; a claim is dropped only once
  its volume was removed or found absent, so a volume that survives a failed
  removal keeps its owner record. `<target>` is a slug (whole
  project) or `<slug>-<agent>` (one key); an omitted target resolves the cwd (an
  error outside a checkout). The age gate is bypassed: `prune --purge` takes
  `--dry-run`/`--format json`/`--yes`, while `sandbox remove --purge` confirms and
  honors `--yes`; both honor `--force` to stop a running sandbox first.
- `--older-than` must be positive: a non-positive value is rejected.

`storage` / `df`:

- `df` — msb-shaped storage table; `--verbose` item blocks and host paths;
  `--format json` msb-shaped report; `-q` category labels only.
- `storage df` / `storage prune` — aliases of the top-level `df`
  (`also: storage df`) and `prune` (`also: storage prune`); both also remain
  top-level.

Out of scope (explicit): `registry`/`registries`, `context`/`ctx`, `self`/
`doctor`/`update`/`downgrade`/`uninstall`.
Runtime-RAM pruning enriches the top-level `prune` rather than adding a command
(#156): a fourth phase calls microsandbox's `PruneStorage` over branch/snapshot
memory backings under the same `--older-than` threshold (converted to whole
seconds; a non-positive age means no age filter), honors `--dry-run` through the
runtime's own flag, and joins a failure into the aggregate error. The native
image-prune report's `SkippedInUse` and `BytesReclaimed` are surfaced in both the
human output and `--format json`; unknown byte counts stay unknown (`-` / `null`),
never `0`.

## Cross-cutting conventions

- **Listing**: every list supports `-q/--quiet` and `--format json`; human tables
  remain the default. Identifiers mode writes through `termio.UI.Print`, which
  bypasses the global `--quiet` stdout suppression, so `-q` still emits
  identifiers. JSON renders an empty list as `[]`, never `null`. `--format json`
  with `--quiet`, or any `--format` other than `json`, is an error ("mutually
  exclusive" / "unsupported format").
- **Inspection output**: the shared output mode applies to a single object too —
  `-q/--quiet` prints the object's identifier (volume name, image reference, or
  sandbox name) and nothing else. Inspect identity/size fields are
  presentation-oriented human strings (matching the list JSON), nullable fields
  render as `-`, and collections (env/cmd/entrypoint/layers/mounts) render as
  arrays. Per-volume usage is exposed in `volume inspect` only; `volume list`
  stays cheap (no per-object stat).
- **Destructive**: every removal/prune supports `--yes` to authorize
  non-interactively, and every removal exposes `-f/--force` to override its
  reference/in-use/running-state guard. `--force` overrides those guards;
  ownership/scope guards (only agents-sandbox project VMs / runner images) are
  hard and not `--force`-overridable. `--yes` skips a confirmation prompt —
  distinct from `--force`, and they may coexist. Every removal command prompts
  (including `volume remove`), and the guard runs before the prompt so a refusal
  fails fast. A declined prompt returns the `termio.ErrCancelled` sentinel; a
  non-interactive invocation without `--yes` fails fast with
  `termio.ErrConfirmationRequired` and never parks on stdin. Removal `--force`
  flags bind with `BoolVarP` so the closure variable updates (`BoolP` parses the
  flag but leaves that variable false). Home-volume deletion is confirmation-gated:
  it is reachable only from `volume prune`, `prune --purge`, and
  `sandbox remove --purge`, never from auto-prune or the batch `prune`.
- **Age threshold**: `--older-than` is the canonical flag (msb's spelling),
  defaulting to `7d` and parsed with `viperconfig.ParseHumanDuration`; `--age` is
  deprecated (hidden, warns, still honored; both set together is an error).
- **Prune report**: `prune` and each group `prune` support `--format json` and
  `-q`. The JSON object carries the per-group counts (`sandboxes`, `volumes`,
  `images`, `docker_images`), a `dry_run` flag, and a `details` array of
  `type`/`name`/`slug`/`digest`/`stale_for_seconds` entries; each group prune's
  object carries only its own counts plus `dry_run`/`details`. The full `prune`
  report also carries a `retired_projects` string array, and a reclaimed Docker
  image is reported as a `docker-image` detail. The report is
  rendered before an error is returned: a prune whose per-group errors are joined
  (top-level `prune` and `AutoPrune`) still reports what it removed first
  ("summary, then error"). Output mode and flag validation (`--format`/`--quiet`
  mutex) run before the confirmation prompt so bad flags fail fast rather than
  being masked by a non-interactive `ErrConfirmationRequired`.
- **Storage accounting**: `df` mirrors microsandbox's own `msb df` rather than
  inventing a format. `StorageUsage` supplies the categories microsandbox owns
  (images, snapshots, branch memory, snapshot memory, sandboxes, volumes) with
  nil counters meaning *unknown*, never zero; a `runtime` category walks the
  private home's `bin`/`lib` for the runtime/FFI artifacts and leaves its `in_use`
  nil. The human table is `TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE` in msb's
  category order, `-` for unknown, with a non-verbose footer reading
  `logical sizes; '-' means unknown. use --verbose for details.` and `--verbose`
  item blocks per category. The `--format json` object is keyed by category
  (`images`, `snapshots`, `branch_memory`, `snapshot_memory`, `sandboxes`,
  `volumes`) with msb's per-category fields (`count`, `in_use`, `logical_bytes`,
  `allocated_bytes`, `reclaimable_logical_bytes`, `items`, `notes`) plus
  top-level `notes` and an extra `runtime` key; unknown counters are `null` and
  `items` is always present (`[]`). Deviations from msb: the extra `runtime`
  category, graceful degradation instead of erroring on `ErrUnsupportedOperation`
  (the note is surfaced on non-verbose output so an all-`-` table is explained),
  and the global `-q` printing category display labels. `--verbose` affects only
  the human render; JSON always includes `items`. The host-side buildkit cache
  and host Docker's store are not part of this seam (the buildkit cache is a
  named msb volume already covered by the `volumes` category).
- **Tree**: new subcommands carry the `also: sandbox X` annotation and aliases
  consistent with the existing tree.
- **Guards are fail-closed**: an unreadable or corrupt project state file blocks
  a guarded removal unless `--force` is given.
