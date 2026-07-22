# Naming Scheme for msb Sandboxes, Volumes, and Images

Date: 2026-07-22
Status: Proposed, not yet implemented

## Context

Current names are opaque (`p-41417eb5`) and the Go rewrite has a bug: it uses
Docker's `inspect.ID` (non-reproducible) instead of the Dockerfile content hash
that the Python implementation uses.

## Current State

### Python (currently running)

| Entity       | Pattern                                              | Example                                    |
|--------------|------------------------------------------------------|--------------------------------------------|
| Project slug | `p-{sha256(gitCommonDir)[:8]}`                      | `p-41417eb5`                               |
| Image hash   | `sha256(dockerfileBytes)[:12]`                       | `8f0f5642b623`                             |
| Image tag    | `inoio-sandbox/runner:{hash}`                        | `inoio-sandbox/runner:8f0f5642b623`        |
| Sandbox name | `inoio-sandbox-{project}-{branch}`                  | `inoio-sandbox-p-41417eb5-feat-go-rewrite` |
| Home volume  | `{project}-opencode-home-{hash}`                    | `p-41417eb5-opencode-home-8f0f5642b623`   |

### Go (rewrite, has bugs)

- Uses Docker's `inspect.ID` (full `sha256:...`, 64+ chars) instead of
  Dockerfile content hash -- **not reproducible across machines**, produces
  very long volume names.
- Uses `opencode-msb` prefix instead of `inoio-sandbox`.

## Hash Analysis

| Hash                          | Source                        | Immutable? | Reproducible? |
|-------------------------------|-------------------------------|------------|---------------|
| Project slug hash (`41417eb5`) | `sha256(absPathOfGitCommonDir)` | Yes        | Yes           |
| Python image hash (`8f0f5642b623`) | `sha256(dockerfileBytes)` | Yes (content-addressed) | Yes (same Dockerfile -> same hash) |
| Go image digest (`sha256:...`) | Docker `inspect.ID`           | Yes (content-addressed) | **No** (varies with layer caching, build context) |

**Verdict:** The Python approach (Dockerfile content hash) is the correct one.
The Go code must adopt it -- using Docker's image ID was a mistake.

## Proposed Naming Scheme

Strategy: `{repoName}-{pathHash}` project slug, shared prefix for correlation,
shortened infixes.

| Entity       | Proposed Pattern                        | Example                                      |
|--------------|----------------------------------------|----------------------------------------------|
| Project slug | `{repoName}-{pathHash[:8]}`             | `saife-41417eb5`                             |
| Image hash   | `sha256(dockerfileBytes)[:12]` (unchanged) | `8f0f5642b623`                           |
| Image tag    | `msb-runner:{imageHash}`               | `msb-runner:8f0f5642b623`                    |
| Sandbox name | `msb-{project}-{branch}`               | `msb-saife-41417eb5-feat-go-rewrite`         |
| Home volume  | `{project}-home-{imageHash}`            | `saife-41417eb5-home-8f0f5642b623`           |

## Correlation Story

- `msb list` shows: `msb-saife-41417eb5-feat-go-rewrite`
- `msb volume list` shows: `saife-41417eb5-home-8f0f5642b623`
- Both share `saife-41417eb5` -- instantly correlatable by prefix.
- The `msb-` prefix on sandboxes distinguishes them from volumes in mixed
  listings.
- Volume's `-home-{imageHash}` suffix shows which image version it was built
  for.
- Sandbox has the branch in its name (ephemeral, per-session); volume doesn't
  (persistent, per-image).

## Changes from Current

1. `p-41417eb5` -> `saife-41417eb5` (human-readable repo name + hash)
2. `opencode-home` -> `home` (shorter, the tool is already opencode-specific)
3. `inoio-sandbox-` prefix -> `msb-` (shorter, consistent)
4. Go code: Docker image ID -> Dockerfile content hash (reproducible + consistent
   with Python)
5. Image tag namespace: `inoio-sandbox/runner` -> `msb-runner` (shorter,
   consistent)

## Files to Change (Go)

- `internal/opencodemsb/worktree.go` -- `ProjectSlug()`: derive repo name from
  git, append path hash.
- `internal/opencodemsb/runner.go` -- `sandboxName()`: use `msb-` prefix.
- `internal/opencodemsb/volumes.go` -- `HomeVolumeName()`: use `-home-` infix.
- `internal/opencodemsb/image.go` -- `ImageTag()`: use `msb-runner:` namespace.
- `internal/opencodemsb/image_sdk.go` -- `EnsureImage()`: compute
  `sha256(dockerfileBytes)[:12]` instead of using `inspect.ID`.
- Corresponding tests in `*_test.go`.

## Files to Change (Python, if keeping in sync)

- `src/inoio_sandbox/worktree.py` -- `project_slug()`
- `src/inoio_sandbox/cli.py` -- sandbox name construction (line 103)
- `src/inoio_sandbox/volumes.py` -- `home_volume_name()`
- `src/inoio_sandbox/image.py` -- `BASE_TAG`, `image_tag()`
- Corresponding tests in `tests/unit/`.
