---
description: Design for #490 (part of #488) to set the runner image's agent tool-tree ownership at copy time (`COPY --chown=dev:dev`) instead of a follow-up full-tree `chown` layer, which records a second copy of the tree and doubles the Hermes runner image; requires guaranteeing the `dev` group for every base (unconditional `groupadd -f` in `devUserBlock`) so the name resolves on the production classic builder, and retires `agentOwnershipBlock`.
read_if: Changing the runner image's tool-tree ownership (`agentOwnershipBlock`, `ImageSpec.OwnedToolPaths`, `devUserBlock`'s dev user/group, the `dev` group contract, `COPY --chown` in `runnerStage`); or diagnosing a doubled Hermes runner image, a `can't find gid for group dev` build failure, or a volume prefill/migrate `chown -R dev:dev` failure on a custom base.
---

# Tool-tree ownership in the runner image

Tracking: [inoio/agents-sandbox#490](https://github.com/inoio/agents-sandbox/issues/490), part of
[#488](https://github.com/inoio/agents-sandbox/issues/488). On landing this supersedes the "Tool-tree ownership
handover" paragraph of [image-composition.md](image-composition.md).

## Contract

The runner image sets an agent tool tree's owner at **copy time**, not with a follow-up `chown`:

- `runnerStage` copies the agent tool tree with
  `COPY --chown=dev:dev --from=agents-sandbox-agent /opt/agents-sandbox /opt/agents-sandbox`.
- There is no separate `chown -R` layer; `agentOwnershipBlock` is deleted.
- `ImageSpec.OwnedToolPaths` stays as the per-agent signal for whether the copy carries `--chown`. An agent that
  names no path gets the plain root-owned copy.

`--chown=dev:dev` binds the **name**, resolved at copy time against the destination stage's `/etc/group`. The
`USER_UID`/`USER_GID` build args are deliberately not usable here: the agent stage can only see the args, and the
group the runner actually ends up with can differ from `USER_GID`.

## The `dev` group is guaranteed

`devUserBlock` ensures the `dev` group for every base, managed and custom, by running
`groupadd -f -g "$USER_GID" dev` unconditionally and creating `dev` with `-g dev` only when the user is absent.
`groupadd -f` is a no-op when the group already exists and otherwise creates it at `USER_GID`, or the next free
GID when that GID is taken (the macOS `dialout/20` case).

This adds no custom-base requirement. `shadow-utils` is already required (`docs/runner-image.md`), and the runtime
home-volume scripts — `PrefillVolume`, `CopyInto` in `internal/sandbox/volume` — already run `chown -R dev:dev`,
which needs the same group. A base that pre-creates a `dev` user without a `dev` group therefore already fails
first-run prefill/migrate today; the build merely tolerates it. Guaranteeing the group makes the build and the
runtime agree and moves any failure to build time.

## Why ownership is set at copy time

A `chown` that rewrites ownership on every file records a full copy of the tree in its layer diff. Copying the
tree as root and then chowning it therefore carries the tree twice: the `COPY` plus the `chown`. For Hermes that
is two ~2.25 GB layers in a 4.77 GB image, and it doubles the `msb` import and the build walk. Applying the owner
during the `COPY` records it in the single copy layer.

Duplication is a property of the layer diff, not the storage driver: `overlay2`, `fuse-overlayfs`, `vfs`,
`docker save`, `docker push`, and the `msb` import all carry both blobs. Only `vfs` additionally balloons the local
build cache.

## Invariants

- The agent tool tree appears **once** in the runner image. No step rewrites ownership on the tree after the copy.
- The rendered Dockerfile and `computeDockerfileID` stay host-independent: `dev:dev` is a name, never the host's
  numeric uid/gid.
- The build and the runtime volume scripts both address the tree's group as `dev`, so they cannot disagree about
  which group the tree belongs to.

## Non-obvious findings (Docker 29.7.2, classic builder)

- A **no-op `chown` commits a 0-byte layer**, so an ownership step that finds nothing to change costs no size.
- The production build path is the **classic builder**: `client.ImageBuild` sets no `version` query param, and
  `POST /build` reports `Step 1/2`. Classic **fails** a `--chown=<name>` whose group is absent, where BuildKit
  silently falls back to the user's primary group. The unconditional `dev` group is what makes `dev:dev` safe on
  the classic builder.
- A numeric agent-stage chown does not work on macOS: the host GID 20 collides with the base's `dialout`, so
  `groupadd -f -g 20 dev` leaves `dev` on the next free GID (1000), the agent chowns to `1000:20`, and the correct
  owner `1000:1000` is only reached by a runner-side chown — restoring the second full layer.

## Scope

- Applies to every agent with `OwnedToolPaths` (today only `hermes`), not Hermes-specific.
- `--chown` covers the whole copied tree, not the `OwnedToolPaths` subset. For Hermes that is effectively the whole
  tree; `bin/hermes` also becomes `dev`-owned. This is the intended scope broadening.
- Custom-base images shrink too: there is no managed/custom branch, because the `dev` group guarantee makes
  `dev:dev` resolvable for both.
- Out of scope: the other #488 reduction options — pruning build-only trees (`hermes/.git`, `tests`, `website`,
  `apps`, `optional-skills`, `cache/uv`), removing `node`/`node_modules`, and dropping `ffmpeg`.
