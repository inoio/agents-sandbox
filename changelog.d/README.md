# Changelog fragments

This directory holds one fragment file per changelog-worthy change, so parallel pull requests never
edit the same file. `CHANGELOG.md` is assembled from these at release time and is not edited by hand
between releases.

## Add a fragment

Create `changelog.d/<slug>.<type>.md`, where:

- `<slug>` is the **PR number**. It is unique across pull requests, so two PRs cannot pick the same
  filename — an issue number is shared when several PRs close the same issue. Put the `(#issue)`
  reference in the bullet text, where it already lives.
- `<type>` is one of `added`, `changed`, `deprecated`, `docs`, `fixed` — the section the entry
  belongs to in `CHANGELOG.md`.
- `<n>` numbers several entries of the same type from one change (`103.changed-1.md`,
  `103.changed-2.md`); a single entry omits it.

Put one bullet's text in the file, **without** the leading `- `, wrapped to 120 columns like the rest
of the changelog. A change spanning two sections uses two files.

Example — `changelog.d/468.changed.md`:

```
Changelog entries are added as per-PR fragments in `changelog.d/` instead of editing `CHANGELOG.md`
directly, so pull requests no longer conflict on the changelog.
```

A required `changelog` check enforces a fragment whenever a pull request changes production code (a
docs-only or test-only change needs none). The full contract is in
[`.operator-shared/specs/changelog-fragments.md`](../.operator-shared/specs/changelog-fragments.md).
