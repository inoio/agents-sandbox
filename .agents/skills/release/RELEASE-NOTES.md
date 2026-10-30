# Release notes

The GitHub release body: authored in [step 4](SKILL.md#4-author-the-release-notes) from the curated
`[Unreleased]` content, injected in [step 8](SKILL.md#8-set-the-release-notes). It is not committed;
the release body is its only home.

## Structure

````markdown
<one-paragraph summary of the release; skip for a small patch release>

## Added

- ...

## Changed

- ...

## Fixed

- ...

## Contributors

Thanks to everyone who contributed to this release!

- @<login> (first contribution)
- @<login>

**Full Changelog**: https://github.com/<owner>/<repo>/compare/v<prev>...v<version>
````

- Mirror the changelog's headings, but include only the ones with content; drop a heading rather
  than writing "nothing".
- Pitch for a user of the tool, not a reader of the diff: what changed for them.
- Keep the compare link at the bottom — it is the reader's path to the full change set.

## Contributors

Pull the contributor list from GitHub's generate-notes endpoint. It computes the merged-PR authors
in the previous-tag-to-current range and already separates first-time contributors:

```
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner)
gh api "repos/$repo/releases/generate-notes" \
  -f tag_name="v<version>" \
  -f previous_tag_name="v<prev>" \
  --jq .body
```

The returned body carries a `## New Contributors` section listing the first-time contributors. Use
those logins for the highlighted lines; if the section is absent, no one is new this release. For
the returning contributors, reuse the authors from the generated `## What's Changed` list.

List first-time contributors first, each marked `(first contribution)`, then the returning ones.
The range the endpoint uses is the same window as the `[Unreleased]` section, so the notes and the
changelog describe one set of changes.
