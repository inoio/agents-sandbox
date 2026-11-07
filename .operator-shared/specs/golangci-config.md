---
description: This repo's golangci-lint standard — the formatter set (`gofumpt` + `gci` + `golines`, with `gofmt`/`goimports` deliberately not used and the gci section order), why the swap was made, and the linter-config posture (upstream-golden baseline with the two free settings adopted and the two costly strict settings declined).
read_if: Changing `.golangci.yml` (formatters or linters), deciding whether to enable a linter or a stricter setting, editing `CONTRIBUTING.md`'s formatting/linting guidance, or diagnosing a formatting or lint-gate disagreement.
---

# golangci-lint configuration

The single config is `.golangci.yml` at the repo root. CI enforces both halves: the `lint` job runs
`golangci-lint run`, and `make validate-fmt` (`golangci-lint fmt -d`) fails on any formatting drift.
`make check`/`make verify` run the same targets locally.

## Formatter standard

`formatters.enable` is exactly `gofumpt`, `gci`, `golines`:

- **`gofumpt`** — stricter `gofmt`, backwards compatible; subsumes `gofmt`, so `gofmt` is not listed.
- **`gci`** — canonical import sections, in order `standard`, `default` (third-party), then
  `prefix(github.com/inoio/agents-sandbox)`. `goimports` is not listed: `gci` owns import ordering,
  and `goimports`' remaining job (adding/removing imports) never fires here — imports are a compile
  error in Go, editors resolve them on save, and any import `gci` groups is already present.
- **`golines`** — wraps at 120 columns (`max-len: 120`). This is the dominant formatter cost.

This replaced the earlier `gofmt` + `goimports` + `golines` set. `gofmt` was redundant with
`goimports`, and `goimports` cost roughly half the run while producing byte-identical output on the
repo; the swap (gofumpt + gci = about a third of goimports' cost) roughly halved `make fmt`
(~9s → ~4s). The one-time reformat touched ~51 Go files.

## Linter posture

The linter set tracks the upstream `maratori/golangci-lint-config` golden config for the pinned
golangci-lint version, with deliberate repo deviations:

- **Added:** `decorder`, `exhaustruct_v5` (with a project-specific `ignore-patterns` list).
- **Replaced:** `revive.exported` is disabled (stutter detection is intentionally not enforced), and
  the upstream revive/TODO *text* exclusions are replaced by `testmock`/`_test.go` path rules.
- **Kept off though upstream enables them:** `funcorder`, `gochecknoglobals`, `mnd`, `paralleltest`,
  `recvcheck`, `testpackage` (each is a taste or false-positive call documented inline).

Two free upstream settings are adopted because they surfaced no findings: `exhaustruct_v5`
`allow-empty-blank-assignments: true` and `perfsprint.error-format: false`.

Two stricter upstream settings are **deliberately declined**, both because they carry large manual
churn and no auto-fix (neither `godoclint`'s `require-stdlib-doclink` nor `govet`'s `shadow` analyzer
offers a fix — a `--fix` run changes nothing):

- `godoclint.require-stdlib-doclink` — ~54 doc-comment sites, each needing `[os.Exit]`-style links.
- `govet.shadow.strict` — ~119 `err`-shadow reports, mostly benign.

Settings blocks for linters that are not enabled (`funcorder`, `inamedparam`, `mnd`) are commented
out as dormant; uncomment them alongside the linter if it is ever enabled. (Note `issues`
`max-issues-per-linter` defaults to 50, which caps how many of a linter's findings a sweep shows.)
