---
description: Dated inventory of official configuration schemas/docs and agents-sandbox seed/read-back coverage for each built-in harness; distinguishes schema validity from transfer authorization.
read_if: Adding or reviewing a strict transfer policy for a built-in harness, assessing whether an upstream schema is version-matched or credential-safe, or deciding which harnesses need separate #849 follow-ups.
---

# Harness configuration schema evidence

**Research snapshot: 2026-11-28.** Sources are upstream documentation, schema/source repositories, and this
repository's profiles/specs/tests. No private host configuration was inspected. No harness binary was run, and
the inventory does not establish host compatibility for any complete configuration. Upstream schema/docs may
move; verify their revision against the exact harness build before using them in a transfer policy.

## Schema and documentation evidence

| Harness | Available upstream evidence | Version/provenance limit |
|---|---|---|
| `opencode` | [v1 config docs](https://opencode.ai/docs/config) point to the published [JSON Schema 2020-12](https://opencode.ai/config.json); the [schema source](https://github.com/anomalyco/opencode/blob/dev/packages/schema/src/config.ts) is generated from config types. | The schema URL and source branch are moving and do not identify a v1 release. The current schema aggregates v1 compatibility and native v2 fields; it had documented lag for earlier v2 releases ([#50852](https://github.com/anomalyco/opencode/issues/50852), [#53398](https://github.com/anomalyco/opencode/issues/53398), [#54764](https://github.com/anomalyco/opencode/issues/54764), [fix #55524](https://github.com/anomalyco/opencode/pull/55524)). |
| `opencode2` | The [v2 config docs](https://v2.opencode.ai/docs/config) and [schema](https://v2.opencode.ai/config.json) use the same source-driven schema. | No release identifier; do not infer v1-compatible and native-v2 path equivalence from the shared guest filename. |
| `pi` | Published [settings JSON Schema](https://pi.dev/schemas/settings.schema.json), described as TypeScript-generated, plus [settings](https://pi.dev/docs/latest/settings) and [configuration](https://pi.dev/docs/latest/configuration) docs. | Schema and `/latest/` docs are moving; the latest-version API reported 1.7.0 but the schema itself names no release ([API](https://pi.dev/api/latest-version)). Root `additionalProperties` is open. `models.json`, `mcp.json`, and `auth.json` are separate files. |
| `oh-my-pi` | No complete static schema found for `config.yml`; the versioned [settings registry/docs](https://github.com/can1357/oh-my-pi/blob/v19.2.1/docs/settings.md) and `omp config list --json` provide a runtime/version-specific catalog. Separate [models docs](https://github.com/can1357/oh-my-pi/blob/v19.2.1/docs/models.md) and [MCP schema](https://github.com/can1357/oh-my-pi/blob/main/packages/coding-agent/src/config/mcp-schema.json) cover other files. | v19.2.1 was the latest release found; its release metadata is not immutable. The MCP schema link is moving `main` and is not the main settings schema. |
| `claude-code` | Official [settings docs](https://code.claude.com/docs/en/settings.md) and [reference](https://code.claude.com/docs/en/settings-reference.md) link the [SchemaStore JSON Schema](https://json.schemastore.org/claude-code-settings.json). | Draft 7, mutable SchemaStore/master URL, no Claude release ID. Anthropic warns that it can lag the CLI. |
| `codex` | The [config reference](https://developers.openai.com/codex/config-reference) links a schema generated from Rust `ConfigToml`; a reproducible copy is in a [release tag](https://github.com/openai/codex/blob/rust-v0.164.0/codex-rs/core/config.schema.json), with its [generation contract](https://github.com/openai/codex/blob/rust-v0.164.0/codex-rs/core/src/config/schema.md). | The hosted schema does not identify a CLI release. Pin the source tag corresponding to the tested CLI; agents-sandbox resolves latest by default. Root closure does not close dynamic maps. |
| `hermes` | No complete published schema found. The [v0.23.4 YAML example](https://github.com/NousResearch/hermes-agent/blob/v0.23.4/cli-config.yaml.example), [defaults](https://github.com/NousResearch/hermes-agent/blob/v0.23.4/hermes_cli/config_defaults.py), and [config docs](https://hermes-agent.nousresearch.com/docs/user-guide/configuration) are evidence. | Versioned examples/defaults are not a full schema. `_config_version` is a migration stamp; the docs and [open issue #27342](https://github.com/NousResearch/hermes-agent/issues/27342) indicate incomplete/absent load-time schema validation. |
| `kimi-code` | Detailed official [TOML config docs](https://www.kimi.com/code/docs/en/kimi-code-cli/configuration/config-files.html); a Zod model exists on moving [`main`](https://github.com/MoonshotAI/kimi-code/blob/main/packages/agent-core/src/config/schema.ts). | The v2.8.1 release tag has `agent-core-v2`, not the package containing that model. Exact model/parser correspondence to the release is unverified ([release](https://github.com/MoonshotAI/kimi-code/releases/tag/%40moonshot-ai%2Fkimi-code%402.8.1), [tag tree](https://api.github.com/repos/MoonshotAI/kimi-code/contents/packages?ref=62402f173c5a838a1c1f8fa263b637a22486fa39)). |
| `qwen-code` | v0.28.4 includes a generated [Draft 7 settings schema](https://github.com/QwenLM/qwen-code/blob/v0.28.4/packages/vscode-ide-companion/schemas/settings.schema.json), its [TypeScript definition](https://github.com/QwenLM/qwen-code/blob/v0.28.4/packages/cli/src/config/settingsSchema.ts), and [generator](https://github.com/QwenLM/qwen-code/blob/v0.28.4/scripts/generate-settings-schema.ts). | Versioned and reproducible, but intended for VS Code assistance, partial, and not runtime validation. Root and credential-bearing `mcpServers`, `modelProviders`, `providerProtocol`, and `env` are open; [issue #6043](https://github.com/QwenLM/qwen-code/issues/6043) calls the schema partial. |
| `goose` | [Config docs](https://goose-docs.ai/docs/guides/config-files) and narrower Rust models for [base config](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/config/base.rs), [providers](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/config/providers.rs), and [extensions](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/config/extensions.rs). | No full `config.yaml` schema/model found; docs are `current`, source is moving `main`, and the config API allows dynamic keys. |
| `grok-build` | CLI-bundled text reference for `config.toml`, `managed_config.toml`, and `requirements.toml`, plus [Rust/Serde config types](https://github.com/xai-org/grok-build/tree/main/crates/codegen/xai-grok-config-types/src) and [xAI docs](https://docs.x.ai/build/settings). | The installed CLI's bundled reference can be version-matched, but the inspected GitHub reference and source URLs use moving `main`. No full published JSON Schema found. |
| `mistral-vibe` | The CLI can emit JSON Schema with `vibe config schema`; the [docs](https://docs.mistral.ai/vibe/code/cli/configuration) describe it. A v3.1.3 [schema implementation](https://github.com/mistralai/mistral-vibe/blob/v3.1.3/vibe/core/config/_setting_keys.py) and [Pydantic models](https://github.com/mistralai/mistral-vibe/blob/v3.1.3/vibe/core/config/vibe_schema.py) are available. | The CLI-generated schema is useful only when captured from the exact policy-tested binary. The online docs move, and this research did not run the CLI or establish the current profile's exact release. |

## Schema validity is not transfer authorization

These artifacts can supply field names, structural types, closed enums, numeric ranges, documented defaults,
and version-specific fixtures. None establishes that an arbitrary schema-valid value is not a credential.
Examples include provider API keys, `env`/header/query maps, endpoint URLs, MCP commands and arguments,
hook commands, prompt/system strings, package/plugin selectors, and dynamic provider/server/model IDs.
OpenCode, Claude Code, and Codex explicitly document such fields ([OpenCode schema](https://opencode.ai/config.json),
[Claude env vars and hooks](https://code.claude.com/docs/en/env-vars.md),
[Codex config reference](https://developers.openai.com/codex/config-reference)). Qwen's generated schema leaves
credential-capable maps open; the others likewise mix constrained values with free-form or dynamic content.

A bounded number may be a PIN, and a credential can equal an enum member. A schema validator only establishes
conformance to its schema. It does not settle credential identity, path/query safety, permission impact, parser
ambiguity, duplicate keys, insertion-order semantics, or which config shape the exact installed harness reads.
For #849, use schema evidence only to define a reviewed, exact-path value policy and its fixtures; a schema update
alone grants no new transfer permission. No candidate-provided `$schema` fetch or policy selection is allowed.

## agents-sandbox seed and read-back coverage

Harness availability, launcher snippet support, automatic host seeding, opt-in raw host drop-in, and home read-back
are separate surfaces. The current read-back path evidence is in `internal/agent/readback_test.go`, the host seed
filters in `internal/firstrun/host.go`/`seed.go`, and user-facing drop-in rules in `docs/configuration/agent.md`.

| Harness | Automatic host-config seed | Passive read-back / current credential coverage |
|---|---|---|
| `opencode` | Top-level known-format files matching `opencode*.json*`; auth store excluded. | `.config/opencode/*` and auth presence. Inline credentials recognized in provider entries and nested `options`. |
| `opencode2` | Same guest path/pattern as v1; launcher snippets use `opencode2/`. | Same config files plus SQLite `key`/`oauth` probe. Inline walker handles v1-compatible `provider.*.options.apiKey`, not native `providers.*.settings`. |
| `pi` | `settings.json`; instructions/prompts/skills use separate rules. | `settings.json` and auth presence. `models.json`/`mcp.json` are excluded from read-back. |
| `oh-my-pi` | Known-format `config.yml`/`config.yaml` snippet candidates. | `config.yml`, `config.yaml`, `models.yml`, `.env`; no complete settings credential map. |
| `claude-code` | `settings.json` is excluded from automatic seed; instruction paths are separate. | Settings are not a read-back path. `.credentials.json` is login presence; `.claude.json.primaryApiKey` has a separate bounded exposed-key path. |
| `codex` | `.codex/config.toml` is a candidate. | Config and auth store. `experimental_bearer_token` is recognized and rewritten to `env_key`; unclassified header/query values refuse. |
| `hermes` | `.hermes/config.yaml`; `.env` is excluded as a value store. | Config and `.env`. Inline `api_key` is recognized only inside a provider entry. |
| `kimi-code` | None for `config.toml`; `tui.toml` and instructions are separate. | `config.toml`, `mcp.json`, credential glob. Provider key forms are recognized; MCP tables are not complete credential identities. |
| `qwen-code` | No `settings.json` seed. | Only OAuth/MCP OAuth stores as login presence; settings are not read back. |
| `goose` | No `config.yaml` host seed; instruction files are separate. | No read-back paths. |
| `grok-build` | No `config.toml` host seed; `sandbox.toml` is a separate drop-in. | `.grok/auth.json` login path only. |
| `mistral-vibe` | No `config.toml` host seed; it is excluded from drop-in. | `.vibe/.env` only. |

The optional `provision-host-config: true` drop-in is a separate user opt-in and may copy credential-bearing
files. Launcher-authored snippets are another path. Neither demonstrates that the strict automatic seed/write-back
is supported or safe.

## Implication for #849 and policy maintenance

The #849 target selects `opencode2` as the initial evidence harness; v1-compatible and native-v2 shapes are separate
assessments. The public provider journey and documented permission examples are useful positive fixtures, not proof
that ordinary complete configurations survive. Other harnesses must report automatic transfer as unsupported until
their own policy, credential classification, and whole-config evidence exist; they must never fall back to the old
heuristic. The user-facing report is a breaking default-behavior change.

For each covered harness, record the exact tested harness version, immutable schema/source revision where available,
source provenance, exact authorized paths/domains, and positive and adversarial whole-config fixtures. Review new,
changed, removed, or semantically changed upstream fields before widening policy. For moving or absent schemas, use
versioned docs and fixtures; do not invent a full schema from examples. No runtime network fetch or automatic policy
expansion is part of schema maintenance.

**Unverified:** no complete host configs or private 40/3 path measurements from #897 were available, no harness
binaries were run, and no host seed/write-back → provisioning → provider request → subsequent-start journey was
performed. These facts must not be reported as tested compatibility.
