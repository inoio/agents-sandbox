---
description: Contract for the advanced-setup CodeQL scanning (`.github/workflows/codeql.yml`) that replaces GitHub's default setup — the languages scanned, the autobuild/CGO detail for Go, the `paths-ignore` filter mirroring the `changes` gate, why default setup must stay disabled, and the job-per-language shape.
read_if: Changing `.github/workflows/codeql.yml`, the scanned languages, the CodeQL path filter or schedule, the CodeQL default-setup state in the repository, or diagnosing a red/absent CodeQL check.
---

# CodeQL scanning

## Contract

Code scanning runs from the committed advanced-setup workflow `.github/workflows/codeql.yml`, not GitHub's default
setup. The repository's default setup must remain `not-configured`: the two cannot coexist, and an advanced analysis
whose SARIF is uploaded while default setup is enabled is rejected (the workflow goes red with a configuration error).
The current state is readable with `gh api repos/inoio/agents-sandbox/code-scanning/default-setup` and set with
`gh api -X PATCH repos/inoio/agents-sandbox/code-scanning/default-setup -f state=not-configured`.

The scanned languages mirror what default setup covered: `actions`, `go`, and `ruby`. Each is a matrix entry with its
own `build-mode` — `none` for the interpreted languages, `autobuild` for Go. Go's autobuild runs with `CGO_ENABLED=1`,
matching the Makefile, because the module binds the microsandbox FFI through cgo. The query suite is the default.

## Triggers and gating

| Trigger | Notes |
|---|---|
| `push` to `main` | filtered by `paths-ignore` |
| `pull_request` | filtered by `paths-ignore` |
| `schedule`, weekly (`27 4 * * 1`) | the depth/coverage run |
| `workflow_dispatch` | a manual scan; ignores the paths filter |

The `paths-ignore` list is the complement of `ci.yml`'s `changes` gate and matches `mutation.yml`'s: `**.md`, `docs/**`,
`.operator-shared/**`, `Formula/**`, `.github/ISSUE_TEMPLATE/**`, `.gitignore`. A change to the workflow files or any
code is not ignored, so `codeql.yml` edits scan themselves. Because `docs/**` is ignored wholesale, the Jekyll Gemfile
under `docs/` is scanned only by the weekly run, not on a dependency bump. CodeQL is **not** a required branch check, so
a skipped or failing scan never blocks a merge.

## Invariants

- Default setup stays disabled while this workflow is the scanner. Re-enabling it in the repository settings silently
  breaks the advanced workflow's upload rather than producing a duplicate scan.
- A language dropped from the workflow's matrix is a coverage loss invisible to CI: default setup scanned `actions`,
  `go`, and `ruby`, so all three must stay until deliberately retired.
