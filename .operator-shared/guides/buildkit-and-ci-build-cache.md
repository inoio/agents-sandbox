---
description: Investigation handoff for making the CI integration tier fast enough to be a required PR check - whether to move the runner-image build to BuildKit (the cache-export requirement), the podman and classic-builder compatibility costs, the GitHub Actions cache scoping that forces a single main writer, and the three candidate approaches with their open decision.
read_if: Deciding whether to switch the runner-image build off the classic builder to BuildKit; adding a cross-run build cache for `make test-integration` in CI; reasoning about podman or other Docker-API engines and BuildKit compatibility; or picking up the "run integration on every PR" work.
---

# BuildKit migration and the CI integration build cache

**Status: investigation, no decision.** Nothing here is implemented. The motivating goal changed mid-investigation
from "make the opt-in integration run faster" to "run integration on every code-affecting PR", and the second goal is
what forces the builder question. See [The decision](#the-decision).

## The goal

Run `make test-integration` on every PR whose diff touches code (`changes.outputs.code == 'true'`, the existing
`changes` gate), and make it a required check - **if** a cached run lands under ~3-5 minutes. Main keeps building
cold and refreshes one shared cache; PRs restore it. A single global cache is wanted, not per-PR.

## Why the build cache is the crux

`make test-integration` runs real engine builds (`internal/sandbox/image`, the largest cost). On an ephemeral
GitHub-hosted runner the engine's layer cache starts empty, so every run pays the cold build. Two facts decide how a
cache can be shared:

- **The suite builds with the classic builder.** `image.buildImage` calls the moby `client.ImageBuild` with no
  `ImageBuildOptions.Version` set, so `/build` runs `version=1` (classic). GitHub's docker cache
  (`docker/build-push-action` with `cache-from`/`cache-to: type=gha`) requires BuildKit. The moby client's
  `ImageBuildOptions` has `CacheFrom` but **no `CacheTo`** - cache *export* is BuildKit-only, so a BuildKit-less run
  can only read a hand-supplied cache, never publish one.
- **GitHub cache scoping forces a single writer.** A `pull_request` run can restore caches created on the default
  branch (and base branches of forks), but a cache the PR run *creates* is scoped to `refs/pull/.../merge` and is
  invisible to main and to sibling PRs. So the shared cache has exactly one writer: **main**, triggered by `push`.
  PRs must be restore-only (`actions/cache/restore`) to avoid writing throwaway merge-ref entries. Cache limit is
  10 GB/repo by default (LRU-evicted beyond); docker images may brush this. (Source: GitHub "Dependency caching
  reference", "Restrictions for accessing a cache".)

## The builder question

Moving production to BuildKit would remove the export limitation entirely (`cache-to: type=gha,mode=max` or a
registry cache), so the investigation turned into "should the launcher build with BuildKit?".

### What is already true in the code

- **The render is builder-neutral.** `image.dockerfile.go` emits only multi-stage `COPY --from=<stage>` plus `RUN`;
  no heredocs, no `RUN --mount`. Both builders accept it.
- **The parser already mirrors BuildKit.** `internal/sandbox/image/dockerfile_lines.go` implements BuildKit's
  heredoc/continuation/directive rules - but only to *parse the user's project Dockerfile* (to detect base image
  references and stage headers, e.g. whether dind/docker stages are needed). It is not a build-time requirement.
- **The test output seam handles both.** `stageStepCached` already recognises classic (`---> Using cache`) and
  BuildKit (`#N CACHED`) output. The `stageInstallMarkers` comment in `internal/sandbox/image/render_integration_test.go` notes the classic vs BuildKit stream difference.
- Consequence: a user Dockerfile using BuildKit-only syntax (heredoc `RUN`, `RUN --mount`) is parsed correctly today
  but **fails to build** on the classic builder - an existing correctness gap that a BuildKit switch would close.

### Compatibility cost

- **Docker Engine (Linux, Colima, Rancher Desktop, Docker Desktop): fine.** BuildKit is the CLI default since Engine
  23. The classic builder is deprecated; Docker's docs state "the classic builder is deprecated so both the automatic
  fallback and opting-out of using BuildKit will no longer be possible in a future release" - so the current implicit
  classic build is on borrowed time regardless of this work. (Source: Docker "Deprecated Docker Engine features".)
- **Podman: the casualty.** Podman's Docker-compat `/build` does not implement BuildKit; it ignores the `version=2`
  intent and builds with buildah, and its API lacks the BuildKit session/`h2c` upgrade. Podman issue #17836 has been
  open since 2023 and remains unresolved (a 2026 comment still calls it "a real gap"). The `integration-podman` CI job
  and the docs' **Tested** engine claim rest entirely on the classic `/build` path - see `guides/podman-engine-testing.md`.
  A BuildKit switch would make that claim misleading (a podman run would exercise a different builder than production,
  or fail), not merely red.
- **Go-side mechanism is not a flag flip.** The first-class BuildKit client is `buildx`/`buildkit` over gRPC, a
  different client from the `moby/moby/client` HTTP client used throughout. `ImageBuildOptions.Version =
  BuilderBuildKit` is the legacy BuildKit-over-`/build` gateway and has known gaps (e.g. moby #48112: registry auth
  is not passed through to BuildKit). "Switch to BuildKit" likely means adopting the buildkit client.

## Approaches

- **A - `actions/cache` of a `docker save` tarball.** Main's cold run saves produced images; PRs restore + `docker
  load`, then run warm. Native, engine-agnostic, no auth, fork-friendly. Risks: (i) unverified whether `docker load`
  re-seeds the **classic** builder's cache (its cache is a chain of image records, not the layer store); (ii) 10 GB
  cap; (iii) `removeTestImage` (force-removes with `PruneChildren: false`) untags the very images a save would want,
  so a keep-images knob or a canonical prebuild is needed.
- **B - ghcr.io as the cache.** Main pushes produced images to ghcr under stable names; PRs pull. Registry layer
  dedup means only missing layers transfer; no 10 GB cap; public packages pull for forks; `packages: write` on main,
  pull on PRs. Failure and cleanup are ours. Same "which images / keep them" plumbing as A; no builder change; no
  podman impact.
- **C - BuildKit in production, then `type=gha` cache.** Collapses the cache problem to a native mechanism, no
  save/load, no classic-builder-reuse uncertainty, and improves user-Dockerfile correctness. Costs the podman
  **Tested** claim and the client-library change above.

## The decision

The choice is really about **sequencing**:

- Do **A** or **B** to get the cached PR check now and defer the podman reckoning, or
- Do **C** first (with B as the interim if the podman and client work is too large for one change).

The open question the investigation ended on, unresolved: **is switching production to BuildKit acceptable at the
cost of the podman "Tested" claim, or must that claim survive?** That answer picks the path. A secondary cost for C
that also needs a call: moving the suite under test from the classic builder to BuildKit changes what the integration
suite validates - deliberate, not incidental.

## Unverified - needs a host spike

Cannot be checked in the dev VM (no docker). Both must be settled before A/C is planned:

1. **Does `docker load` of a saved image re-seed the classic builder's cache?** The go/no-go for approach A. Spike:
   `build -> docker save -> prune -> docker load -> rebuild`, watch for classic "Using cache".
2. **Is `ImageBuildOptions.Version = BuilderBuildKit` usable enough to avoid the full buildkit client?** Or is
   moby #48112 et al. a blocker.

## Not yet done

- No spec change. `specs/ci/ci-pipeline.md` still documents integration as opt-in/not-required; that is correct until
  the decision lands. If the goal is realized, that spec's job-gating section needs the new cache and required-check
  contract.
- The "required on PRs but skipped on the merge queue" shape (a `merge_group` job skipped by its `if` reports
  Success) is noted but not designed.
