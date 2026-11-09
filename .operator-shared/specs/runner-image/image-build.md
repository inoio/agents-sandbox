---
description: Contract for the runner-image build and its microsandbox cache - which agent version is baked, when the image is (re)built and when Docker's layer cache is bypassed (`BuildOptions.Force`), the lazy msb load decision over a content-addressed reference (`<slug>:<agent>.<contentid>`, with the Docker tag kept stable), `ParseImageTag`/ownership round-trip, and prune retention.
read_if: Changing image build/caching, `buildDecision`/`buildReasons`/`BuildOptions.Force`/`--rebuild`, `image.EnsureImage`/`Build`/`EnsureLoaded`/`ImageInfo`, `prepare.ResolveAgentVersionForBuild`/`resolveBuildVersion`/`recoverAgentVersion`, a profile's `LatestVersion`/`NewerThan`, `naming.RunnerImageRef`/`ImageIdentity`/`ParseImageTag`, `state.ImageReferences`, `pruning.PruneImages`/`keepImage`, or the volume subcommands' image handling; or diagnosing an unexpected `loading image … into microsandbox` on a `run`/`shell`.
---

# Runner image build and content addressing

How the rendered Dockerfile is arranged into stages (so layer caching works as assumed here) is owned by `specs/runner-image/image-composition.md`.

## Build decision vs cache bypass

`EnsureImageWithClient` makes two independent decisions from one input, `BuildOptions.Force`:

- **Build at all:** `buildDecision` (`internal/sandbox/image/inspect.go`) short-circuits on `Force`, then `buildReasons` requests a build when the host Docker image is absent or its recorded `dockerfile-id` differs from the rendered one. The `dockerfile-id` label is the source signature (rendered Dockerfile + resolved agent version + every build-context file with its name, mode, and content; the files travel via the build context rather than the Dockerfile text, so each is folded into the signature for an edit to invalidate a warm image — see `specs/runner-image/image-composition.md`); a mismatch means the rendered recipe changed and triggers a build. It is a signature of the declared inputs, not a content identity: it deliberately excludes host-dependent inputs (the resolved base digest and the dev user's uid/gid), so a label match does not imply equal image bytes.
- **Bypass cache:** `Force` is passed through to `NoCache` on the Docker build.

So `Force` means **clean rebuild**: rebuild even when the identity matches, with `NoCache=true`. It is set only by an explicit user `--rebuild` on `build`, `run`, or `shell`.

A build triggered by a stale identity (changed project Dockerfile, agent version, or base) reuses Docker's layer cache. This is safe because each change is visible to Docker's cache key: the agent version is rendered as the agent stage's install `ARG` default (and also passed as a build arg), so it is part of the Dockerfile content and invalidates the agent install layer, and the `dockerfile-id` label lives after the agent install. An accepted agent upgrade is a version change, not a force: it rebuilds the install layer while keeping earlier layers cached. The node and docker stages render their pinned versions the same way.

## Load decision

`EnsureImageWithClient` returns an `ImageInfo{DockerTag, Ref, Identity, Digest, DockerfileID, Env, AgentVersion}`. `DockerTag` is the stable Docker tag; `Ref` is the content-addressed microsandbox reference (`<slug>:<agent>.<contentid>`, derived from `Digest`); `Identity` is the structured `naming.ImageIdentity{Slug, Agent, ContentID}` `Ref` is rendered from, so no caller parses a reference the launcher built; `Digest` is Docker's image ID, i.e. the same content identity microsandbox computes for an image it holds (`config.digest = sha256(config_bytes)`), `DockerfileID` is the source signature above, and `AgentVersion` is the release the image reports through its `agent-version` label (empty for a missing or non-release sentinel label). Loading into microsandbox is deferred to a separate `EnsureLoaded` call, so reconnecting to a running VM never pays the import cost.

`EnsureLoaded(ctx, client, info, ui)` decides from the msb cache only, never by inspecting host Docker. It tries three candidate references in reuse-preference order and reuses the first that already holds the content:

1. the content ref `info.Ref` (`<slug>:<agent>.<contentid>`);
2. the prefix-free name ref `info.Identity.NameRef()` (`<slug>:<agent>-latest`; a 0.6.0 install cached under it);
3. the legacy prefixed Docker-tag ref `info.DockerTag` (`agents-sandbox/runner-<slug>:<agent>-latest`, a pre-0.6 cache).

A candidate is reused only when its cached digest equals `info.Digest`; a cached image with no digest is unknown content and is reloaded, never matched by the `dockerfile-id` label. A digest mismatch is never overridden by a matching label — the label is a source signature and can match while the bytes differ (base tag move + `--rebuild`). When no candidate matches, the Docker image is loaded under `info.Ref`; a load never removes a cached image first, because an existing VM may still reference it. The build/load split is deliberate: `run`/`shell` build eagerly via `EnsureImage` but load lazily, only when a VM is actually created or recreated.

Pruning keeps a candidate reference while a kept VM holds it, while its `(slug, agent)` key is the one the current `run`/`shell` is about to use, or while it parses an agent and was used within the prune age; a pruned key's refs, an agent-less ref, and an aged-out variant are reclaimed, so a `build` followed by a `run` reimports nothing. The native image-data prune (`Image.Prune`) runs only when the invocation retains nothing, so a `run`/`shell`'s startup auto-prune defers it and never discards the image it is about to load. The full reference shape and retention contract follow.

### Reference shape

The msb reference is a deterministic function of the image **content**; the Docker tag stays a stable name.

- **msb reference (content-addressed):** `<slug>:<agent>.<contentid>`, e.g. `myproject-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31`. `<contentid>` is a short, lowercase, filesystem-safe encoding of the image content identity: the Docker image ID (the `sha256:` config digest, which equals the msb config digest) with the `sha256:` prefix stripped, truncated to a fixed-length prefix (16 hex chars is the working choice; collision risk within one project is negligible). The `:` and any `sha256:` prefix must not appear, because the reference is `<slug>:<tag>` and the tag charset is `[A-Za-z0-9._-]`.
- The `.` separates the agent from the content id. Agent names are `[a-z0-9-]` (`opencode`, `opencode2`, `pi`, `claude-code`), so `.` is unambiguous and round-trips.
- **Docker tag (unchanged, stable):** `naming.DockerImageRef(slug, agent)` → `<DockerImagePrefix><slug>:<agent>-latest`. Only the msb reference is content-addressed; the Docker tag keeps holding the latest build for the project/agent (Docker retags cheaply, and `buildDecision`/`docker.ImageInspect`/`ImageSave`/`readImageInfoFromDocker` keep working unchanged).

`image.EnsureImageWithClient` sets `ImageInfo.Ref` from the inspected Docker image ID (it already inspects the built image for `Digest`), not from `naming.RunnerImageRef`. `ImageInfo.DockerTag` stays the Docker tag. A preview (`--dry-run`) keeps reporting the Docker tag, which is known before the build; the content ref is not knowable pre-build and is not previewed.

#### Migration and reconfig tolerance

The candidate set above expands so an upgrade reimports nothing. Parsing (`naming.ParseImageTag`) remains only where a reference arrives from outside this process's own construction: the microsandbox image list (pruning, `image list`/`remove`) and CLI arguments.

The reconfig plan's image comparison (`reconfig.sameImageRef`) takes the desired image as the structured identity and the persisted `cfg.Image` as the one reference that must be parsed (it is read back from the VM). It treats the content, prefix-free-name and prefixed-Docker-tag spellings of one `(slug, agent)` as the same image, so a VM created under an older spelling is not recreated merely because the launcher now computes a content ref. A digest-pinned or non-runner ref remains equal only to itself, and the launcher never computes one. The rule is asymmetric on `ContentID`:

- When **either** reference is a name/legacy spelling (no `ContentID`) — the upgrade from a 0.6.0 or pre-0.6 VM — same `(Slug, Agent)` is the same image.
- When **both** are content-addressed, the content ids must also match. A content-id difference is a genuine image switch that the persisted digest may already reflect: the image-change home action records the new `ImageDigest` *before* the VM is replaced (`volume.ApplyHomeAction`), so if the replacement fails or the process stops in between, the next run sees matching digests and the content ref is the remaining signal that the live VM is still on the old content. Comparing `(Slug, Agent)` alone would silently reconnect to the old VM instead of retrying the switch.

Without the name/legacy tolerance, every existing install takes a pointless VM recreate on first run even though the digest is unchanged.

#### Parse and ownership

`naming.ParseImageTag` gains the content shape and `ArtifactInfo` gains a `ContentID` field:

- `<slug>:<agent>.<contentid>` → `Slug`, `Agent`, `ContentID` set.
- `<slug>:<agent>-latest` (and the legacy prefixed form) → `Slug`, `Agent` set, `ContentID` empty, as today.
- any other tag → legacy `Digest`, as today.

The parsed `Slug`/`Agent` are what ownership and liveness key on, so `image.ListImages` (foreign-image exclusion), `image.RemoveImages`/`refuseNonRunnerRefs`, `naming.SandboxIdentity`, and `pruning` keep resolving content refs to the same `state.Key{Slug, Agent}` without a `ContentID` term. Ownership stays positional in the private runtime home; a content ref is a runner image exactly when its parsed slug embeds the launcher's 14-character slug hash (`naming.IsRunnerImageRef`), the predicate `image/list.go`, `image/remove.go` and `pruning/images.go` all apply.

`state.ImageReferences` keeps protecting the project's legacy refs (the prefix-free name ref and the prefixed Docker-tag ref). It need not enumerate every content ref: the in-use guard unions it with the live-sandbox scan (`image.InUseImageReferences`), which already protects the content ref any kept sandbox holds via its `config.Image`.

#### Retention

`pruning.keepImage` retains an msb image reference when **any** holds:

- A kept sandbox references it (`keptImageRefs`, the in-use content) — always, regardless of age.
- Its parsed `(slug, agent)` key is in `pruneState.RetainKeys` — the key the current invocation is about to use. A `run`/`shell` passes its own project key, so the auto-prune that runs at its start never reclaims the image it is about to import.
- Its parsed `(slug, agent)` key is not in `ToPrune`, it parses an agent, and the image was used within the prune age. Use the image handle's `LastUsedAt()` (fall back to `CreatedAt()` when it is the zero time). The age is the same threshold the prune invocation already resolves (`PruneState.Age`).

VM liveness (`ToKeep`) deliberately no longer gates image retention. A project with no VM is not yet retired: a `build` loads its image before any VM exists, and the run that follows must reuse it rather than reimport it, so a VM-less key's ref survives while it is within the age. Only a `ToPrune` key (its stale VM is being reclaimed in this pass), an agent-less ref, and an aged-out variant are surplus. `PruneImages` parses each listed reference once and passes the resulting `ArtifactInfo` into `keepImage`, so a reference read back from the microsandbox cache is never parsed twice.

The native microsandbox image-data prune (`Image.Prune`) is a second, independent reclaimer: it removes cached image data that **no sandbox references** and has no retention parameter, so it cannot honor `RetainKeys` at all. `PruneImages` therefore invokes it only when the invocation retains nothing. A `run`/`shell`'s startup auto-prune passes its own key, so it skips the native prune and defers that reclamation to the next explicit `prune` — otherwise the native sweep would discard the image the run is about to load, which `keepImage`'s ref-level retention cannot prevent.

## Version selection

`prepare.ResolveAgentVersionForBuild(k, requested)` is the one precedence rule both `build` and `run`/`shell` apply: a user-provided agent (`agent-source=user`) ignores the requested version and returns the recorded one with `UserProvided=true`; every other project honors the requested version with `UserProvided=false`. `resolveBuildVersion` (run/shell) builds on it for the version to bake:

1. **User-provided agent** (`agent-source=user`) → recorded version, no check; any `--agent-version` pin is **ignored** (the tool does not own the agent, so a pin cannot change what is baked).
2. **`--agent-version <v>` pin** → `v`, no upgrade check.
3. **No upgrade checker** → recorded version, no check.
4. **No recorded baseline, or one that disagrees with the existing image's label** → take the release the image reports through its `agent-version` label (`prepare.recoverAgentVersion` via `image.ExistingAgentVersion`) and rewrite the cache, then continue to the upgrade check below. The image is the authority; a disagreeing record is stale and never drives a build. Only when no image (or no release label) yields a version does an empty record resolve to latest at build time.
5. **Recorded (or label-reconciled) baseline** → upgrade check; may prompt to rebuild with a newer release.

`--agent-version` is available on `build`, `run`, and `shell`. `--rebuild` is orthogonal: it only forces a clean rebuild of whichever version is selected and does **not** suppress the upgrade check. `build` has no upgrade check, but it is not always a fresh version: for a user-provided agent it, like `run`, ignores the pin and reuses the recorded version (`UserProvided=true`), resolving a release only if a rebuild later needs one.

The image-ensuring paths are the writers of record for the baseline: `image.EnsureImage` (via `ImageInfo.AgentVersion`) and `image.Build` (which returns the `ImageInfo` from `EnsureLoaded`) surface the baked release so the caller records it in `HomeState.agent_version`. See `../state/upgrade-state.md`.

### Latest-version sources

Each profile's `LatestVersion` (the upgrade-check source) must mirror the source that profile's upstream installer actually resolves, so the pinned version is always one the installer can fetch:

- `opencode` → GitHub `releases/latest`; `pi` → `pi.dev`; `claude-code` → npm `latest` dist-tag.
- `opencode2` → the opencode update API's `latest` channel (`https://opencode.ai/update/api/latest/cli/npm`), the same endpoint `https://opencode.ai/v2/install` queries when unpinned. Do **not** read an npm dist-tag here: the deprecated `@opencode-ai/cli` `beta` tag resolves to a version whose platform tarball ships `bin/opencode2`, which the v2 installer (expecting `bin/opencode`) cannot install, so the runner-image build fails at the installer's `mv` step. The `0.0.0-beta-<build>` numeric-comparison path in `newerOpenCode2Than` is retained only to compare against a previously baked pre-GA snapshot.

## Callers

- `build` / `image build` → `image.Build` → `Force = -r/--rebuild`.
- `run` / `shell` → `PrepareSandbox` → `Force = opts.Rebuild` (`-r/--rebuild`). An accepted upgrade (`shallUpgrade`) is **not** a force; it triggers the build via the changed version identity.
- `volume migrate` / `volume reset` / `volume edit` → `image.EnsureImage` → `Force = false`. Each still exposes `--rebuild` as a hidden, deprecated no-op (restored for one cycle — see `../conventions/deprecation-policy.md`); it never forces a clean rebuild.

## Invariants

- `BuildOptions.Force` is the only input that bypasses the layer cache; a stale-image rebuild never does.
- `--rebuild` on the volume subcommands is a deprecated no-op; the only real cache bypass is `BuildOptions.Force` on `build`/`run`/`shell`.
- The internal build helper parameter that maps to `NoCache` is named `noCache`, not `force`, so the cache meaning stays explicit at the build call.
- The msb reference is a pure, deterministic function of `(slug, agent, docker image ID)`; no random uuid/hash — a repeat run for identical content resolves to the same reference and reimports nothing.
- The content id is the **content** identity (Docker image ID / msb config digest), never the `dockerfile-id` source signature: the source signature excludes the resolved base digest and the dev user's uid/gid, so it can match while the bytes differ (base tag move + `--rebuild`); keying the cache on it would silently reuse the wrong content.
- `ImageInfo.DockerTag` remains the stable Docker tag; only `ImageInfo.Ref` is content-addressed. `ImageInfo.Identity` is the image's structured identity and never changes; `ImageInfo.Ref` is the microsandbox reference that actually holds the content, so it can be a legacy candidate `EnsureLoaded` reused. Identity is set by the same call that renders the initial `Ref`, so no caller parses a reference it constructed.
- Ownership/liveness resolve from `(slug, agent)` alone; `ContentID` never participates in a `state.Key`.
- No legacy msb image with a recorded digest is reimported by this change: the name ref and the prefixed ref are still reused when their content matches. An image that records no digest is reloaded once (a load records a digest).

## Non-goals / limitations

- **The Docker rebuild and the VM recreate are not avoided.** Switching variants still rebuilds the Docker image (Docker layer cache makes it cheap) and changes the image digest, so `reconfig.PlanReconfig` still recreates the VM and still asks the image-change home-volume question. This removes the *reimport* only; removing the rebuild/recreate would require the docker capability to stop being an image axis, which is a separate decision.
- No GC of a live project's variants by count, and no pinning; only the age window above.
- No change to the Docker tag or its prefix, and no rename/rewrite of existing msb images (write-new, read-all-both).
- The content id length and encoding are the builder's call within the shape above; a change to it is not a contract change as long as it stays deterministic and content-derived.
