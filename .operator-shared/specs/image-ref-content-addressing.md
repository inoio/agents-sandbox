---
description: Contract for keying the microsandbox runner-image cache by image content instead of a constant name/reference, so a project's image variants (docker on/off, agent bumps, body edits) coexist in msb and toggling reimports nothing; the Docker tag stays stable, ParseImageTag round-trips the content suffix, and prune retains a ref held by a kept VM, the current invocation's own key, or any agent ref used within the age, and reclaims a pruned key's refs and aged-out variants, while the native image-data prune runs only when the invocation retains nothing.
read_if: Changing the msb runner-image reference (`naming.RunnerImageRef`, `naming.ImageIdentity`, `ParseImageTag`), `image.ImageInfo.Identity`, `image.EnsureImageWithClient`/`EnsureLoaded` and the load decision, `state.ImageReferences`, `pruning.PruneImages`/`keepImage` image retention, or diagnosing an unexpected `loading image ... into microsandbox` on a `run`/`shell`.
---

# Content-addressed runner-image references

Tracking issue: [inoio/agents-sandbox#322](https://github.com/inoio/agents-sandbox/issues/322).

## Problem

The microsandbox runner-image cache holds **one manifest per reference**, and the reference is a constant derived from the
project and agent: `naming.RunnerImageRef(slug, agent)` → `<slug>:<agent>-latest`. The Docker tag is a different constant
string (`<DockerImagePrefix>` + the same). So the cache key is a *name*, not the content.

Any input change that is not the Docker tag — `--docker` on/off, an agent-version bump, a project-Dockerfile edit, a base
tag move — rebuilds the image under the same name. `EnsureLoaded` then sees the msb-cached digest differ from the freshly
built Docker image ID and reimports (`ImageSave` from Docker, `ImageLoad` into msb). Alternating `--docker` reimports the
whole image on every run, which is the reported symptom; the same happens for any other content change.

## Contract

The msb reference becomes a deterministic function of the image **content**; the Docker tag stays a stable name.

### Reference shape

- **msb reference (content-addressed):** `<slug>:<agent>.<contentid>`, e.g.
  `myproject-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31`. `<contentid>` is a short, lowercase, filesystem-safe encoding of
  the image content identity: the Docker image ID (the `sha256:` config digest, which equals the msb config digest) with
  the `sha256:` prefix stripped, truncated to a fixed-length prefix (16 hex chars is the working choice; collision risk
  within one project is negligible). The `:` and any `sha256:` prefix must not appear, because the reference is
  `<slug>:<tag>` and the tag charset is `[A-Za-z0-9._-]`.
- The `.` separates the agent from the content id. Agent names are `[a-z0-9-]` (`opencode`, `opencode2`, `pi`,
  `claude-code`), so `.` is unambiguous and round-trips.
- **Docker tag (unchanged, stable):** `naming.DockerImageRef(slug, agent)` → `<DockerImagePrefix><slug>:<agent>-latest`.
  Only the msb reference is content-addressed; the Docker tag keeps holding the latest build for the project/agent (Docker
  retags cheaply, and `buildDecision`/`docker.ImageInspect`/`ImageSave`/`readImageInfoFromDocker` keep working unchanged).

`image.EnsureImageWithClient` sets `ImageInfo.Ref` from the inspected Docker image ID (it already inspects the built image
for `Digest`), not from `naming.RunnerImageRef`. `ImageInfo.DockerTag` stays the Docker tag. A preview (`--dry-run`) keeps
reporting the Docker tag, which is known before the build; the content ref is not knowable pre-build and is not previewed.

### Load decision and migration

`EnsureLoaded` keeps its rule — load only when neither cached reference already holds `info.Digest` — but the set of
candidate references expands so an upgrade reimports nothing:

1. The content ref `info.Ref`.
2. The prefix-free name ref `naming.RunnerImageRef(slug, agent)` (`<slug>:<agent>-latest`): the reference a 0.6.0 install
   cached under.
3. The legacy prefixed Docker-tag ref `info.DockerTag` (`<DockerImagePrefix><slug>:<agent>-latest`): the reference a
   pre-0.6 install cached under.

A candidate is reused only when its cached content matches (`cachedImageMatches`): the microsandbox config digest must
equal `info.Digest`. A cached image that records no digest is unknown content and is reloaded — the `dockerfile-id`
label is a source signature, not a content identity, and must not stand in for the digest (a floating base tag can move
the bytes under a matching label). Reloading is the deliberate cost: a load records a digest, so such an image matches
on the next run instead. `ImageInfo` carries the image's structured identity (`naming.ImageIdentity{Slug, Agent,
ContentID}`) alongside its rendered references, set by `EnsureImageWithClient` in the same expression that renders
`Ref`; `EnsureLoaded` derives the name-ref candidate from it, so a reference the launcher itself constructed is never
parsed back. Parsing (`naming.ParseImageTag`) remains only where a reference arrives from outside this process's own
construction: the microsandbox image list (pruning, `image list`/`remove`) and CLI arguments.

### Reconfig tolerance

The plan's image comparison (`reconfig.sameImageRef`) takes the desired image as the structured identity and the
persisted `cfg.Image` as the one reference that must be parsed (it is read back from the VM). It treats the content,
prefix-free-name and prefixed-Docker-tag spellings of one `(slug, agent)` as the same image, so a VM created under an
older spelling is not recreated merely because the launcher now computes a content ref. A digest-pinned or non-runner
ref remains equal only to itself, and the launcher never computes one. The rule is asymmetric on `ContentID`:

- When **either** reference is a name/legacy spelling (no `ContentID`) — the upgrade from a 0.6.0 or pre-0.6 VM — same
  `(Slug, Agent)` is the same image.
- When **both** are content-addressed, the content ids must also match. A content-id difference is a genuine image
  switch that the persisted digest may already reflect: the image-change home action records the new `ImageDigest`
  *before* the VM is replaced (`volume.ApplyHomeAction`), so if the replacement fails or the process stops in between,
  the next run sees matching digests and the content ref is the remaining signal that the live VM is still on the old
  content. Comparing `(Slug, Agent)` alone would silently reconnect to the old VM instead of retrying the switch.

Without the name/legacy tolerance, every existing install takes a pointless VM recreate on first run even though the
digest is unchanged — the recreate the spec's migration clause forbids.

### Parse and ownership

`naming.ParseImageTag` gains the content shape and `ArtifactInfo` gains a `ContentID` field:

- `<slug>:<agent>.<contentid>` → `Slug`, `Agent`, `ContentID` set.
- `<slug>:<agent>-latest` (and the legacy prefixed form) → `Slug`, `Agent` set, `ContentID` empty, as today.
- any other tag → legacy `Digest`, as today.

The parsed `Slug`/`Agent` are what ownership and liveness key on, so `image.ListImages` (foreign-image exclusion),
`image.RemoveImages`/`refuseNonRunnerRefs`, `naming.SandboxIdentity`, and `pruning` keep resolving content refs to the
same `state.Key{Slug, Agent}` without a `ContentID` term. Ownership stays positional in the private runtime home; a
content ref is a runner image exactly when its parsed slug embeds the launcher's 14-character slug hash
(`naming.IsRunnerImageRef`), the predicate `image/list.go`, `image/remove.go` and `pruning/images.go` all apply
(#350, #383).

`state.ImageReferences` keeps protecting the project's legacy refs (the prefix-free name ref and the prefixed Docker-tag
ref). It need not enumerate every content ref: the in-use guard unions it with the live-sandbox scan
(`image.InUseImageReferences`), which already protects the content ref any kept sandbox holds via its `config.Image`.

### Retention

`pruning.keepImage` retains an msb image reference when **any** holds:

- A kept sandbox references it (`keptImageRefs`, the in-use content) — always, regardless of age.
- Its parsed `(slug, agent)` key is in `pruneState.RetainKeys` — the key the current invocation is about to use. A
  `run`/`shell` passes its own project key, so the auto-prune that runs at its start never reclaims the image it is
  about to import.
- Its parsed `(slug, agent)` key is not in `ToPrune`, it parses an agent, and the image was used within the prune age.
  Use the image handle's `LastUsedAt()` (fall back to `CreatedAt()` when it is the zero time). The age is the same
  threshold the prune invocation already resolves (`PruneState.Age`).

VM liveness (`ToKeep`) deliberately no longer gates image retention. A project with no VM is not yet retired: a `build`
loads its image before any VM exists, and the run that follows must reuse it rather than reimport it, so a VM-less key's
ref survives while it is within the age. Only a `ToPrune` key (its stale VM is being reclaimed in this pass), an
agent-less ref, and an aged-out variant are surplus. `PruneImages` parses each listed reference once and passes the
resulting `ArtifactInfo` into `keepImage`, so a reference read back from the microsandbox cache is never parsed twice.

The native microsandbox image-data prune (`Image.Prune`) is a second, independent reclaimer: it removes cached image
data that **no sandbox references** and has no retention parameter, so it cannot honor `RetainKeys` at all. `PruneImages`
therefore invokes it only when the invocation retains nothing. A `run`/`shell`'s startup auto-prune passes its own key,
so it skips the native prune and defers that reclamation to the next explicit `prune` — otherwise the native sweep would
discard the image the run is about to load, which `keepImage`'s ref-level retention cannot prevent.

The `-latest` suffix test in the current `keepImage` must be dropped (keeping on the parsed agent alone): a content ref
does not end in `-latest`, and ownership already implies the per-agent tag. This clause is already the shape on `main`
(forward-port note).

## Invariants

- The msb reference is a pure, deterministic function of `(slug, agent, docker image ID)`; no random uuid/hash — a repeat
  run for identical content resolves to the same reference and reimports nothing.
- The content id is the **content** identity (Docker image ID / msb config digest), never the `dockerfile-id` source
  signature. The source signature excludes the resolved base digest and the dev user's uid/gid, so it can match while the
  bytes differ (base tag move + `--rebuild`); keying the cache on it would silently reuse the wrong content.
- `ImageInfo.DockerTag` remains the stable Docker tag; only `ImageInfo.Ref` is content-addressed.
- `ImageInfo.Identity` is the image's structured identity and never changes; `ImageInfo.Ref` is the microsandbox
  reference that actually holds the content, so it can be a legacy candidate `EnsureLoaded` reused. Identity is set by
  the same call that renders the initial `Ref`, so no caller has to parse a reference it constructed.
- Ownership/liveness resolve from `(slug, agent)` alone; `ContentID` never participates in a `state.Key`.
- No legacy msb image with a recorded digest is reimported by this change: the name ref and the prefixed ref are still
  reused when their content matches. An image that records no digest is reloaded once (a load records a digest), because
  a missing digest is unknown content and the source-signature label cannot vouch for it.

## Non-goals / limitations

- **The Docker rebuild and the VM recreate are not avoided.** Switching variants still rebuilds the Docker image (Docker
  layer cache makes it cheap) and changes the image digest, so `decideReconfig` still recreates the VM and still asks the
  image-change home-volume question. This change removes the *reimport* only; removing the rebuild/recreate would require
  the docker capability to stop being an image axis (always-baked or runtime-installed), which is a separate decision.
- No GC of a live project's variants by count, and no pinning; only the age window above.
- No change to the Docker tag or its prefix, and no rename/rewrite of existing msb images (write-new, read-all-both).
- The content id length and encoding are the builder's call within the shape above; a change to it is not a contract
  change as long as it stays deterministic and content-derived.

## Spec amendments

- `artifact-naming.md`: the msb image ref is no longer `<slug>:<agent>-latest` as the produced form — that becomes the
  legacy/name form only; the produced msb ref is content-addressed. The "resolve by content" clause already established
  for the Docker/msb split extends to the content suffix.
- `runner-image-build.md`: the "Load decision" section gains the candidate-reference set (content, name, prefixed) and
  the `ImageInfo.Identity` field the name ref is derived from; the retained-reference rule changes from "per-agent
  `-latest`" to "all variants of a live key within the prune age".

## Acceptance

- `asb run` (no `--docker`), then `asb run --docker`, then `asb run` again: each variant is imported into msb at most
  once; the second and third runs import nothing (`--verbose` shows no `loading image ... into microsandbox`).
- `asb build`, then the first `asb run`: the image is imported once (by the build); the run reuses it and imports
  nothing. The run's startup auto-prune must not reclaim the image the project is about to use — neither its ref
  (`keepImage`) nor its image data (the native `Image.Prune`, which is deferred to an explicit `prune`).
- Same for an agent-version bump and back, and for a project-Dockerfile edit and back.
- An existing 0.6.0 install (cached under `<slug>:<agent>-latest`) and a pre-0.6 install (cached under the prefixed ref)
  each keep their cache when the content matches — no reimport on upgrade, and no VM recreate while the image digest is
  unchanged (`reconfig.sameImageRef` tolerates the content spelling).
- `image list` shows every content ref; `image remove <content-ref>` refuses while a kept sandbox holds it and otherwise
  removes it; `image prune` keeps a project's recent variants (with or without a running VM) and removes a pruned key's
  refs and aged-out variants (dry-run reports the same).
- `ParseImageTag`/`SandboxIdentity` round-trip a content ref to the same `(slug, agent)` `state.Key` (fuzz coverage).
- `make check` green; `CHANGELOG.md` Unreleased line; docs updated where they describe the runner-image cache.
