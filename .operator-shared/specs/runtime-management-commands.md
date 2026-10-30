---
description: Contract for extending agents-sandbox's CLI to cover microsandbox's *management* surface against its own private runtime home — volume inspect, image inspect/remove, sandbox remove/inspect/copy, a top-level `df` plus a `storage` group alias, and uniform `-q`/`--format json`/`--yes`/`--older-than` conventions — while deliberately not mirroring msb's full lifecycle, data-portability, registry, cloud, or self-install surface. Structured as ordered, independently shippable slices with shared groundwork landing first.
read_if: Adding or reviewing volume/image/sandbox inspection and removal commands, the `df`/`storage` accounting commands, prune confirmation/format flags, or command-surface consistency conventions.
---

# Runtime management commands

Date: 2026-10-15

Status: **design; Slices 0–5 implemented, Slice 6 dropped**.
Tracked as issue #165; delivered as sub-issues #168–#172 and #155. Builds on `specs/runtime-home-decoupling.md` (the private `$MSB_HOME` home).

## Intent

agents-sandbox now owns its microsandbox runtime in a private home
(`$MSB_HOME`, `specs/runtime-home-decoupling.md`) and no longer installs or
advertises a user-facing `msb`. That removes the user's only way to inspect and
clean up the runtime's own objects (volumes, images, sandboxes) with msb's own
tooling. The `volume remove` command added during that migration is the first
piece of the replacement.

This spec defines the rest of the **management** surface agents-sandbox should
provide so a user can inspect and reclaim its runtime objects without an `msb`
binary: object inspection, object removal, storage accounting, and uniform prune
controls. It is a deliberately bounded slice of msb's CLI — not a front-end to
msb, and not a mirror of its lifecycle or portability features.

The work is specified as **ordered delivery slices** (below). Because every
slice depends on the same cross-cutting output/confirmation conventions and on a
couple of shared seams, **the groundwork slices (0–2) land before any
user-facing command**, and each remaining slice is independently shippable in
dependency order.

## Goals

- Provide read-only inspection of the objects agents-sandbox manages: home
  volumes, cached runner images, and project sandboxes/VMs.
- Provide explicit removal of volumes and images by name.
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

## Anti-goals

- A command surface that implies agents-sandbox is a general msb replacement,
  setting an expectation that user-arbitrary sandboxes or images are first-class.
- Removal commands that can silently orphan a project's recorded home volume or
  an in-use runner image.
- Destructive commands that prompt or block in non-interactive contexts.
- Shipping a user-facing command whose output/confirmation behavior is
  inconsistent with its siblings, because the shared conventions were not landed
  first.

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

## Delivery slices

Slices are ordered; a slice depends only on earlier slices. Each slice is a
mergeable unit with its own tests, docs, and `CHANGELOG.md` entry.

### Slice 0 — Shared CLI conventions (groundwork)

No new user-facing command. Land the cross-cutting seam every later slice uses:

- A shared list-output path so a list can render, in addition to its default
  table, `--format json` (structured output on stdout) and `-q/--quiet`
  (identifiers only). A shared `resolveListOutput`/`printList` seam renders every
  list, rather than duplicating per command.
- A shared `--yes` flag and non-interactive confirmation helper for destructive
  commands, distinct from `--force` (which overrides a guard).
- Make `--older-than` the canonical age flag on prune commands (human-duration
  syntax: `7d`, `2w`) and deprecate `--age`/`-a`: it stays functional when set
  but is hidden from help and warns when used.
- JSON serialization for the prune reports (`prune` and each group `prune`).
- Deprecate `sandbox list --names` as an alias of the `-q/--quiet` spelling
  (hidden, warns, still honored).

Depends on: nothing. Done when the existing `sandbox`/`volume`/`image` list and
prune commands use the shared helpers, and the flags are covered by tests.

### Slice 1 — Reference-guard seam (groundwork)

Generalize the fail-closed reference scan behind `volume remove`
(`state.HomeVolumeReferences`) into a reusable guard with an image analogue
(`state.ImageReferences`, or a small guard package). No user-facing command.

Depends on: nothing (parallel with Slice 0). Needed by Slice 4. Guard semantics:
an unreadable or corrupt project state file blocks the operation unless forced.

Resolved: an image is in use when a kept project state **or** a kept sandbox
references its reference — the union of the project-state scan (the
`HomeVolumeReferences` analogue) and the live kept-sandbox image refs already
computed for image pruning. Project state is the source of truth for ownership;
the live sandbox refs catch a digest-pinned VM whose tag is no longer the
per-agent `-latest`.

Implemented shape: the walk behind `state.HomeVolumeReferences` is generalized
into an unexported `state.projectStateReferences(extract)` with the same
fail-closed semantics (a missing state root is empty; an unreadable directory or
unreadable/corrupt state file is an error). `state.ImageReferences` reuses it,
mapping each state to the per-agent runner ref its slug and agent derive
(`naming.RunnerImageRef`, now the single builder behind `image.runnerTag`); a
legacy agent-less key owns none, since runner images are per-agent.
`image.InUseImageReferences(keptSandboxes)` unions that scan with the image refs of
the given kept sandboxes, attributing each to the key parsed from the sandbox
name and de-duplicating. It reads each sandbox's image from `Config` (not
`Image`, which swallows the config error) so an unreadable sandbox config fails
closed rather than silently dropping the reference.

### Slice 2 — Home layout accounting seam (groundwork)

A seam that enumerates the private home's storage categories with byte sizes and
host paths, feeding Slice 5's `df`. No user-facing command.

Prefer microsandbox's own storage observation (`StorageUsage`) for the objects it
owns — images, snapshots, sandboxes, volumes, and branch/snapshot memory — which
reports per-category and per-object usage and whose optional counters mean
*unknown* when nil (never zero). Walk the private home only for what microsandbox
does not report: the runtime/FFI artifacts. `ErrUnsupportedOperation` from the
observation (cloud or older native libraries) degrades gracefully: the categories
it covers are marked unknown rather than reported as zero.

Depends on: nothing (parallel with Slices 0–1). Needed by Slice 5.

Implemented (issue #170) in `internal/sandbox/storage`: `Observe` emits the six
microsandbox categories (nil counters stay unknown) followed by a `runtime`
category that walks the private home's `bin`/`lib` for the runtime/FFI
artifacts. `ErrUnsupportedOperation` records a report-level note and leaves the
six categories unknown. A missing `bin`/`lib` subtree is a known zero, whereas a
walk error and any observation error other than `ErrUnsupportedOperation`
propagate. Per-object rows are opt-in via `Options.IncludeObjects` (`Objects`
stays nil, not empty, when not requested). The host-side buildkit cache and docker images were
**deferred out of this seam**: the buildkit cache is a named msb volume already
covered by the `volumes` category, and host Docker's store is outside the
private home. Revisit when a docker-backend image store lands.

Realigned in Slice 5 to microsandbox's report shape: category names are
snake_case (`branch_memory`, `snapshot_memory`) and ordered as microsandbox
orders them (images, snapshots, branch memory, snapshot memory, sandboxes,
volumes; `runtime` last), and `Totals` carries the category `InUse` count.

### Slice 3 — Object inspection (read-only commands)

- `volume inspect <name>` — name, kind, host path (`Path()`), quota, used,
  capacity, disk format, disk fstype, labels, creation time (all already exposed
  by `msb.VolumeHandle`).
- `image inspect <ref>` — manifest/layers/config; reuse the existing
  `image.inspect` path plus `msb.Image.Inspect` (already used for digest
  resolution).
- `sandbox inspect <name>` — status, image, labels, mounts, resources, `ID`, and
  backend kind.
- Wire `volume list`/`image list` onto the Slice 0 JSON/quiet output.

Depends on: Slice 0.

Implemented (issue #171): `volume.InspectVolume`, `image.InspectImage`, and
`vm.InspectSandbox` produce package-local detail DTOs; the CLI commands
(`buildVolumeInspectCmd`/`buildImageInspectCmd`/`buildSandboxInspectCmd` in
`commands_inspect.go`) resolve the shared output mode and render through
`printInspect` (identifier under `-q`, JSON under `--format json`, an aligned
key/value block by default). `volume list`/`image list` were already wired onto
the Slice 0 output by Slice 0 itself. Two seam additions made the inspection
possible: `msb.ImageInspectDetail` (a local `msb.ImageDetail` mapping, keeping
the existing `ImageInspect` config-only method untouched) and `SandboxHandle.ID`
(the stable identity, first needed here). `vm.InspectSandbox` sorts mounts by
guest path so the render is deterministic.

### Slice 4 — Object removal

- `image remove <ref>...` (`rm`) — by-reference deletion with `-f/--force` and
  the Slice 1 image-reference guard (refuses an in-use runner image, naming the
  referencing projects). Only runner references are accepted; a non-runner
  reference is a hard ownership refusal that `--force` does not override.
- `sandbox remove <name>` (`rm`) — remove an agents-sandbox project VM by name;
  `--force` permits removing a running VM (stopping it first). It does **not**
  drop the persisted state.

Depends on: Slice 0 (confirmation/force conventions), Slice 1 (image guard).

Implemented (issue #172): both removal commands run **guard → confirm → mutate**
and expose `-f/--force`. `image.RemoveImages` first hard-refuses any reference
that is not an `agents-sandbox/runner-*` image (`refuseNonRunnerRefs`, not
overridable by `--force`), then, unless forced, guards with the Slice 1 seam:
`inUseImageReferences` unions the project-state scan with the image refs held by
live launcher sandboxes (`naming.IsLauncherSandbox`), and
`refuseReferencedImages` names every referencing project; `--force` skips the
in-use guard and is passed through to `ImageRemove`. `vm.RemoveSandbox` accepts
only `agents-sandbox-vm-*` names (a hard refusal), refuses a VM that is not
stopped unless `--force` (the refusal reads "is not stopped" so an
unknown/unfetchable status is not mislabelled "running"), and stops a forced
running VM only after the confirmation succeeds, immediately before removal. It
never touches `state.yaml`, so the recorded home volume survives and the next
run recreates the VM against it. Both commands, and the retrofitted
`volume remove`, confirm through `termio.ConfirmAction` unless the global `--yes`
is set; a refusal runs before the prompt, so it fails fast without prompting.
Declining returns the `termio.ErrCancelled` sentinel. Ownership/scope guards are
hard (VMPrefix / ImagePrefix); only the in-use and running-state guards are
`--force`-overridable. `state.FormatKeys` renders the referencing keys as sorted
`slug/agent` labels (a bare `slug` for a legacy agent-less key), and the shared
`naming.IsProjectSandbox`/`IsTaskSandbox`/`IsLauncherSandbox` predicates replace
the duplicated prefix checks across `image` and `pruning`.

### Slice 5 — Storage accounting

Tracked as issue **#155** (the canonical ticket for this slice).

- `df [--verbose] [--format json]` — category totals from the Slice 2 seam;
  `--verbose` adds per-object rows and host paths; `humanize.FormatBytes` for
  display.
- `storage` group alias — `storage df` and `storage prune` alias the top-level
  `df` and `prune`; both also remain top-level.

Depends on: Slice 0, Slice 2.

Implemented (issue #155) in `cmd/agents-sandbox/commands_storage.go` +
`storage_output.go`. `df` mirrors microsandbox's own `df` (see "Resolved
decisions → Storage accounting (Slice 5)"): human table
`TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE` in msb category order with
`humanize.FormatBytes`/`-`, a non-verbose footer, and `--verbose` item blocks
with host paths and retention reasons. `--format json` emits microsandbox's
category-keyed report shape plus a `runtime` key. The `storage` group reuses
`buildDFCmd`/`buildPruneCmd` verbatim, so the aliases cannot drift.

### Slice 6 — Sandbox file transfer (dropped)

`sandbox copy`/`cp` (host↔VM transfer over `SandboxFS`) is **dropped**: it is the
excluded data-portability surface, not part of the bounded management surface
this spec defines.

## Command surface contract

The full per-group contract, as the slices deliver it.

`volume`:

- `volume list` — table default; `-q/--quiet` (names only), `--format json`.
- `volume inspect <name>` — Slice 3 fields above.
- `volume remove <name>...` (`rm`) **exists** — by-name deletion with the
  fail-closed project-reference guard and `-f/--force`; now also confirms under
  `--yes` (Slice 4 retrofit).
- `volume prune` **exists** — unreferenced home volumes above `--age`. Gains the
  Slice 0 flags.
- `volume create` **excluded** — home volumes come from the runner image via
  `volume migrate`/`reset`/`edit`; a raw create would bypass that model.

`image`:

- `image list` — table default; `-q/--quiet` (references only), `--format json`.
- `image inspect <ref>` — Slice 3.
- `image remove <ref>...` (`rm`) — Slice 4; hard-refuses non-runner references,
  in-use guard overridable by `-f/--force`.
- `image prune` **exists** (extended) — keeps a live project's variants used
  within the prune age and in-use images. Gains `--yes`, `--format json`, and `-q`.
- `image pull`/`load`/`save` **excluded** — runner images are built, not moved.

`sandbox`:

- `sandbox list`, `sandbox run`, `sandbox shell`, `sandbox stop`,
  `sandbox kill` **exist**. Here `-f/--force` is a remove-state toggle, not a
  guard override: on an inactive VM (`Created`/`Stopped`/`Crashed`) it skips
  the stop/kill and removes the persisted state, and `--dry-run` removes
  nothing (#161).
- `sandbox remove <name>` (`rm`) — Slice 4; project VMs only, refuses a running
  VM unless `-f/--force` (stops it first), leaves persisted state intact.
- `sandbox inspect <name>` — Slice 3.
- `sandbox copy`/`cp` — Slice 6 (dropped).
- `sandbox start`/`restart`/`exec`/`logs`/`metrics`/`pause`/`resume`/`wait`
  **excluded** — lifecycle/exec surface agents-sandbox does not expose.
- `sandbox modify`/`fork`/`snap`/`ssh`/`install` **excluded** — no fit for the
  project-VM model (reconfiguration flows through reprovision, not `msb modify`).

`storage` / `df`:

- `df` — Slice 5: msb-shaped storage table; `--verbose` item blocks;
  `--format json` msb-shaped report; `-q` category labels only.
- `storage df` / `storage prune` — Slice 5 aliases of the top-level `df`
  (`also: storage df`) and `prune` (`also: storage prune`).

Out of scope (explicit): `registry`/`registries`, `context`/`ctx`, `self`/
`doctor`/`update`/`downgrade`/`uninstall`, and `sandbox compact` (issue #157).
Runtime-RAM pruning enriches the top-level `prune` rather than adding a command
(#156): a fourth phase calls microsandbox's `PruneStorage` over branch/snapshot
memory backings under the same `--older-than` threshold (converted to whole
seconds; a non-positive age means no age filter), honors `--dry-run` through the
runtime's own flag, and joins a failure into the aggregate error. The native
image-prune report's `SkippedInUse` and `BytesReclaimed` are surfaced in both the
human output and `--format json`; unknown byte counts stay unknown (`-` / `null`),
never `0`.

## Cross-cutting conventions (delivered by Slice 0)

- **Listing**: every list supports `-q/--quiet` and `--format json`; human tables
  remain the default. Identifiers mode writes through `termio.UI.Print`, which
  bypasses the global `--quiet` stdout suppression, so `-q` still emits
  identifiers. JSON renders an empty list as `[]`, never `null`. `--format json`
  with `--quiet`, or any `--format` other than `json`, is an error ("mutually
  exclusive" / "unsupported format").
- **Destructive**: every removal/prune supports `--yes` to authorize
  non-interactively, and every removal exposes `-f/--force` to override its
  reference/in-use/running-state guard. `--force` overrides those guards;
  ownership/scope guards (only agents-sandbox project VMs / runner images) are
  hard and not `--force`-overridable. `--yes` skips a confirmation prompt —
  distinct from `--force`, and they may coexist. Every removal command prompts
  (Slice 4 retrofitted `volume remove`, which previously never did), and the guard
  runs before the prompt so a refusal fails fast. A declined prompt returns the
  `termio.ErrCancelled` sentinel; a non-interactive invocation without `--yes`
  fails fast with `termio.ErrConfirmationRequired` and never parks on stdin.
  Removal `--force` flags bind with `BoolVarP` so the closure variable updates
  (`BoolP` parses the flag but leaves that variable false).
- **Age threshold**: `--older-than` is the canonical flag (msb's spelling),
  defaulting to `7d` and parsed with `viperconfig.ParseHumanDuration`; `--age` is
  deprecated (hidden, warns, still honored; both set together is an error).
- **Prune report**: `prune` and each group `prune` support `--format json` and
  `-q`. The JSON object carries the per-group counts (`sandboxes`, `volumes`,
  `images`, `docker_images`), a `dry_run` flag, and a `details` array of
  `type`/`name`/`slug`/`digest`/`stale_for_seconds` entries; each group prune's
  object carries only its own counts plus `dry_run`/`details`. The report is
  rendered before an error is returned: a prune whose
  per-group errors are joined (top-level `prune` and `AutoPrune`) still reports
  what it removed first ("summary, then error"). Output mode and flag validation
  (`--format`/`--quiet` mutex) run before the confirmation prompt so bad flags
  fail fast rather than being masked by a non-interactive `ErrConfirmationRequired`.
- **Tree**: new subcommands carry the `also: sandbox X` annotation and aliases
  consistent with the existing tree.
- **Guards are fail-closed**: an unreadable or corrupt project state file blocks
  a guarded removal unless `--force` is given.

## Resolved decisions

- **Image reference scan (Slice 1)**: project state is the source of truth for
  ownership; the scan is the union of the `HomeVolumeReferences` analogue and the
  live kept-sandbox image refs. Fail-closed on unreadable or corrupt state.
- **`df` category discovery (Slice 2)**: microsandbox's `StorageUsage` supplies
  the categories it owns; the private-home walk supplies the runtime/FFI
  artifacts (its `bin`/`lib`). Nil = unknown. The buildkit cache and docker
  images were deferred (see Slice 2).
- **`sandbox copy` (Slice 6)**: dropped.
- **Per-volume usage**: exposed in `volume inspect` only; `volume list` stays
  cheap (no per-object stat).
- **Inspect `-q` (Slice 3)**: the shared output mode applies to a single object
  too — `--quiet` prints the object's identifier (volume name, image reference,
  or sandbox name) and nothing else, matching the list convention.
- **Inspect JSON (Slice 3)**: identity/size fields are presentation-oriented
  human strings (matching the list JSON), nullable fields render as `-`,
  collections (env/cmd/entrypoint/layers/mounts) render as arrays.
- **Inspect seams (Slice 3)**: `image inspect` reads through a new
  `msb.ImageInspectDetail` (a local `msb.ImageDetail` DTO) rather than widening
  the existing config-only `msb.ImageInspect`, which the build digest check
  still uses; `sandbox inspect` needs `SandboxHandle.ID`, added to the seam.
- **Storage accounting (Slice 5)**: `df` mirrors microsandbox's `msb df`
  (`crates/cli/lib/commands/storage.rs`) rather than inventing a format —
  human columns `TYPE COUNT IN USE LOGICAL SIZE RECLAIMABLE` in msb's category
  order, `-` for unknown, `--verbose` item blocks per category, and a
  `--format json` object keyed by category (`images`, `snapshots`,
  `branch_memory`, `snapshot_memory`, `sandboxes`, `volumes`) with microsandbox's
  per-category fields (`count`, `in_use`, `logical_bytes`, `allocated_bytes`,
  `reclaimable_logical_bytes`, `items`, `notes`) plus top-level `notes` and our
  extra `runtime` key. Unknown counters are `null`; `items` is always present
  (`[]`). Deviations from msb: the extra `runtime` category, graceful
  degradation instead of erroring on `ErrUnsupportedOperation` (the note is
  surfaced on non-verbose output so an all-`-` table is explained), and our
  global `-q` printing category display labels (bare minimum, list-style).
  The non-verbose footer reads `logical sizes; '-' means unknown. use --verbose
  for details.`, and the `runtime` category leaves its `in_use` nil.
  `--verbose` affects only the human render; JSON always includes `items`
  (msb behaviour). The Slice 2 seam was realigned (snake_case names, msb order,
  `InUse`) to keep one source of truth; the CLI derives display labels and
  `storage prune` reuses `buildPruneCmd`.

## Left to the builder

- Testing strategy per slice (table/JSON goldens, guard paths, fail-closed
  scans) and the CLI integration tests per the repo's conventions.
- Docs (`docs/`, `README.md`) and `CHANGELOG.md` updates, per slice.
