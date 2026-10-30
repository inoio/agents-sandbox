---
description: Contract for per-PR changelog fragments: `changelog.d/<slug>.<type>.md` (slug = issue or PR number) replaces direct `[Unreleased]` edits so PRs cannot conflict on `CHANGELOG.md`, plus the type vocabulary, the release-time assembly order, the required always-run `changelog` shape/presence check, and the transition from the existing `[Unreleased]` entries.
read_if: Changing how changelog entries are authored or assembled (`changelog.d/`, `CONTRIBUTING.md`'s changelog rules, the release curation step, the `changelog` CI check), adding a `CHANGELOG.md` section, or diagnosing a missing/duplicated changelog entry or a conflict that was expected to vanish.
---

# Changelog fragments

## Why

Every behavior change adds a bullet under `CHANGELOG.md`'s `## [Unreleased]`, so any two PRs that touch
no other file share one anchor there and conflict when the merge queue builds them together.
`CHANGELOG.md` is the only file that conflicts in practice.

`CHANGELOG.md merge=union` cannot fix this on GitHub: GitHub's server-side merge and its merge-queue
build ignore user `.gitattributes` merge drivers, and a queued PR whose group build conflicts is
removed from the queue. The evidence is in
[`guides/github-merge-driver-support.md`](../guides/github-merge-driver-support.md). The fix is to
stop editing one shared file: each change carries its own fragment file, which cannot collide by
construction.

## The fragment contract

A changelog fragment is one file in `changelog.d/` at the repository root:

- **Name**: `<slug>.<type>.md`.
  - `<type>` is one of `added`, `changed`, `deprecated`, `docs`, `fixed` (lowercase) — exactly the
    `CHANGELOG.md` section it belongs to.
  - `<slug>` identifies the change: the **issue number** when the change has one, otherwise its
    **PR number**. Both are unique by construction, which is what removes the conflict. A
    descriptive slug is not used, because two branches can pick the same words and collide on the
    same path — reintroducing the very conflict this removes. GitHub allocates issues and PRs from
    one sequence, so the PR number is known as soon as the PR is opened, which is when the presence
    check runs; deriving it earlier from the API is possible but racy and not required.
  - Example: `changelog.d/432.added.md`, `changelog.d/431.fixed.md`.
- **Content**: exactly the text of one `CHANGELOG.md` bullet, without the leading `- `, with
  continuation lines indented two spaces like the surrounding changelog. One fragment becomes one
  bullet; a change spanning two sections uses two fragments. A human-readable issue or PR reference
  (`(#432)`) stays in the bullet text where it already lives today, so the filename carries only the
  bare number.
- The directory may carry a short `README.md` describing the workflow (it renders in GitHub's
  directory view and is read by agents). The check ignores the towncrier-standard set —
  `README.md`, `README.rst`, `.gitignore`, `.gitkeep`, `.keep` — so the directory can exist while
  empty and be documented in place. Apart from those, it holds only fragment files.

## Assembly contract

Fragments are assembled into the changelog by the release process, following the rules below; there
is no separate tool and no continuous writer to `main`. Between releases, `changelog.d/` is the live
record.

- **Order**: sections in the fixed order `Added`, `Changed`, `Deprecated`, `Docs`, `Fixed`; within a
  section, fragments ordered by filename ascending, so assembly is deterministic.
- **Emission**: each fragment's first line is prefixed with `- ` and its remaining lines follow
  verbatim; the bullet is appended to its section's list.
- **Target**: at release the assembled content forms the new `## [X.Y.Z] - <date>` section, together
  with any legacy `[Unreleased]` entries curated per the release process; the fragment files are
  deleted in the same change.
- The result keeps the Keep-a-Changelog shape and the section headings the release process already
  uses. The vocabulary above is closed: a new section is a deliberate change to this contract.

## Enforcement contract

Two rules, enforced by a dedicated `changelog` check that **always runs** and is a required status
check. It must never be conditionally skipped: a skipped required check reports success without
running, which is why the required set is always-run (see `specs/ci-job-gating.md`). It decides the
presence rule internally from the PR's diff rather than gating its own execution.

- **Shape** (every run): every file in `changelog.d/` matches `<slug>.<type>.md` with a known type,
  except the ignored names above.
- **Presence** (pull requests): a PR whose changeset touches code must add at least one fragment.
  "Code" is the complement of the `changes`-job filter in `.github/workflows/ci.yml`: `docs/`,
  `.operator-shared/`, `Formula/`, `.github/ISSUE_TEMPLATE/`, any `*.md`, and `.gitignore` are
  exempt, so a docs-only PR needs no fragment. The check decides this internally from the PR diff;
  it does not gate its own execution.

## Transition

Existing `## [Unreleased]` entries stay where they are and are consumed by the next release; they
are frozen — from adoption on, no change edits `[Unreleased]` directly. After that first release the
section is empty and `changelog.d/` is the only live record for the next cycle. Migrating the current
entries into fragments is deliberately not done: it is a churn PR on the one file that still
conflicts, and the entries age out in one cycle anyway.

## Non-goals

- No change to the Keep-a-Changelog format or the section vocabulary beyond `changelog.d/`.
- No generation of entries from commit messages, PR titles, or labels; the wording stays authored
  prose.
- No bot or CI commit to `main`: assembly stays a release-time, maintainer-run step.
