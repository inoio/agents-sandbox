---
description: Durable deprecation policy for agents-sandbox (0.x, high UX bar): how a flag/config key/env var/command is deprecated, how long it survives, how it is surfaced, and the current deprecation register with replacement and removal targets.
read_if: Deprecating or removing a CLI flag, config key, env var, or command; deciding whether a change needs a shim or a clean break; or enforcing the deprecation register in tests.
---

# Deprecation policy

Date: 2026-10-16

The project is pre-1.0 but holds a high UX bar: a rename is never a silent break.
This policy is the contract for how existing usage is retired, and the register
of every deprecation currently in force.

## Rules

- **Works, warns, hides.** A deprecated flag / config key / env var / command
  keeps working with **lower precedence** than its replacement and prints a
  **one-time warning** to stderr. It is hidden from `--help` but still honored.
- **Replacement wins.** The new form always takes precedence; the old form
  resolves only when the new one is unset. Both set together is not an error
  unless a specific pair is ambiguous (then follow the existing per-flag rule).
- **Two surfaces.** Every deprecation is listed in `CHANGELOG.md` under
  `Deprecated` (with its replacement) **and** in a docs "Deprecations" section
  (see `docs/`), so it is discoverable without reading the changelog.
- **Minimum lifetime.** A deprecation survives **at least one minor release**
  after the release that introduces it. Removal is announced (in `Changed` or
  `Removed`) in the release notes that removes it.
- **Removal condition stated up front.** Each entry in the register names the
  release it becomes eligible for removal in; nothing is removed without a
  stated condition.
- **No undeprecated removals.** A user-facing flag/key/env/command is never
  removed outright; it is deprecated first, or the change is rejected.

## Clean break vs shim

- **Shim** (deprecate + alias) is the default for renames of a flag, config key,
  env var, or command.
- **Clean break** is acceptable for a **default-behaviour change** where no
  alias is meaningful. Such a change is not a "removal" under this policy, but it
  must be called out as breaking in the CHANGELOG and release notes.
  - **0.6.0 accepted break:** `prune` (and `image`/`volume`/`sandbox prune`) now
    confirm by default; non-interactive without `--yes` fails fast. Documented,
    not shimmed (`--yes` is the escape).
- Removing a flag that shipped in a prior release **is** a removal and follows
  the minimum-lifetime rule. If a removal slipped out without a deprecation
  cycle, **restore it as a hidden deprecated alias/no-op for one cycle** rather
  than shipping the removal.
  - **0.6.0 correction (H2 in #203):** `--rebuild` on `volume migrate`/`reset`/
    `edit` (present in 0.5.0, removed in this range) **is to be restored as a
    hidden deprecated no-op that warns**, removable at its 0.8.0 eligibility
    (issue #206). Point users at `agents-sandbox build`.

## Register (current)

| Deprecated form | Replacement | Introduced | Eligible removal |
|---|---|---|---|
| `--dind` / `dind` config key / `AGENTS_SANDBOX_DIND` | `--docker` / `docker` / `AGENTS_SANDBOX_DOCKER` | 0.6.0 | 0.8.0 |
| `runner-base-dind` image reference | `runner-base-docker` | 0.6.0 | 0.8.0 |
| `# agents-sandbox:dind` Dockerfile marker | `# agents-sandbox:docker` | 0.6.0 | 0.8.0 |
| `OPENCODE_SANDBOX_*` env prefix | `AGENTS_SANDBOX_*` | 0.6.0 | 0.8.0 |
| `sandbox list --names` | global `-q`/`--quiet` | 0.6.0 | 0.8.0 |
| `prune --age` / `-a` | `--older-than` | 0.6.0 | 0.8.0 |
| `--opencode-version` | `--agent-version` | 0.2.0 | 0.7.0 |
| `volume migrate`/`reset`/`edit` `--rebuild` (restore pending, #206) | run `agents-sandbox build` first | 0.5.0 (removed) / 0.6.0 | 0.8.0 |

Eligibility is a floor, not a promise; a removal only lands in a minor release,
never a patch.

## Enforcement

- The `Deprecated` changelog entries and the register above are kept in sync in
  the same PR that introduces or removes a deprecation.
- Every deprecation has a regression test asserting it still resolves with lower
  precedence and emits one warning. `cmd/agents-sandbox/cli_deprecations_test.go`
  (#202) covers the flag/env/config aliases and asserts the warning fires exactly
  once; the image-reference aliases are silent by design and are covered by the
  image-package tests. A deprecation without a test is incomplete.
- The docs "Deprecations" page (issue #205) is updated from the register by hand
  (small, curated) and checked by `make check`'s docs linkcheck.

## Non-goals

- No compatibility mechanism beyond aliases (no feature flags, no runtime
  version gates) for ordinary renames.
- No silent acceptance of the deprecated form once it is removed.
