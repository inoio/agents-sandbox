---
description: Durable design contract for agents-sandbox first-run onboarding, safe config migration, credential-kind handling, network posture, the tiered setup wizard, and `config harden`.
read_if: Working on config migration, first-run guidance, credential/auth handling, network egress derivation, the setup wizard, or `config harden`.
---

# Guided onboarding and credential handling

## Problem

The secure default `provision-host-config: false` removed raw host credentials from the VM but also removed the
convenience of "it just works". The first-run experience must therefore guide a user from a host machine with existing
agent configuration to a working, reviewable sandbox configuration without ever copying raw host credentials into the
VM.

PR #103 built the mechanical half of this (a `configmigration` package that plans, reviews, and applies a safe
migration, plus a wizard in `Guide`). This spec keeps that machinery and corrects its model so it matches the project's
core intent: **secure by design and easy at the same time**. See "Relationship to PR #103" for what changes.

## Principles

1. **Prefer static secrets over interactive login.** Where a provider offers an API key or bearer token, migrate it to a
   microsandbox secret (placeholder in the VM, raw value on the host, host-scoped substitution). This is the most secure
   and the most deterministic path.
2. **Never placeholder-migrate refreshable OAuth.** An OAuth access/refresh token is refreshed by the agent and rewritten
   inside the VM, so treating it as a static secret either materializes real tokens in the VM or depends on fragile
   proxy-mediated refresh.
3. **Host-first onboarding is the documented story.** A new user sets the agent up on the host; agents-sandbox detects
   that config and migrates it. The wizard makes this explicit; it does not silently do nothing when config is missing or
   unmigratable.
4. **Least privilege by default, public only as an informed choice.** Network egress defaults to `none`
   (deny-by-default + explicit allow). A user may consciously choose public access; agents-sandbox never silently broadens
   or silently narrows.
5. **Always explicit outcomes.** Every interactive first run ends in a named outcome (migrated, login hand-off,
   host-first hand-off, review hand-off, manual, dismissed). No path returns silently leaving the user with an
   unconfigured sandbox.
6. **Guide, don't block.** Non-interactive runs warn and continue; they never hang or fail on onboarding.

## Credential kinds

Every native auth entry is classified by a single concept, independent of agent:

- **`Static`** — an inline, non-refreshable secret that can be replaced by a placeholder: API keys (`type: "api"` /
  `"api_key"`), well-known tokens (`type: "wellknown"`), bearer tokens. **Migrate**: placeholder + `env.secret.yaml` +
  egress-allow host.
- **`Login`** — refreshable OAuth (`type: "oauth"`) or machine-bound login state that agents-sandbox cannot read (Claude
  Code's `.credentials.json`, `~/.claude.json`, or OS keychain). **Do not copy**: drop the entry from generated files,
  allow the login hosts, and let the user authenticate inside the persistent sandbox home.

OAuth detection is generic: the auth entry's `type` field equals the agent's declared login auth type (default `"oauth"`).
An agent declares its credential shape on `ConfigMigrationSpec`:

- `LoginAuthTypes []string` (default `["oauth"]`).
- `LoginHosts map[string][]string` — per-provider hosts needed for the login flow, defaulting to `KnownProviderHosts`
  where a provider's login and API endpoint share a host.

Claude Code is not a special case by design; it is simply the agent whose credential is always `Login` (it declares no
`NativeCredential` and pre-allows its login hosts via `RequiredNetworkHosts`).

When native config uses `Login` but the provider also offers an API key, the wizard recommends switching to the key
path (host-side secret) while still allowing the login fallback.

## Host determination

Two independent lists, never conflated:

- **Network egress hosts** (`network.profile` + `egress-allow`/`deny`): where the VM may connect at all.
- **Secret substitution hosts** (each secret's `hosts`): where the proxy may replace a placeholder with the real value.

Resolution for **network** hosts depends on the chosen posture:

1. Least privilege → derive from custom endpoints, then the curated known-provider table, then ask the user.
2. Public → no per-host enumeration; set the broad policy and stop deriving.

Resolution for **secret substitution** hosts is required for every migrated `Static` credential, even under a public
network posture, unless the user explicitly chooses `AllowAnyHostDangerous`:

1. Custom endpoint in the native config (`baseURL` / `endpoint` / `url`) → its host.
2. Known provider → curated table.
3. Otherwise → **ask the user**.

Login hosts for the `Login` fallback resolve as: agent-declared per-provider table (suffixes permitted), then ask, then
degrade to a warned explicit option (documented public profile or manual `egress-allow`). An unresolved host is never a
dead end.

## Onboarding state machine

**Derived state is authoritative**; only user decisions and the apply journal are persisted. This ensures external file
changes are always observed. State is keyed by the native-configuration source hash (an existing concept), so any native
file change re-arms the entire flow.

Persisted in the per-agent manifest `~/.local/state/agents-sandbox/<agent>/config-migration.yaml`:

- `state` — the last terminal decision for this native configuration: `completed`, `dismissed`, `manual`,
  `login-pending`.
- `outputs` — the apply journal for interrupted-apply resume (existing).
- `pending` — login-required providers and their allowed hosts.

Derived each run by `Guide`:

| Native config | Managed config | Decision | Outcome |
|---|---|---|---|
| none | none | — | host-first hand-off |
| none | present | — | completed (nothing to do) |
| present | none | static/settings | migrate (→ `login-pending` if OAuth also present) |
| present | none | OAuth only | login hand-off |
| present | none | unsupported/malformed | review hand-off |
| any | any | dismissed | quiet until source hash changes |

`login-pending` is a **verification loop, not terminal**: on the next interactive start, ask the user whether the
in-sandbox login worked. `Yes` → `completed`. `No` → re-enter host review and keep `login-pending`. `Not yet` → quiet
reminder next time. Non-interactive runs print a one-line reminder and never prompt.

Optional enhancement (not a dependency): if a stat of the persistent VM home can detect the agent's credential file,
auto-confirm the login and skip the question.

## `Guide` outcomes

`Guide` is a decision tree whose every interactive branch terminates in an explicit outcome:

- `provision-host-config: true` → skip entirely (explicit unsafe opt-in).
- Managed config present → completed; optionally suggest `config harden`.
- Native config with migratable content → **Migrated**: `Review` shows generated files, placeholders, login-required
  providers, and warnings; `Apply` writes; then confirm start. `login-pending` providers get login guidance.
- Native config with OAuth only → **Login hand-off**: explain why it is not copied, allow hosts, record
  `login-pending`. Warnings are surfaced; this must never be a silent no-op.
- Native config, unsupported/malformed only → **Review hand-off**: surface every warning ("review required", "skipped
  malformed", "found but not copied"); offer host-first, manual config, or start-anyway.
- No native config → **Host-first hand-off**: "set up `<agent>` on the host, then rerun; agents-sandbox will migrate
  it", plus manual-config docs and start-anyway.

Warnings and review-required items are surfaced **before** any "has changes" gate, so a plan with nothing to write still
communicates its findings. This corrects the current behavior where `HasChanges()` short-circuits `Guide` and
`applyAuthMigration` returns before appending warnings when the generated auth is empty.

Non-interactive runs: warn (native config found but not migrated, etc.) and continue.

## Tiered setup wizard

A declarative, extensible catalog of `SetupQuestion`s (key path, tier, prompt, default, validation). Every question has
a default; accepting defaults is one keystroke.

- Tier membership (fixed; the catalog stays re-tierable without structural change — see
  `internal/configwizard/catalog.go` for the concrete catalog):
  - **T1 essentials** — `agent` (only when unset) and `network.profile`.
  - **T2 resources** — `cpus`, `memory`, `disk-size`, `workspace-quota`, collapsed behind one "resource defaults ok?"
    confirmation.
  - **T3 runtime** — `dind`, `tmp-size`, `log-level`.
  - **T4 integrations** — `notify.*`, `upgrade.*`, `auto-prune-age`.
- First run asks T1–T3. Deeper tiers are opt-in later via `config wizard --tier=N`; `config wizard` with no flag asks
  all unanswered tiers. Only the first run or the no-flag full wizard marks the first run done; an explicit
  `config wizard --tier=1` must not suppress the later first-run tiers.
- The wizard runs as a first-run step **before** credential migration, then run options are re-resolved so migration sees
  the chosen agent and network posture.
- A value already set at a more specific scope (project / per-slug / env / flag) is never overwritten; the question is
  skipped and this is reported.
- Accepting a default is a no-op for an existing value: the offered default is the value already set in the user config
  when present (for choice questions only when it is one of that question's choice keys), otherwise the catalog default.
  A value is written only when it differs from the current user-config value, so pressing Enter through an existing
  config leaves the file untouched and reports zero writes.
- A group of related value questions may be collapsed behind one confirmation: e.g. a single "resource defaults ok?"
  accepts `cpus` / `memory` / `disk-size` / `workspace-quota` together, and only expands them into individual questions
  when declined.
- Network posture is asked at every first run (it is the one question that is always relevant to credential handling),
  unless a more specific scope already fixes it.
- **User control / early exit:** every non-essential question offers an "enough questions — use defaults for the rest"
  option that stops the interview immediately and applies defaults to all remaining questions (recorded, so it does not
  re-ask). Essential questions — agent selection when unset, and network posture — are asked first and cannot be skipped
  this way, because the flow cannot proceed without them.
- Writes user-level `config.yaml` **preserving comments and key order**, reusing the launcher-config YAML-node machinery
  built for migration.
- Answered/dismissed state is remembered so the wizard never nags.

Credential handling remains a **flow**, not a config question, and runs after the wizard establishes the agent and
network posture because migration output feeds the network policy.

## `config harden`

A standalone, re-runnable audit over the *effective* configuration. Separate concern from the wizard; shares the
comment-preserving config writer and validation. Each finding carries an optional remediation, applied step-by-step and
interactively; declines are remembered.

Representative findings:

- `network.profile: public` or over-broad `egress-allow` → offer to reduce to the required hosts.
- `provision-host-config: true` → offer the safe `config migrate` path.
- Native host config present with managed config absent → offer migration.
- Secret with `AllowAnyHostDangerous` or over-broad `hosts` → offer narrowing.
- `env.secret.yaml` mode not `0600` → offer fix.

`config harden` is also the home for "reduce egress later", so the public posture is always reversible through a
one-time, non-nagging prompt rather than automatic.

## Secondary fixes (bundled)

- **Network-deny conflict** during migration becomes an interactive resolution (skip that host / remove the deny / choose
  public / abort) rather than a hard error that aborts the whole migration.
- **Deny-conflict discovery is corrected.** Today the check (`updateLauncherNetwork`) only inspects the launcher config
  file's `egress-deny` plus the project config's deny, and matches by exact string or `"*"`. It must instead:
  1. resolve the **effective** network policy through the normal resolver (so env vars, `--network`, and the profile are
     considered, not just the file); and
  2. match derived hosts against deny entries using msb's destination semantics — host, CIDR, `.suffix`, and `*` — so a
     suffix/CIDR deny that would shadow a new `egress-allow` (deny is emitted before allow) is detected rather than
     silently defeating the migration.
  A host that survives must actually be reachable; a shadowed host is surfaced to the user, never silently added.
- **Dismissal** is explicit, remembered, and re-armed by source-hash change; `config migrate` and `config wizard`
  re-enter voluntarily. In particular, explicit `config migrate` on a login-only native configuration records
  `login-pending` and directs the user to authenticate in the sandbox, rather than reporting nothing to migrate.

## Relationship to PR #103

Retained: the `configmigration` package, plan/review/apply split, manifest and interrupted-apply resume, placeholder +
`env.secret.yaml` extraction, endpoint/known-provider host derivation, comment-preserving launcher-config writer, and
the CLI/behavior test structure.

Changed:

- OAuth (`type: "oauth"`) is no longer redacted/migrated; it becomes a `Login` item routed to in-sandbox login.
- `Guide` no longer silently returns when there is nothing to migrate; every branch is explicit and surfaces warnings.
- Network posture and host determination become an informed, reversible choice rather than a fallback.
- The first-run flow grows a tiered setup wizard and a companion `config harden` command.

## Implementation staging

This spec is delivered in four sequential plans; each produces working software on its own:

- **Plan A — Credential kinds, explicit `Guide`, network-deny handling.** Credential-kind model, always-explicit
  `Guide`, effective-policy deny detection/resolution, plus the necessary refactorings (shared credential metadata,
  dead-code/duplication removal, file decomposition). Plan document: `.operator-shared/plans/2026-10-07-guided-onboarding-a-credential-kind.md`.
- **Plan B — Onboarding state machine** (see "Onboarding state machine").
- **Plan C — Tiered setup wizard** (see "Tiered setup wizard"). Plan document:
  `.operator-shared/plans/2026-10-08-guided-onboarding-c-tiered-wizard.md`.
- **Plan D — `config harden`** (see "`config harden`").

To resume: read the spec section named for the plan, then expand it into the same TDD-shaped plan as Plan A.

## Testing strategy

Behavior-focused tests per terminal `Guide` outcome, per credential kind, and per state transition, including:
OAuth-only native config produces a login hand-off (not a silent no-op) and no generated auth file; static creds still
migrate; `login-pending` verification loop across runs; non-interactive warn-and-continue; network-deny interactive
resolution; wizard answering/dismissal and comment preservation; `config harden` findings and remediation idempotency.
CLI command/flag changes covered in `cmd/agents-sandbox/cli_*_test.go`.

## Deferred / open

- Auto-confirming in-sandbox login by statting the VM home (enhancement only).
- Whether `LoginHosts` needs a richer per-provider structure than a host list (e.g. device-flow hosts) after
  real-provider testing.
