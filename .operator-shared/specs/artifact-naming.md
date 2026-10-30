---
description: Contract for the prefix-free artifact naming strategy: sandboxes, home volumes and microsandbox image references drop the `agents-sandbox-`/`agents-sandbox/runner-` prefix while Docker images keep it, new artifacts follow the new scheme and existing ones stay discoverable (write-new, read-both), and artifact discovery relies on the private runtime home plus labels rather than a name prefix.
read_if: Changing `internal/sandbox/naming`, the VM/home-volume/runner-image name or reference constructors or parsers, artifact discovery (list/remove/prune/reference scans), the sandbox or volume labels, the Docker-vs-msb image reference split (`ImageInfo`), or diagnosing a legacy artifact that stopped being found, reimported, or rebuilt after the naming change.
---

# Prefix-free artifact naming

## Intent

Names and references of the artifacts agents-sandbox creates in the microsandbox runtime must **not** carry the
vendor prefix:

| Artifact | Before | After |
|---|---|---|
| project VM | `agents-sandbox-vm-<slug>-<agent>` | `<slug>-<agent>` |
| task sandbox | `agents-sandbox-task-<slug>-<agent>-<nanos>` | `<slug>-<agent>-<nanos>` |
| home volume | `agents-sandbox-home-<slug>-<agent>-<ts>` | `<slug>-<agent>-<ts>` |
| microsandbox runner image ref | `agents-sandbox/runner-<slug>:<agent>-latest` | `<slug>:<agent>.<contentid>` |
| Docker runner image | `agents-sandbox/runner-<slug>:<agent>-latest` | **unchanged** |

Docker keeps the prefix so our runner images are distinguishable from user images in the system Docker daemon. This
is a naming change inside a runtime we own; it needs no runtime replacement, and it is not a user-facing rename (the
names are internal keys, not CLI dialect).

The produced microsandbox runner reference is **content-addressed** (`specs/image-ref-content-addressing.md`):
`<slug>:<agent>.<contentid>` keys each distinct image content, so a project's variants coexist. `<slug>:<agent>-latest`
survives as the *name* form, and the pre-0.6 `agents-sandbox/runner-<slug>:<agent>-latest` string as the *legacy* form;
both are still read, never produced.

`<slug>` is `<folder≤20>-<14-char base36>` (`internal/git`), lowercase and alphanumeric-starting, so every target
name still satisfies the runtime's name rules (sandbox ≤128 bytes / alnum-start / `[A-Za-z0-9._-]`; volume same
charset) and the derived guest hostname (≤64 bytes). The shorter names are a side benefit: `<slug>-<agent>` is ~47
bytes, so msb no longer truncates and hash-suffixes the guest hostname, and `projectVMName`'s 128-byte truncation
stops biting.

## The Docker/msb reference split

`image.ImageInfo.Tag` today is simultaneously the Docker tag and the microsandbox reference. The two must split:

- `ImageInfo.DockerTag` — `agents-sandbox/runner-<slug>:<agent>-latest`; used by `buildDecision`, `buildFromPlan`,
  `docker.ImageInspect`, `docker.ImageSave`, `readImageInfoFromDocker`.
- `ImageInfo.Ref` — the resolved microsandbox reference (new or legacy, see below); used by `ImageGet`,
  `ImageInspect`, `ImageLoad`, `ImageRemove`, and `WithImage`.

The Docker tag is exactly `agents-sandbox/runner-` + the new msb ref, so the split is mechanical. Because the Docker
tag is unchanged, this never triggers a rebuild — at most a one-time `ImageSave`/`ImageLoad` into msb under the new
ref.

## Write-new, read-both

New artifacts are created under the new scheme; lookups accept either scheme, so existing artifacts keep working and
are neither renamed, reimported nor rebuilt. `naming` owns the production constructors (`TaskSandboxName`,
`HomeVolumeName`, `RunnerImageRef`, `ContentImageRef`, `DockerImageRef`, and `ImageIdentity`, whose `Ref`/`NameRef`
render the image references for a structured `(slug, agent, contentID)`) and its parsers accept both shapes; the project-VM name is built
in `vm` (it needs truncation) and legacy names are only ever parsed, never constructed in production. Tests build
scheme-shaped fixtures from the exported prefixes.

- **Home volume — recorded, therefore free.** The name is persisted in `HomeState.HomeVolume`, so an existing project
  resolves its volume by the recorded name and never rotates it. Only the list/parse paths must accept the legacy
  shape so the volume still appears and is still classified. A legacy-named volume stays legacy until the project
  next rotates its volume.
- **msb image — resolve by content.** The produced ref is content-addressed, derived from the image content id
  (`<slug>:<agent>.<contentid>`, see `specs/image-ref-content-addressing.md`); the name ref `<slug>:<agent>-latest` and
  the legacy prefixed ref are read-only fallbacks. Resolution is: if any candidate already holds the current content
  (`cachedImageMatches`), reuse it — no reimport; otherwise load under the content ref. The digest check is what makes
  "not reimported" exact: a fallback ref is reused only when it already holds the current content. Changed content
  imports under a new content ref, silently migrating that project and leaving the old variant cached for pruning.
- **Sandbox — lookup fallback.** The name is derived, not stored: try `GetSandbox(newName)`, then
  `GetSandbox(legacyName)`, and create under the new name only when neither exists. A reused legacy VM keeps its
  legacy name until it is next recreated (reconfig, or a fresh session after it is gone), which is not a rebuild
  caused by the rename.

`state.ImageReferences` and `vm/adopt.go`, which recompute the legacy runner-image ref for ownership/adoption, must
consider both refs; a content ref needs no state entry because the live-sandbox scan (`image.InUseImageReferences`)
protects whatever ref a kept sandbox holds via its `config.Image`.

## Ownership vs classification

Ownership is positional for sandboxes and volumes: the private runtime home (`configpaths.MSBHome()`) is ours alone and
the external-runtime escape hatch was removed (`runtime-home-decoupling.md`), so their prefix-based "is this ours?"
filters in `vm/list.go` and `volume/list.go` are dropped. **Runner-image ownership is deliberately a per-object
name-shape predicate**: the parsed slug must embed the 14-character base36 hash every launcher-built slug ends with
(`naming.IsRunnerImageRef`, reusing `isBase36Hash`). A prefix test cannot stand in for it — a manually loaded
single-segment image can live in the private runtime home, and the list seam (`msb.ImageList`) exposes no image labels,
so neither the prefix nor the label route can tell it apart. `image/list.go`, `image/remove.go` and
`pruning/images.go` all apply that one predicate, so list, remove and prune agree; `image/remove.go`'s
`refuseNonRunnerRefs` refuses a hashless ref without `--force` overriding it. Do not "simplify" this back to
`naming.ParseImageTag(ref).Slug == ""`.

Classification and identity **are** still needed, and both come from labels on anything the launcher creates:

- Every sandbox is created with `org.agents-sandbox.type` (`project`/`task`), `org.agents-sandbox.slug` (the full
  checkout slug, even when the name was truncated) and `org.agents-sandbox.agent`. Project VMs keep
  `org.agents-sandbox.project` (repo slug, for `list --label`) and `.image`. A home volume carries no sandbox label;
  its identity is name- or state-derived.
- `naming.SandboxIdentity(name, labels)` is the resolver: labels win, else the name heuristic (`ParseVMName` plus the
  legacy `org.agents-sandbox.task` marker) for a sandbox created before the labels existed. `Config().Labels` is the
  read path; a config error degrades to the name fallback rather than failing.
- The label removes the ambiguity the name shape cannot resolve: a project whose folder itself begins with a legacy
  prefix (a folder literally named `agents-sandbox-vm`/`-task`/`-home`) produces a prefix-free name that *starts with
  that prefix*. `ParseVMName`/`ParseHomeVolumeName` therefore strip a legacy prefix only when the stripped remainder
  is a hash-anchored legacy body, otherwise they parse the whole name prefix-free. This also anchors on the *rightmost*
  whole 14-char segment, so a hash-shaped folder segment does not shadow the real slug hash — and a legacy digest-named
  home volume (`<folder>-<hash>-<digest>`, two hash-shaped segments) is split between the digest and the project hash
  rather than folding the digest into the slug.
- Pruning keeps transient task sandboxes out of the project-key sets: `PruneState.Tasks` holds them (reclaimed by
  `PruneSandboxes`) while `ToPrune`/`ToKeep` hold only project VMs. A leftover stopped task can therefore never mark
  its still-active project stale (which otherwise reclaims the project's runner image and an unrecorded home volume).
- Volumes have one kind (home volumes); images have one kind (runner images). Neither needs a marker.

Two name-shape checks survive for a different reason: `runtimemigration/legacy.go` scans the **legacy
`~/.microsandbox`**, which a user's own msb shared, so `IsHomeVolume`/`IsLauncherSandbox` are the only way to avoid
copying foreign data there. Those predicates stay (and accept both shapes).

## Non-goals

- No Docker image rename; the Docker prefix stays.
- No rename/rewrite of existing artifacts — legacy objects are read, never migrated in place.
- No change to the slug (`internal/git`) or to the state schema/directory keys.
- No single "which scheme is live" config switch; both schemes coexist indefinitely for reading.

## Acceptance

- New runs create `<slug>-<agent>`, `<slug>-<agent>-<ts>` and `<slug>:<agent>.<contentid>`; the Docker tag keeps
  `agents-sandbox/runner-`.
- A project with a legacy home volume, legacy msb image ref and legacy project VM: the volume is still listed and
  resolved, the image is reused (no reimport) while its content matches, and the VM is reused (no rebuild); once the
  VM is recreated it comes back under the new name.
- `volume list` / `sandbox list` / `image list` show both schemes; task sandboxes never appear in `sandbox list`.
- `pruning` reclaims legacy-named task sandboxes and surplus refs, and keeps the refs a live project VM holds plus its
  variants used within the prune age. A leftover stopped task sandbox never makes its live project's image or
  unrecorded home volume prunable.
- A project whose folder begins with a legacy prefix (`agents-sandbox-vm`/`-task`/`-home`) keeps its full slug and is
  not misclassified.
- `make check` green; `CHANGELOG.md` Unreleased line; `docs/how-it-works.md`, `docs/sandboxes.md` and
  `docs/runner-image.md` updated for the new names/references.
