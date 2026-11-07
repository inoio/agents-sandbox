---
description: Contract for the YAML dependency — every parse/serialize goes through the maintained `go.yaml.in/yaml/v3`, not the unmaintained `gopkg.in/yaml.v3` v3.0.1, whose emitter dropped a leading blank line and broke the state serialization-stability invariant `FuzzParseState` asserts.
read_if: Adding or changing a YAML parse/serialize path, touching `go.mod`'s YAML dependency, or triaging a `FuzzParseState` serialization-stability failure.
---

# YAML library

## Contract

- Every YAML parse and serialize in the launcher goes through `go.yaml.in/yaml/v3` (the maintained continuation of
  go-yaml), never a direct import of `gopkg.in/yaml.v3`.
- `gopkg.in/yaml.v3` still resolves as an indirect *test* dependency of `go-git` via `gomega`, so it stays in
  `go.sum`; that is not a licence to import it directly.

## Why

`gopkg.in/yaml.v3` v3.0.1's emitter corrupts a string with a single leading newline on marshal: it emits a literal
block scalar with an explicit indentation indicator and strip-chomping (`home_volume: |4-` followed by the indented
content) that drops the leading blank line and the trailing newline, so re-reading what was written yields a different
value. The maintained fork (v3.0.4+) emits the leading blank line and round-trips. This is not a state-file concern
only — any field can carry such a string — but it surfaced through the state parser's stability invariant.

## Regression seed

`internal/sandbox/state/testdata/fuzz/FuzzParseState/79cbe6941173cc3b` holds the crasher found by the fuzz workflow
(`home_volume: >\n\n 0`) and runs under `make test`.
