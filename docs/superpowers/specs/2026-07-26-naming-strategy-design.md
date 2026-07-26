# Unified artifact naming strategy

## Problem

The launcher creates several kinds of artifacts — Docker images, microsandbox
images, session sandboxes, ephemeral task sandboxes, home volumes, and clone
volumes. Their naming evolved independently, producing three problems:

1. **Inconsistent prefixes.** Sandboxes and images share `opencode-msb` but
   volumes use a mid-string substring (`-opencode-home-`) that doesn't follow
   the same prefix discipline. Filters are a mix of prefix and substring
   matching.

2. **Leaky separation.** Clone volumes inherit the `-opencode-home-` substring
   from their source, so `ListVolumes` reports transient clones as home volumes.
   Ephemeral task sandboxes (`prefill-`, `clone-`) share the `opencode-msb-`
   prefix with real sessions, so they appear in `ListSandboxes` while
   provisioning.

3. **Poor human readability.** The project slug is a bare hash (`p-<8hex>`)
   with no project name. Image references carry no project provenance. The
   image digest is truncated to 12 hex in the tag but kept full-length in the
   volume name, so you can't correlate an image to its volume by eye.

## Goals

- One prefix stem (`opencode-msb`) for every artifact type, with typed infixes
  for clean prefix-based filtering.
- Clean separation between main home volumes and clone volumes, and between
  session sandboxes and ephemeral task sandboxes.
- Project slug embedded in every artifact name, including a human-readable
  folder name.
- Identical image digest token in the msb image reference and the home volume
  name, so image ↔ volume correlation is greppable.
- Docker and msb image references aligned as closely as possible.

## Decisions

### Base62 encoding, 8 characters

All hashes use base62 (`0-9a-zA-Z`) at 8 characters (47.6 bits of entropy).

- **Why base62:** 5.95 bits/char vs hex's 4.0 — 49% more entropy per character,
  no special characters that conflict with the `-` delimiter scheme.
- **Why 8 chars:** Same collision resistance as the current 12-hex (48 bits),
  but 4 characters shorter. Birthday-collision threshold is ~17M builds —
  negligible for a personal tool. At 1,000 builds (realistic upper bound) the
  collision probability is ~0.000003%.
- **Why not base64url:** Uses `-` and `_`, which collide with our delimiters.

A single `hashID(input string) string` function replaces all hex hashing. It
takes an arbitrary string, computes `sha256.Sum256([]byte(input))`, converts
the 32-byte digest to a `big.Int`, encodes it in base62 via repeated
division, and returns the first 8 characters. This works uniformly for both
the project slug hash (`hashID(absPath)`) and the image digest hash
(`hashID(dockerImageID)`).

### No type-marker letters

Hashes are bare — no `p`, `v`, `d` prefix on the 8-char token. The project
hash is parseable as the last 8 chars of the slug (after the final `-`); the
image digest is always the terminal token in both image tag and volume name.
Positional parsing is trivial; no visual marker is needed.

### Project slug

**Format:** `<sanitized-folder-name>-<8b62>`

| Component | Source | Rule |
|-----------|--------|------|
| Folder name | `filepath.Base(git-common-dir)`, or CWD if not a repo | — |
| Sanitized | lowercase, non-alphanumeric → `-`, collapse consecutive `-`, trim leading/trailing `-`, cap at 20 chars | `My App` → `my-app` |
| Hash | SHA-256 of absolute git-common-dir path → 8 base62 chars | `3f9a2b1c` |

**Result:** `opencode-msb-3f9a2b1c`

The folder name provides human readability; the hash disambiguates projects
that happen to share a folder name (e.g. two repos both cloned as `app`).

### Image naming

| Entity | Docker tags | msb reference | Example |
|--------|-------------|---------------|---------|
| Base image | `opencode-msb/runner-base:{latest,<8b62>}` | not loaded into msb | `opencode-msb/runner-base:latest` |
| Project runner image | `opencode-msb/runner-<slug>:{latest,<8b62>}` | `opencode-msb/runner-<slug>:<8b62>` | `opencode-msb/runner-opencode-msb-3f9a2b1c:3f9a2b1c` |

**Docker ↔ msb alignment:** The msb reference is the exact same string as the
Docker digest tag. `msb load --tag opencode-msb/runner-<slug>:<8b62>`. The
`:latest` alias exists only in Docker for human inspection; msb stores only
the digest-tagged form.

**Dockerfile FROM line** references `opencode-msb/runner-base:latest`. The
`ReferencesBase` check is updated to look for this string.

The base image is Docker-only — it is never loaded into the msb image store.
Only project runner images are `msb load`-ed, tagged with their digest.

### Sandbox naming

| Entity | Pattern | Example |
|--------|---------|---------|
| Session sandbox | `opencode-msb-sb-<slug>-<branchSlug>` | `opencode-msb-sb-opencode-msb-3f9a2b1c-main` |
| Task sandbox (prefill) | `opencode-msb-task-prefill-<slug>-<ts>` | `opencode-msb-task-prefill-opencode-msb-3f9a2b1c-1719432000` |
| Task sandbox (clone) | `opencode-msb-task-clone-<slug>-<ts>` | `opencode-msb-task-clone-opencode-msb-3f9a2b1c-1719432000` |

Typed infixes (`-sb-` for sessions, `-task-` for ephemeral) cleanly separate
real sessions from transient provisioning sandboxes in the same
`opencode-msb-` namespace.

Branch slug escaping (`-` → `--`, `/` → `---`) is unchanged — it prevents
ambiguity between the `-` delimiter and branch names containing hyphens or
slashes.

### Volume naming

| Entity | Pattern | Example |
|--------|---------|---------|
| Main home volume | `opencode-msb-home-<slug>-<8b62>` | `opencode-msb-home-opencode-msb-3f9a2b1c-3f9a2b1c` |
| Clone home volume | `opencode-msb-clone-<slug>-<8b62>-<ts>` | `opencode-msb-clone-opencode-msb-3f9a2b1c-3f9a2b1c-1719432000` |

The `<8b62>` in the volume name is the **image digest hash** — the same token
as the msb image tag. So
`opencode-msb/runner-<slug>:3f9a2b1c` ↔ `opencode-msb-home-<slug>-3f9a2b1c`
is greppable correlation. A new image build produces a new digest → new volume
→ fresh prefill. Correct and visible.

The clone volume inherits the source volume's slug and digest, plus a
timestamp. You can trace `opencode-msb-clone-<slug>-3f9a2b1c-<ts>` back to its
source `opencode-msb-home-<slug>-3f9a2b1c`.

### Filter strategy

All filters become clean **prefix matches** — no more substring matching:

| Filter | Prefix | Catches | Excludes |
|--------|--------|---------|----------|
| `ListSandboxes` | `opencode-msb-sb-` | session sandboxes | task sandboxes, foreign |
| `ListVolumes` | `opencode-msb-home-` | main home volumes | clone volumes, foreign |
| `ListImages` | `opencode-msb/runner-` | project runner images | base image (excluded by convention — never loaded into msb; the prefix also matches `runner-base` but it can't appear in the msb store) |
| Clone volume cleanup | `opencode-msb-clone-` | clone volumes | main home volumes |
| Task sandbox cleanup | `opencode-msb-task-` | orphaned task sandboxes | session sandboxes |

### Migration

Old-prefixed artifacts won't match any new prefix filter:

- `opencode-msb-<slug>-<branch>` sandboxes (no `-sb-` infix)
- `<slug>-opencode-home-<digest>` volumes (no `opencode-msb-home-` prefix)
- `opencode-msb/runner:base` and `opencode-msb/runner:latest` images (no
  `runner-base` / `runner-<slug>` namespace)
- `opencode-msb-prefill-<ts>` and `opencode-msb-clone-<ts>` sandboxes (no
  `-task-` infix)

These become invisible to `list`/`prune` but are not deleted. The `doctor`
command optionally warns about orphaned old-prefixed artifacts. No automatic
migration; users manually clean up old artifacts once.

## Full naming table

| Entity | Pattern | Example |
|--------|---------|---------|
| Project slug | `<folder>-<8b62>` | `opencode-msb-3f9a2b1c` |
| Base image (Docker only) | `opencode-msb/runner-base:{latest,<8b62>}` | `opencode-msb/runner-base:latest` |
| Project runner image (Docker + msb) | `opencode-msb/runner-<slug>:{latest,<8b62>}` | `opencode-msb/runner-opencode-msb-3f9a2b1c:3f9a2b1c` |
| Session sandbox | `opencode-msb-sb-<slug>-<branchSlug>` | `opencode-msb-sb-opencode-msb-3f9a2b1c-main` |
| Task sandbox (prefill) | `opencode-msb-task-prefill-<slug>-<ts>` | `opencode-msb-task-prefill-opencode-msb-3f9a2b1c-1719432000` |
| Task sandbox (clone) | `opencode-msb-task-clone-<slug>-<ts>` | `opencode-msb-task-clone-opencode-msb-3f9a2b1c-1719432000` |
| Main home volume | `opencode-msb-home-<slug>-<8b62>` | `opencode-msb-home-opencode-msb-3f9a2b1c-3f9a2b1c` |
| Clone home volume | `opencode-msb-clone-<slug>-<8b62>-<ts>` | `opencode-msb-clone-opencode-msb-3f9a2b1c-3f9a2b1c-1719432000` |

## Affected files

| File | Changes |
|------|---------|
| `internal/git/git.go` | `ProjectSlug` returns `<folder>-<8b62>`; new `hashID` base62 function; folder name sanitization |
| `internal/sandbox/image.go` | `BaseTag` → `opencode-msb/runner-base`; `ImageTag` returns `opencode-msb/runner-<slug>:<8b62>`; `runnerTag` becomes per-project `opencode-msb/runner-<slug>:latest`; `ReferencesBase` checks for `runner-base` |
| `internal/sandbox/runner.go` | `sandboxName` produces `opencode-msb-sb-<slug>-<branchSlug>` |
| `internal/sandbox/volumes.go` | `HomeVolumeName` produces `opencode-msb-home-<slug>-<8b62>`; prefill/clone sandbox names use `-task-` infix |
| `internal/sandbox/query.go` | All filters updated to new prefixes: `opencode-msb-sb-`, `opencode-msb-home-`, `opencode-msb/runner-` |
| `internal/sandbox/image_test.go` | Test expectations updated for new image tags |
| `internal/sandbox/runner_test.go` | Test expectations updated for new sandbox names |
| `internal/sandbox/volumes_test.go` | Test expectations updated for new volume names |
| `internal/sandbox/query_test.go` | Test expectations updated for new filter prefixes |
| `internal/sandbox/doctor.go` | Optional: warn about orphaned old-prefixed artifacts |

## Constraints

- Sandbox names are capped at 128 chars by msb; the `sandboxName` function
  truncates if needed (unchanged from current behavior).
- Base62 alphabet must be ordered consistently (`0-9a-zA-Z`) to ensure
  deterministic encoding.
- The `hashID` function takes a string, hashes it with SHA-256, and encodes
  the full 256-bit result in base62 (43 chars), taking the first 8. It must
  use `math/big.Int` to avoid overflow.
