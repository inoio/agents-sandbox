---
description: Contract for deciding which agent version is baked into the runner image, when it is (re)built, and when the Docker layer cache is bypassed.
read_if: Changing image build/caching, BuildOptions, `--rebuild`, `--agent-version`, agent upgrade, or the volume subcommands' image handling.
---

# Runner image build and cache contract

How the rendered Dockerfile is arranged into stages (so layer caching works as assumed here) is owned by
`specs/image-composition.md`.

## Build decision vs. cache bypass

`EnsureImageWithClient` makes two independent decisions from one input, `BuildOptions.Force`:

- **Build at all:** `needsBuild = Force || !imageHasDockerfileID(...)`. The `dockerfile-id` label is the content identity
  (rendered Dockerfile + resolved agent version + the profile.d merge script, which travels via the build context rather
  than the Dockerfile text); a mismatch means a stale image and triggers a build.
- **Bypass cache:** `Force` is passed through to `NoCache` on the Docker build.

So `Force` means **clean rebuild**: rebuild even when the identity matches, with `NoCache=true`. It is set only by an
explicit user `--rebuild` on `build`, `run`, or `shell`.

A build triggered by a stale identity (changed project Dockerfile, agent version, or base) reuses Docker's layer cache. This is
safe because each change is visible to Docker's cache key: the agent version is a build `ARG` referenced by the install `RUN`,
and the `dockerfile-id` label lives after the agent install. An accepted agent upgrade is a version change, not a force: it
rebuilds the install layer while keeping earlier layers cached.

## Version selection

`resolveBuildVersion` (run/shell) picks the version to bake with this precedence:

1. **User-provided agent** (`agent-source=user`) → recorded version, no check; any `--agent-version` pin is **ignored** (the tool
   does not own the agent, so a pin cannot change what is baked).
2. **`--agent-version <v>` pin** → `v`, no upgrade check.
3. **No upgrade checker** → recorded version, no check.
4. **No recorded baseline** → empty (resolved to latest at build time).
5. Otherwise → upgrade check; may prompt to rebuild with a newer release.

`--agent-version` is available on `build`, `run`, and `shell`. `--rebuild` is orthogonal: it only forces a clean rebuild of
whichever version is selected and does **not** suppress the upgrade check. `build` has no upgrade check because it always resolves a
fresh version (pinned or latest) rather than reusing the baked one.

## Callers

- `build` / `image build` → `image.Build` → `Force = -r/--rebuild`.
- `run` / `shell` → `PrepareSandbox` → `Force = opts.Rebuild` (`-r/--rebuild`). An accepted upgrade (`shallUpgrade`) is
  **not** a force; it triggers the build via the changed version identity.
- `volume migrate` / `volume reset` / `volume edit` → `image.EnsureImage` → `Force = false`. These subcommands do **not**
  expose `--rebuild`; they ensure a current image but never force a clean rebuild.

## Invariants

- `BuildOptions.Force` is the only input that bypasses the layer cache; a stale-image rebuild never does.
- No volume subcommand exposes `--rebuild`.
- The internal build helper parameter that maps to `NoCache` is named `noCache`, not `force`, so the cache meaning stays
  explicit at the build call.