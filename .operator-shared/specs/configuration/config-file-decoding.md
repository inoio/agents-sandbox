---
description: Contract for reading and decoding launcher, home-manifest, and agent snippet config files - one `configdecode` core (`FormatFor`/`Decode`/`DecodeFile`) over `configread`'s bounded read and `configpaths`' extension vocabulary, with each loader's accepted-format set, error label, and parse-failure policy kept explicit rather than copied.
read_if: Changing how a config file is read, classified, or JSON5/JSONC-normalized in `homeconfig`, `viperconfig` (`mergeDir`, `LayerSettings`, the owner scan `layerSetsKey`), or `configmerge`; adding a config format; touching the launcher read path's YAML parser (viper's `yaml/v3` vs our `yaml/v4`), the `MergeConfig` -> `MergeConfigMap` seam, or the project-config trust projection's `AllSettings`; or diagnosing a file one loader accepts and another rejects.
---

# Config-file reading and decoding

Four code paths each implemented "read a config file and turn it into a `map[string]any`", and could
drift: a file one loader accepted, another could reject or parse differently. `internal/configdecode`
is the one core they all call, so an intended difference is a parameter, not an accident of the copies.

## Layering

Three packages stack, each depending only on the one below. Parsers stay out of the wide-importer
packages.

```
configpaths   extension vocabulary (ExtJSONC/ExtJSON5), FindConfigFile, supportedExts   — no parser deps
   ^
configread    ReadFile under MaxBytes/ErrTooLarge (config, secret, snippet)             — no parser deps
   ^
configdecode  FormatFor, Decode, DecodeFile                                             — json5 + yaml/v4 + toml/v2 + yamlfmt
```

`configread` stays its own package. It is the bounded-read boundary ADR
[`0001-yaml-handling.md`](../../adr/0001-yaml-handling.md) names, it serves the `envsecret` reads
that are not config decodes, and folding the parsers into it would pull `json5` and `go-toml/v2` into
`envsecret`'s import graph. `configpaths` keeps `ExtJSONC`/`ExtJSON5` and `supportedExts`; it stays
parser-free because its importer set is wide.

## The core

`internal/configdecode` exposes exactly three functions:

```go
// Format is the serialization family a config file's extension implies.
type Format int // FormatYAML, FormatJSON, FormatTOML

// FormatFor classifies a file name's extension (case-insensitively). ok is
// false for an unknown extension, so a caller that accepts only some formats
// can reject it itself.
func FormatFor(name string) (Format, bool)

// Decode parses data into a string-keyed map using format. The JSON family
// (JSON, JSONC, JSON5) is parsed with titanous/json5, YAML with
// go.yaml.in/yaml/v4, TOML with pelletier/go-toml/v2.
func Decode(format Format, data []byte) (map[string]any, error)

// DecodeFile reads path under configread.MaxBytes and decodes it, deriving the
// format from the path extension. label names the file in errors. An unknown
// extension is an error.
func DecodeFile(path, label string) (map[string]any, error)
```

`DecodeFile` reproduces the message shape the loaders have today, so their pinned tests keep passing:

- a read failure (including `configread.ErrTooLarge`) returns `read <label> <path>: %w`, wrapping the
  `*fs.PathError` so `errors.AsType[*fs.PathError]` still matches;
- a YAML parse failure returns `yamlfmt.WrapErr(path, err)` (`<path> contains invalid YAML
  (line N): %w`), which is why `configdecode` depends on `internal/yamlfmt`;
- any other parse failure returns `parse <label> <path>: %w`.

`.json`, `.jsonc`, and `.json5` all classify as `FormatJSON` and all decode with `json5`, so a comment
or trailing comma is accepted in every one of them (today the launcher parses `.json` with viper's
strict `encoding/json`, and the owner scan with `encoding/json`). `json5` also accepts `NaN` and
`Infinity`; see *Behavior changes*.

## Per-site wiring

| Site | Byte source | Entry point |
|---|---|---|
| `homeconfig.ReadHomeFromConfigDir` | host `config.<ext>` from `configpaths.FindConfigFile` | `DecodeFile(path, "config")`, then a shared `home` extractor |
| `viperconfig.mergeDir` | host `config.<ext>` | `DecodeFile(path, "launcher config")`, then `v.MergeConfigMap` |
| `viperconfig.layerSetsKey` (owner scan) | host `config.<ext>` | `DecodeFile` |
| `configmerge.scanSnippets` | host snippet matched by an agent glob | `FormatFor` + `configread.ReadFile` + `Decode`, recording a `SkippedSnippet` on read or parse failure |
| `configmerge.DocumentsEqual` | `cf.Merged` (built in memory) + guest bytes from `sb.FS().Read` | `FormatFor` + `Decode` |

`mergeDir` has a second consumer, `viperconfig.LayerSettings` (used by the project-config trust
projection, `internal/projecttrust/projection.go`): it calls `mergeDir` and returns `v.AllSettings()`.
It is unchanged by this work but inherits the parser move, so its output is part of the compatibility
guarantee below.

Only `configmerge.DocumentsEqual` has no host file to read: it compares the merged agent config
`BuildMerged` produced against the document read back from the VM (called from
`reprovision.ChangedAgentConfigPaths`, whose `vmData` comes from `ReadVMConfig` → `sb.FS().Read`). Every
other site reads a discovered host file and uses `DecodeFile`. `configmerge` reads through
`configread.ReadFile` itself and calls `Decode` because of the unknown-extension default below;
`DecodeFile` deliberately carries no fallback format.

`homeconfig.ParseHomeSection(data []byte, path string)` stays exported (its tests and
`FuzzParseHomeSection` call it). Today it does its own `yaml.Unmarshal`; it becomes a thin wrapper that
decodes the bytes as `FormatYAML` and extracts `home` through the same helper
`ReadHomeFromConfigDir` uses, so the extraction rules live once. `ReadHomeFromConfigDir` decodes via
`DecodeFile` (so `.json5` is read with json5) and shares only the extractor.

## Divergences, made explicit

- **Accepted formats.** `configpaths.supportedExts` (`.yaml`, `.yml`, `.json`, `.jsonc`, `.json5`) is
  the set the launcher config, the home manifest, and the owner scan discover; TOML is not in it, so
  none of them can read a `config.toml`. `configmerge` is glob-driven by the agent's `SnippetPattern`
  (`opencode*.json*`, `config*.toml`, `config*.{yml,yaml}`), so its set is open and includes TOML.
  `configdecode` can classify and decode TOML; the accepted set, not the primitive, is what keeps TOML
  out of the launcher. This is why `configType`/`configFormatFor`/the owner-scan `switch` collapse into
  `FormatFor` while `supportedExts` stays.
- **Unknown extension.** `FormatFor` reports `ok=false`. Both `configmerge` callers map that to
  `FormatJSON` — `parseConfigFile`-equivalent for parsing and `marshalConfig` for serializing (a
  `settings.json.bak` snippet is best-effort parsed as JSON5 and skipped on failure); the launcher
  never reaches the case because `FindConfigFile` gates on `supportedExts`.
- **Parse failure.** `homeconfig` and `viperconfig` fail the read; `configmerge` skips the snippet and
  records it as a `SkippedSnippet` on `MergedConfig.Skipped`, which `reprovision.LoadConfigFilesForHost`
  reports as a warning (one per skipped file). This is caller error handling, not a primitive concern.
- **Error label.** `DecodeFile`'s `label` preserves each loader's prefix (`config`, `launcher config`)
  and each loader keeps wrapping the failure in its own message.

## Behavior changes

- **`.json` and `.json5` become json5-lenient everywhere.** A `.json` launcher config, a `.json`
  owner-scan file, and a `.json`/`.json5`/`.jsonc` home manifest may now carry `//` comments, trailing
  commas, and non-finite numbers. Loosening only for syntax; the non-finite case is called out next.
- **Non-finite numbers now decode.** `titanous/json5` accepts `NaN`/`Infinity`, so `Decode` returns
  them successfully. The previous rejection was a side effect of the removed normalize step
  (`json.Marshal` on a `NaN`), not a designed guard. The decode core no longer rejects them; how one is
  handled is key-specific — `cpus: NaN` fails `validate`'s range check, while `memory: NaN` decodes to the
  string `"NaN"` and later falls back to the default memory like any other unparseable `memory` value.
  Under an unknown key it is silently stored, and `homeconfig` stops rejecting a `NaN` anywhere. This is
  the one substantive semantic change; the alternative — a non-finite scan in `Decode` — is deliberately
  not taken.
- **The launcher's YAML read moves from viper's `go.yaml.in/yaml/v3` to our `go.yaml.in/yaml/v4`.**
  `viper.MergeConfig` is `unmarshalReader` (viper's codec) followed by `MergeConfigMap`; calling
  `MergeConfigMap` with a map we decoded skips viper's `unmarshalReader` entirely, so viper no longer
  parses the launcher config. `MergeConfigMap` still runs `insensitiviseMap`, and `InConfig` and
  `AllSettings` still read the merged map, so key folding and presence are unchanged. This amends ADR
  `0001-yaml-handling.md`: its "viper only reads YAML ... its pinned `v3` cannot reintroduce the
  emitter defect" clause, its `Non-goals` clause that the owner scan "stays on yaml.v3/json5/json",
  and its dependency statement are all updated.

## Test-and-symbol inventory

The change edits or removes these, beyond the two deleted error branches:

- `internal/viperconfig/load.go`: `configType`, the `ctYAML`/`ctJSON` constants (`config.go`), and the
  now-unused `bytes`, `encoding/json`, `json5`, `strings` imports go. `mergeDir` loses its
  `yamlfmt.WrapErr`/`load launcher config` arms (moved into `DecodeFile`).
- `internal/viperconfig/load_test.go`: `TestConfigTypeUnknown` is removed. The `LayerSettings` tests
  stay.
- `internal/viperconfig/resolver_flow_test.go` `TestNewResolverReportsABrokenConfigLayer`: the
  "malformed json5" row keeps `parse launcher config`; the "malformed json" row changes from
  `load launcher config` to `parse launcher config`; the removed "json5 that cannot be normalized" (NaN)
  row is replaced by `TestNewResolverRejectsANonFiniteNumber` (the `validate` range rejection for `cpus`)
  and `TestNewResolverDecodesANonFiniteMemory` (`memory: NaN` decodes to `"NaN"`). The "malformed
  per-slug/project yaml" rows and `TestResolverConfigSyntaxErrorIsFriendly` (`contains invalid YAML (line 2)`)
  stay green via `DecodeFile`.
- `internal/homeconfig/homeconfig_test.go`: `TestReadHomeFromConfigDirNormalizeErrorKeepsPresentFalse`
  is removed; the `ParseHomeSection` tests and `FuzzParseHomeSection` stay (adapted to the wrapper).
- `internal/configmerge/merge.go`: `configFormatFor`/`configFormat` give way to `FormatFor`; the
  parsing default lives at both `scanSnippets` and `marshalConfig`.

## The viper seam

The `MergeConfig` -> `MergeConfigMap` swap is the one behavioral risk: `MergeConfig` is exactly
`unmarshalReader` + `MergeConfigMap`, so merge order and key folding are identical, but the *decoder*
changes (`yaml/v3` -> `yaml/v4`, strict JSON -> json5). The change is covered by a compatibility test
that feeds the existing config fixtures through both `MergeConfig` and the new `DecodeFile` +
`MergeConfigMap` path and asserts the resolved `*viper.Viper` state is identical: values, types,
`InConfig` for every key, and `AllSettings` (the last because `LayerSettings` feeds the trust
projection's approve-on-change diff).

## Non-goals

- No TOML in the launcher, home manifest, or owner scan: `supportedExts` is unchanged.
- No new config formats, and no change to config discovery order or the six-layer precedence.
- `configread` is not merged or renamed; `MaxBytes`/`ErrTooLarge` keep their identifiers.
- `envsecret`, `state`, and the `oneyaml` write path (`WriteSetting`) are untouched.
