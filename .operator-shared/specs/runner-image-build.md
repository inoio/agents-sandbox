---
description: Contract for deciding which agent version is baked into the runner image, when it is (re)built, and when the Docker layer cache is bypassed.
read_if: Changing image build/caching, `ImageInfo`/the load decision, BuildOptions, `--rebuild`, `--agent-version`, agent upgrade, or the volume subcommands' image handling.
---
# Runner image build and cache contract

How the rendered Dockerfile is arranged into stages (so layer caching works as assumed here) is owned by
`specs/image-composition.md`.

## Build decision vs. cache bypass

`EnsureImageWithClient` makes two independent decisions from one input, `BuildOptions.Force`:

- **Build at all:** `buildDecision` (`internal/sandbox/image/inspect.go`) short-circuits on `Force`, then `buildReasons`
  requests a build when the host Docker image is absent or its recorded `dockerfile-id` differs from the rendered one. The
  `dockerfile-id` label is the source
  signature (rendered Dockerfile + resolved agent version + every build-context file with its name, mode, and content;
  the files travel via the build context rather than the Dockerfile text, so each is folded into the signature for an
  edit to invalidate a warm image — see `specs/image-composition.md`); a mismatch means the rendered recipe changed and
  triggers a build. It is a
  signature of the declared inputs, not a content identity: it deliberately excludes host-dependent inputs (the
  resolved base digest and the dev user's uid/gid), so a label match does not imply equal image bytes.
- **Bypass cache:** `Force` is passed through to `NoCache` on the Docker build.

So `Force` means **clean rebuild**: rebuild even when the identity matches, with `NoCache=true`. It is set only by an
explicit user `--rebuild` on `build`, `run`, or `shell`.

A build triggered by a stale identity (changed project Dockerfile, agent version, or base) reuses Docker's layer cache. This
is safe because each change is visible to Docker's cache key: the agent version is rendered as the agent stage's install `ARG`
default (and also passed as a build arg), so it is part of the Dockerfile content and invalidates the agent install layer, and
the `dockerfile-id` label lives after the agent install. An accepted agent upgrade is a version change, not a force: it
rebuilds the install layer while keeping earlier layers cached. The node and docker stages render their pinned versions the
same way (see `specs/image-composition.md`).

## Load decision

`EnsureImageWithClient` returns an `ImageInfo{DockerTag, Ref, Identity, Digest, DockerfileID, Env, AgentVersion}`. `DockerTag` is the stable
Docker tag; `Ref` is the content-addressed microsandbox reference (`<slug>:<agent>.<contentid>`, derived from `Digest`);
`Identity` is the structured `naming.ImageIdentity{Slug, Agent, ContentID}` `Ref` is rendered from, so no caller has to
parse a reference the launcher built;
`Digest` is Docker's image ID, i.e. the same content identity microsandbox computes for an image it holds
(`config.digest = sha256(config_bytes)`), `DockerfileID` is the source signature above, and `AgentVersion` is the release
the image reports through its `agent-version` label (empty for a missing or non-release sentinel label). Loading into
microsandbox is deferred to a separate `EnsureLoaded` call, so reconnecting to a running VM never pays the import cost.

`EnsureLoaded(ctx, client, info, ui)` decides from the msb cache only, never by inspecting host Docker. It tries three
candidate references in reuse-preference order and reuses the first that already holds the content:

1. the content ref `info.Ref` (`<slug>:<agent>.<contentid>`);
2. the prefix-free name ref `info.Identity.NameRef()` (`<slug>:<agent>-latest`; a 0.6.0 install cached under it);
3. the legacy prefixed Docker-tag ref `info.DockerTag` (`agents-sandbox/runner-<slug>:<agent>-latest`, a pre-0.6 cache).

A candidate is reused only when its cached digest equals `info.Digest`; a cached image with no digest is unknown content
and is reloaded, never matched by the `dockerfile-id` label. A digest mismatch is never overridden by a matching label —
the label is a source signature and can match while the bytes differ (base tag move + `--rebuild`). When no candidate
matches, the Docker image is loaded under `info.Ref`; a load never removes a cached image first, because an existing VM
may still reference it. The build/load split is deliberate:
`run`/`shell` build eagerly via `EnsureImage` but load lazily, only when a VM is actually created or recreated.

Pruning keeps a candidate reference while a kept VM holds it, while its `(slug, agent)` key is the one the current
`run`/`shell` is about to use, or while it parses an agent and was used within the prune age; a pruned key's refs, an
agent-less ref, and an aged-out variant are reclaimed, so a `build` followed by a `run` reimports nothing. The full
retention contract is `specs/image-ref-content-addressing.md`.

## Version selection

`vm.ResolveAgentVersionForBuild(k, requested)` is the one precedence rule both `build` and `run`/`shell` apply: a
user-provided agent (`agent-source=user`) ignores the requested version and returns the recorded one with
`UserProvided=true`; every other project honors the requested version with `UserProvided=false`. `resolveBuildVersion`
(run/shell) builds on it for the version to bake, with this precedence:

1. **User-provided agent** (`agent-source=user`) → recorded version, no check; any `--agent-version` pin is **ignored** (the tool
   does not own the agent, so a pin cannot change what is baked).
2. **`--agent-version <v>` pin** → `v`, no upgrade check.
3. **No upgrade checker** → recorded version, no check.
4. **No recorded baseline, or one that disagrees with the existing image's label** → take the release the image reports
   through its `agent-version` label (`vm.recoverAgentVersion` via `image.ExistingAgentVersion`) and rewrite the cache,
   then continue to the upgrade check below. The image is the authority; a disagreeing record is stale and never drives
   a build. Only when no image (or no release label) yields a version does an empty record resolve to latest at build
   time.
5. **Recorded (or label-reconciled) baseline** → upgrade check; may prompt to rebuild with a newer release.

`--agent-version` is available on `build`, `run`, and `shell`. `--rebuild` is orthogonal: it only forces a clean rebuild of
whichever version is selected and does **not** suppress the upgrade check. `build` has no upgrade check, but it is not
always a fresh version: for a user-provided agent it, like `run`, ignores the pin and reuses the recorded version
(`UserProvided=true`), resolving a release only if a rebuild later needs one.

The image-ensuring paths are the writers of record for the baseline: `image.EnsureImage` (via `ImageInfo.AgentVersion`)
and `image.Build` (which returns the `ImageInfo` from `EnsureLoaded`) surface the baked release so the caller records it
in `HomeState.agent_version`. See `specs/agent-version-sync.md`.

### Latest-version sources

Each profile's `LatestVersion` (the upgrade-check source) must mirror the source that profile's upstream installer
actually resolves, so the pinned version is always one the installer can fetch:

- `opencode` → GitHub `releases/latest`; `pi` → `pi.dev`; `claude-code` → npm `latest` dist-tag.
- `opencode2` → the opencode update API's `latest` channel (`https://opencode.ai/update/api/latest/cli/npm`), the same
  endpoint `https://opencode.ai/v2/install` queries when unpinned. Do **not** read an npm dist-tag here: the deprecated
  `@opencode-ai/cli` `beta` tag resolves to a version whose platform tarball ships `bin/opencode2`, which the v2
  installer (expecting `bin/opencode`) cannot install, so the runner-image build fails at the installer's `mv` step.
  The `0.0.0-beta-<build>` numeric-comparison path in `newerOpenCode2Than` is retained only to compare against a
  previously baked pre-GA snapshot.

## Callers

- `build` / `image build` → `image.Build` → `Force = -r/--rebuild`.
- `run` / `shell` → `PrepareSandbox` → `Force = opts.Rebuild` (`-r/--rebuild`). An accepted upgrade (`shallUpgrade`) is
  **not** a force; it triggers the build via the changed version identity.
- `volume migrate` / `volume reset` / `volume edit` → `image.EnsureImage` → `Force = false`. Each still exposes
  `--rebuild` as a hidden, deprecated no-op (restored for one cycle, removal targeted 0.8.0 — see
  `specs/deprecation-policy.md`); it never forces a clean rebuild.

## Invariants

- `BuildOptions.Force` is the only input that bypasses the layer cache; a stale-image rebuild never does.
- `--rebuild` on the volume subcommands is a deprecated no-op; the only real cache bypass is `BuildOptions.Force` on
  `build`/`run`/`shell`.
- The internal build helper parameter that maps to `NoCache` is named `noCache`, not `force`, so the cache meaning stays
  explicit at the build call.