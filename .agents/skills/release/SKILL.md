---
name: release
description: "Cut an agents-sandbox release."
disable-model-invocation: true
---

# Release

Cut a release of `agents-sandbox`. `release` prepares the release end to end, then stops at a
sign-off gate; nothing reaches a remote until the maintainer approves. One release per invocation,
and the maintainer decides the version and who pushes the tag.

## Reference docs

- [RELEASE-NOTES.md](RELEASE-NOTES.md): the release-note structure and how to pull contributors,
  including first-time ones.
- [NON-GO-DEPS.md](NON-GO-DEPS.md): the pinned non-Go tool versions (dependabot does not manage
  them) and the node `<25` rule.

## The one hard rule

Steps 1-4 run locally and reach no remote. Step 5 is a gate: present the release and wait for an explicit
go. Land the changelog (6), push the tag (7), and set the notes (8) only after the maintainer
approves, and only for the version they chose. On abort, stop.

## Flow

### 1. Curate the changelog

Assemble `changelog.d/` into the changelog, then curate. Read every fragment in `changelog.d/` (the
ignored `README.md` aside) together with the legacy `[Unreleased]` section of `CHANGELOG.md`, and turn
them into the entries a reader wants. Order the fragments by section (`Added`, `Changed`, `Deprecated`,
`Docs`, `Fixed`) and, within a section, by filename ascending, per
[`specs/changelog-fragments.md`](../../../.operator-shared/specs/changelog-fragments.md).

- Use one heading per section in the order `Added`, `Changed`, `Deprecated`, `Docs`, `Fixed`. Merge duplicate headings
  if the section accumulated them, and put documentation-only entries under `Docs`. Do not invent other headings.
- Drop negligible entries — internal churn a user of the tool would never notice. Keep the meaningful `Internal:` lines:
  state shape and migrations, reliability or error-surfacing changes, and rebuild/caching behaviour. Drop dev-tooling,
  CI, test-infrastructure, and pure-refactor churn.
- Rewrite each keeper as a human-readable line: lead with the behaviour change, not the implementation. Trim mechanism
  detail but keep every user-visible fact — flags, paths, commands, numbers, and issue links.
- Group related entries under the heading that fits; one user-visible change reads as one line. Sort by user impact per
  section, and lead `Added` and `Changed` with the release's headline stories.
- Write for a user of the tool, in the voice of [`STYLEGUIDE.md`](../../../STYLEGUIDE.md): "sandbox" and "agent", not
  "VM" or "harness", and no filler words.

Completion: every `[Unreleased]` entry is kept, rewritten, or dropped, and the section reads clean
top to bottom.

### 2. Verify

Run `make check` and confirm it passes.

Confirm the last `fuzz` workflow run on `main` is green (`gh run list --workflow=fuzz.yml --branch main --limit 1`), or
dispatch one (`gh workflow run fuzz.yml -f fuzztime=15m`) and wait for it. It runs nightly and on merges that touch the
fuzzed parsers, needs no VM or docker, and nothing is gated on it: a red run means a crasher to fix and commit as a
corpus seed before tagging.

`make test-integration` builds a real runner image and needs a normal host docker daemon;
microsandbox's egress-inspection CA breaks in-image downloads, so it does not run inside the
agents-sandbox VM. Mark it a host-only step for the maintainer; the tag-triggered CI runs it before
the release job.

Run `make mutation` and triage what it reports. It is on demand and never a gate: by default it
mutates the whole module but only the lines changed since the last release tag, so it covers exactly
this release. Every LIVED mutant is closed one of two ways — strengthen the test so the mutant now
fails, or record the reason in the source next to the mutant with `// gomutants:disable <TYPE>
reason="..."`. Commit the strengthened tests locally; the step-6 PR carries them and the tag lands on
the merged result. This is the one write in steps 1-4 — nothing reaches a remote until step 5.

Completion: `make check` green, mutation survivors triaged, and the host-only integration step noted
for the maintainer.

### 3. Choose a version

Pick the next version by SemVer from the curated content: a breaking change bumps the major, a new
feature the minor, fixes the patch. A release carrying big user-facing features may warrant a
pre-release (`vX.Y.Z-alpha.N`). State the version and the reasoning; the maintainer makes the final
call.

Completion: one proposed version with its rationale.

### 4. Author the release notes

Draft the GitHub release body from the same curated content, re-pitched for the public, following
[RELEASE-NOTES.md](RELEASE-NOTES.md). Include the Contributors section, highlighting first-time
contributors. Keep the draft in a temp file.

Completion: a complete notes body in a temp file, ready to present.

### 5. Gate

Stop and present, in one message: the proposed version, the curated `[Unreleased]` content, the
drafted release notes, and the tag-push options (the maintainer pushes from the host, or the agent
creates the tag through `gh`). Wait for an explicit decision.

### 6. Land the changelog

On approval, open a PR that assembles `changelog.d/` and the legacy `[Unreleased]` content into
`## [X.Y.Z] - <today>`, deletes the fragment files, and adds a fresh empty `[Unreleased]` above the
new section; merge it to `main`. The tag must point at the merged content.

### 7. Create the release branch and push the tag

Every minor (or major) release gets a release branch `release/<major>.<minor>` at the merged `main`
tip; the tag lands on that branch. `main` keeps taking new work, and a released line's fixes land on
its release branch — forward-port anything that still applies to `main`. See
[`specs/release-branching.md`](../../../.operator-shared/specs/release-branching.md).

The maintainer may push from the host, or ask the agent to do it through `gh`:

```
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
sha=$(gh api "repos/$repo/commits/main" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/heads/release/<major>.<minor>" -f sha="$sha"
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

A patch release tags the tip of the existing `release/<major>.<minor>` branch instead:

```
sha=$(gh api "repos/$repo/commits/release/<major>.<minor>" --jq .sha)
gh api "repos/$repo/git/refs" -f ref="refs/tags/vX.Y.Z" -f sha="$sha"
```

CI then cross-compiles the three targets, publishes the GitHub Release with auto-generated notes,
bumps the Homebrew formula, and publishes the docs.

### 8. Set the release notes

Wait for the `publish` job to create the Release (`gh run watch`, or poll `gh release view vX.Y.Z`),
then replace the auto-generated body with the authored notes:

```
gh release edit vX.Y.Z --notes-file <file>
```

Add `--prerelease` for a pre-release. The notes live only in the release body; `CHANGELOG.md` stays
the in-repo record.

Completion: `gh release view vX.Y.Z` shows the authored notes.

Then monitor and double-check the release: the three target artifacts are attached to the Release,
the Homebrew formula bump PR is open (or merged), and the Pages deploy from the tag succeeds.

### 9. Post-release

- `make upgrade-deps`, then PR any `go.mod`/`go.sum` changes.
- Bump the non-Go tool versions per [NON-GO-DEPS.md](NON-GO-DEPS.md), then PR them.
- Confirm `[Unreleased]` is empty again and `changelog.d/` holds only `README.md`.

## Non-goals

- No binary building; CI builds the release artifacts.
- No `.github/release.yml` and no CI workflow changes; the notes are authored, not tuned
  auto-generation.
- No release without the maintainer's explicit approval.
