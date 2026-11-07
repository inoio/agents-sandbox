---
description: Decision record for the config write path (`oneyaml.dev/go` over `goccy/go-yaml`) and the 16 MiB cap on untrusted config/secret/snippet reads.
read_if: Changing `viperconfig.WriteSetting`, the YAML edit path, or untrusted config/secret read bounds; or evaluating oneyaml vs goccy.
---

# YAML handling

Date: 2026-11-06.

Status: Accepted.

## Context

`viperconfig.WriteSetting` persists an accepted VM resource raise into the owning config layer by editing
the file in place. `TestWriteSettingPreservesFileBytes` pins byte-for-byte preservation of everything but
the edited value, and comments (including on the edited key) must survive; the edited layer can be
untrusted project config (`specs/configuration/project-config-trust.md`). The previous implementation hand-rolled the
edit: parse into a `yaml.Node` tree, splice the value text with a read-back check, fall back to a
node-tree re-encode with `copyComments`, plus a case-folded key match and an anchored-value refusal —
~430 lines in `setting_writer.go`.

The YAML parse/serialize library is `go.yaml.in/yaml/v3` (see *The YAML dependency* below). The questions
were whether a library could replace the hand-rolled editor, and whether the config parses are bounded on
untrusted input.

## Options

1. **Keep the hand-rolled editor.** No new dependency and the byte contract stays, at the cost of keeping
   ~430 lines of single-purpose splice code.
2. **Replace it with `github.com/goccy/go-yaml`.** Mature (2.2k★); its AST keeps comments and anchors and
   has a YAMLPath replace API.
3. **Replace it with `oneyaml.dev/go` (recursive-ai-oss/oneyaml-go).** A lossless `Document` whose
   `Set`/`Remove`/`Rename` change only the addressed value and keep every other byte and comment.

## Decision

**Option 3: adopt `oneyaml.dev/go` for the config write path.**

- `WriteSetting` parses with `oneyaml.Parse(data, oneyaml.Untrusted())` and writes `Document.Bytes()`
  atomically. The dotted key is matched case-insensitively (viper resolves keys case-insensitively) by
  walking `Root().Entries()` with `strings.EqualFold` and setting through the matched value node's
  `Node.Pointer()`; the library's pointer tokens match key text exactly, so the fold is the caller's job.
- An anchored value that an alias elsewhere references is still refused (`Node.Anchor()` /
  `Node.AliasName()`), preserving the previous `ensureReplaceableValue` contract.
- The **only** behaviour change is that a replaced value keeps its quote/block style (`"4G"` → `"6144M"`)
  instead of being re-rendered plain; the pinned expectations in `setting_writer_test.go` were updated.
- **Option 2 rejected.** goccy's emitter rebuilds text from tokens: it normalises inter-token whitespace
  (tab→space, `CRLF`→`LF`, trailing whitespace stripped) with no option to control it, and turns
  `key: # comment\n  value` into `key # comment: value`, which no longer parses as a mapping. It cannot
  meet the byte contract.
- The port was validated before adoption: `internal/viperconfig`, `internal/sandbox/resourceproposal` and
  `internal/configpaths` pass, `make lint` is clean, and the only failing tests in the full suite are two
  pre-existing `internal/sandbox/session` failures that also fail on the untouched baseline.
- `setting_writer.go` is ~295 lines (was ~432). A library cannot remove the surrounding atomic-write,
  symlink, file-mode and non-YAML-refusal logic.

### The YAML dependency

- Every YAML parse and serialize goes through `go.yaml.in/yaml/v3` (the maintained continuation of go-yaml);
  `gopkg.in/yaml.v3` is never imported directly. It still resolves as an indirect *test* dependency of
  `go-git` via `gomega`, so it stays in `go.sum`; that is not a licence to import it.
- Why: `gopkg.in/yaml.v3` v3.0.1's emitter corrupts a string with a single leading newline on marshal
  (`home_volume: |4-` followed by the content), dropping the leading blank line so a re-read differs; the
  maintained fork (v3.0.4+) round-trips. The crasher (`home_volume: >\n\n 0`) is the fuzz seed
  `internal/sandbox/state/testdata/fuzz/FuzzParseState/79cbe6941173cc3b`, run under `make test`.

### Parse bounds — a shared input-size cap

A second decision was taken alongside the library choice: bound the untrusted reads by input size.

- **`internal/configread.ReadFile` caps every untrusted config, secret and snippet read at 16 MiB**
  (`MaxBytes`, `ErrTooLarge`). It is applied at `viperconfig.mergeDir` and its owner-scan read,
  `WriteSetting`'s own read, `homeconfig.ReadHomeFromConfigDir`, `envsecret.ParseSecretSpecYAML` /
  `ParseSecretSpecLegacy` / `BuildEnvMap`, and `configmerge`'s snippet scan. The `envsecret` readers warn when a
  *present* file exceeds the cap, so a dropped scope is visible; a missing optional file stays quiet.
- Rationale: the parsers bound *structure* (yaml.v3 depth 10000 and alias expansion; `encoding/json` and
  TOML depth 10000) but none bounds total input size, so the file read is where that is capped.
- **JSON5 is kept**: `titanous/json5` stays and is bounded only by this size cap. It remains unbounded in
  nesting depth (no maintained guarded full-JSON5 decoder exists), so a large enough nesting bomb could
  still overflow the stack; the cap limits its size but does not eliminate it. This residual is accepted.
- Rejected: narrowing the accepted grammar to JSONC (a documented-behaviour change), and migrating the
  reads to `oneyaml` (would change owner-resolution key semantics).

## Consequences

- `oneyaml.Untrusted()` must be passed wherever this parse handles untrusted input; without it the input,
  scalar, and document counts are unbounded.
- Adopting the library accepts its maturity as a known risk (at adoption: single author, weeks old, no
  visible adoption, unsigned release). Its licence is dual Apache-2.0 OR MIT.
- `go.yaml.in/yaml/v3` (with `titanous/json5` and `encoding/json`) stays on the read path and the owner
  scan, so two parsers coexist: `oneyaml` owns the write path only.
- A future change to the write path must preserve the byte-and-comment contract or amend this ADR.
- Every untrusted config, secret and snippet read must go through `configread.ReadFile`; a new read site
  that calls `os.ReadFile` directly re-opens the unbounded-size vector.

### Non-goals

- **The owner scan (`LayerSettingOwner` → `readLayerValues`) stays on yaml.v3/json5/json; migrating it to
  oneyaml is not planned.** It is a read path with no byte contract, it parses three formats (oneyaml is a
  YAML document API, so it would not even unify them), and it resolves keys case-insensitively through a
  `map[string]any` walk that oneyaml's case-sensitive pointers would have to rebuild. Migrating it would add
  code and change owner-resolution semantics for no benefit. The scan and the editor therefore no longer
  share a parser: a YAML file the scan accepts but oneyaml rejects surfaces as a `WriteSetting` error from
  `PersistMemory` (an error path, never a shadowed write), and the scan's skip-unparseable-layer guard
  covers the reverse.

### Follow-ups

- `titanous/json5`'s unbounded nesting depth is mitigated only in size by the input cap; a nesting guard
  for JSON5 config remains open.
