# Changelog fragments

This directory holds one fragment file per changelog-worthy change, so parallel pull requests never
edit the same file. `CHANGELOG.md` is assembled from these at release time and is not edited by hand
between releases.

## Add a fragment

Create `changelog.d/<slug>.<type>.md`, where:

- `<slug>` is the **issue number** when the change has one, otherwise the **PR number**. It must be
  unique within this directory; the number guarantees that.
- `<type>` is one of `added`, `changed`, `deprecated`, `docs`, `fixed` — the section the entry
  belongs to in `CHANGELOG.md`.

Put one bullet's text in the file, **without** the leading `- `, wrapped to 120 columns like the rest
of the changelog. A change spanning two sections uses two files.

Example — `changelog.d/468.changed.md`:

```
Changelog entries are added as per-PR fragments in `changelog.d/` instead of editing `CHANGELOG.md`
directly, so pull requests no longer conflict on the changelog.
```

A required `changelog` check enforces a fragment whenever a pull request changes code (a docs-only
change needs none). The full contract is in
[`.operator-shared/specs/changelog-fragments.md`](../.operator-shared/specs/changelog-fragments.md).
