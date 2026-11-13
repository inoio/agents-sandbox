---
description: Contract for guiding a fresh install to a working sandbox - safe config migration (static-vs-login credential kinds, host determination, the derived-state machine, the `Guide` outcomes), the tiered `config wizard`, `config harden`, and the doctor-remediation + Get started docs flow.
read_if: Working on `config migrate`/`config wizard`/`config harden`, first-run guidance, credential/auth handling, network egress derivation, the `Guide` outcomes and their warning/`HasChanges` gating, the doctor output and its troubleshooting anchors, or the Get started docs (`installation.md`, `tutorial.md`, README quick start).
---

# Onboarding

The secure default `provision-host-config: false` removed raw host credentials from the VM but also removed the convenience of "it just works". The first-run experience guides a user from a host machine with existing agent configuration to a working, reviewable sandbox configuration without ever copying raw host credentials into the VM. (The migration/wizard machinery is a design, not yet implemented; the doctor and docs guidance is landed.)

## Principles

1. **Prefer static secrets over interactive login.** Where a provider offers an API key or bearer token, migrate it to a microsandbox secret (placeholder in the VM, raw value on the host, host-scoped substitution) — the most secure and most deterministic path.
2. **Never placeholder-migrate refreshable OAuth.** An OAuth access/refresh token is refreshed by the agent and rewritten inside the VM, so treating it as a static secret either materializes real tokens in the VM or depends on fragile proxy-mediated refresh.
3. **Host-first onboarding is the documented story.** A new user sets the agent up on the host; agents-sandbox detects that config and migrates it. The wizard makes this explicit; it does not silently do nothing when config is missing or unmigratable.
4. **Public egress by default, least privilege as an informed choice.** The default network profile is `public` (first run just works). The wizard treats least privilege (`none` plus the derived required hosts) as the recommended posture, and agents-sandbox never silently broadens or narrows a user's chosen policy.
5. **Always explicit outcomes.** Every interactive first run ends in a named outcome (migrated, login hand-off, host-first hand-off, review hand-off, manual, dismissed). No path returns silently leaving the user with an unconfigured sandbox.
6. **Guide, don't block.** Non-interactive runs warn and continue; they never hang or fail on onboarding.

## Credential kinds

Every native auth entry is classified by one concept, independent of agent:

- **`Static`** — an inline, non-refreshable secret that can be replaced by a placeholder: API keys (`type: "api"` / `"api_key"`), well-known tokens (`type: "wellknown"`), bearer tokens. **Migrate**: placeholder + `env.secret.yaml` + egress-allow host.
- **`Login`** — refreshable OAuth (`type: "oauth"`) or machine-bound login state agents-sandbox cannot read (Claude Code's `.credentials.json`, `~/.claude.json`, or OS keychain). **Do not copy**: drop the entry from generated files, allow the login hosts, and let the user authenticate inside the persistent sandbox home.

OAuth detection is generic: the auth entry's `type` equals the agent's declared login auth type (default `"oauth"`). An agent declares its credential shape on `ConfigMigrationSpec`:

- `LoginAuthTypes []string` (default `["oauth"]`).
- `LoginHosts map[string][]string` — per-provider hosts needed for the login flow, defaulting to `KnownProviderHosts` where a provider's login and API endpoint share a host.

Claude Code is not a special case by design; it is simply the agent whose credential is always `Login` (it declares no `NativeCredential` and pre-allows its login hosts via `RequiredNetworkHosts`). When native config uses `Login` but the provider also offers an API key, the wizard recommends switching to the key path (host-side secret) while still allowing the login fallback.

## Host determination

Two independent lists, never conflated:

- **Network egress hosts** (`network.profile` + `egress-allow`/`deny`): where the VM may connect at all.
- **Secret substitution hosts** (each secret's `hosts`): where the proxy may replace a placeholder with the real value.

Resolution for **network** hosts depends on the chosen posture: least privilege → derive from custom endpoints, then the curated known-provider table, then ask the user; public → no per-host enumeration, set the broad policy and stop deriving.

Resolution for **secret substitution** hosts is required for every migrated `Static` credential, even under a public network posture, unless the user explicitly chooses `AllowAnyHostDangerous`: custom endpoint in the native config (`baseURL` / `endpoint` / `url`) → its host; known provider → curated table; otherwise → **ask the user**.

Login hosts for the `Login` fallback resolve as: agent-declared per-provider table (suffixes permitted), then ask, then degrade to a warned explicit option (documented public profile or manual `egress-allow`). An unresolved host is never a dead end.

## Onboarding state machine

**Derived state is authoritative**; only user decisions and the apply journal are persisted, so external file changes are always observed. State is keyed by the native-configuration source hash, so any native file change re-arms the entire flow.

Persisted in the per-agent manifest `~/.local/state/agents-sandbox/<agent>/config-migration.yaml`: `state` (last terminal decision: `completed`, `dismissed`, `manual`, `login-pending`), `outputs` (apply journal for interrupted-apply resume), and `pending` (login-required providers and their allowed hosts).

Derived each run by `Guide`:

| Native config | Managed config | Decision | Outcome |
|---|---|---|---|
| none | none | — | host-first hand-off |
| none | present | — | completed (nothing to do) |
| present | none | static/settings | migrate (→ `login-pending` if OAuth also present) |
| present | none | OAuth only | login hand-off |
| present | none | unsupported/malformed | review hand-off |
| any | any | dismissed, manual | quiet until source hash changes |

`login-pending` is a **verification loop, not terminal**: on the next interactive start, ask whether the in-sandbox login worked. `Yes` → `completed`. `No` → re-enter host review and keep `login-pending`. `Not yet` → quiet reminder next time. Non-interactive runs print a one-line reminder and never prompt.

The verification question is asked **before** the managed-config quiet gate, so a mixed static+OAuth config — which writes files yet still records `login-pending` — reaches the loop on the next run instead of being silenced by the config it just wrote. A pending login is never auto-completed because a login host later became unreachable or deny-shadowed; only the verification answer (or the optional credential detection) does.

`manual` is as quiet as `dismissed` until the source hash changes. It is recorded by the host-first, review, and login hand-offs when the user picks "configure manually" or, where offered, "start anyway"; the host-first "set up on the host" choice instead defers the run without recording a decision. An explicit `config migrate` still proceeds over a recorded `manual`, `login-pending`, or `dismissed` state for the current native configuration, noting the re-entry.

Optional enhancement (not a dependency): if a stat of the persistent VM home detects the agent's credential file, auto-confirm the login and skip the question.

## `Guide` outcomes

`Guide` is a decision tree whose every interactive branch terminates in an explicit outcome:

- `provision-host-config: true` → skip entirely (explicit unsafe opt-in).
- Managed config present → completed; optionally suggest `config harden`.
- Native config with migratable content → **Migrated**: `Review` shows generated files, placeholders, login-required providers, and warnings; `Apply` writes; then confirm start. `login-pending` providers get login guidance.
- Native config with OAuth only → **Login hand-off**: explain why it is not copied, allow hosts, record `login-pending`. Warnings are surfaced; never a silent no-op.
- Native config, unsupported/malformed only → **Review hand-off**: surface every warning ("review required", "skipped malformed", "found but not copied"); offer host-first, manual config, or start-anyway.
- No native config → **Host-first hand-off**: "set up `<agent>` on the host, then rerun; agents-sandbox will migrate it", plus manual-config docs and start-anyway.

Warnings and review-required items are surfaced **before** any "has changes" gate, so a plan with nothing to write still communicates its findings. This guards against a `HasChanges()` short-circuit skipping `Guide` and `applyAuthMigration` returning before it appends warnings when the generated auth is empty.

Non-interactive runs: warn (native config found but not migrated, etc.) and continue.

## Tiered setup wizard

A declarative, extensible catalog of `SetupQuestion`s (key path, tier, prompt, default, validation). Every question has a default; accepting defaults is one keystroke.

- Tier membership (fixed; the catalog stays re-tierable without structural change — the concrete catalog lives in `configwizard`): **T1 essentials** — `agent` (only when unset) and `network.profile`; **T2 resources** — `cpus`, `memory`, `disk-size`, `workspace-quota`, collapsed behind one "resource defaults ok?" confirmation; **T3 runtime** — `dind`, `tmp-size`, `log-level`; **T4 integrations** — `notify.*`, `upgrade.*`, `auto-prune-age`.
- First run asks T1–T3. Deeper tiers are opt-in later via `config wizard --tier=N`; `config wizard` with no flag asks all unanswered tiers; a `--tier` outside 0–4 is rejected. Only the first run or the no-flag full wizard marks the first run done; an explicit `config wizard --tier=1` must not suppress the later first-run tiers.
- The wizard runs as a first-run step **before** credential migration, then run options are re-resolved so migration sees the chosen agent and network posture.
- A value already set at a more specific scope (project config / per-slug user config / env / flag, reported by `viperconfig.Resolver.KeySetAboveUserConfig`) is never overwritten; the question is skipped and this is reported.
- Accepting a default is a no-op for an existing value: the offered default is the value already set in the user config when present (for choice questions only when it is one of that question's choice keys), otherwise the catalog default. A value is written only when it differs from the current user-config value, so pressing Enter through an existing config leaves the file untouched and reports zero writes.
- A group of related value questions may be collapsed behind one confirmation: e.g. a single "resource defaults ok?" accepts `cpus`/`memory`/`disk-size`/`workspace-quota` together, and only expands them into individual questions when declined. A group confirm applies its members' defaults through the same already-set/higher-scope skip rules.
- Network posture is asked at every first run (the one question always relevant to credential handling), unless a more specific scope already fixes it.
- **User control / early exit:** every non-essential question offers an "enough questions — use defaults for the rest" option that stops the interview immediately and applies defaults to all remaining questions (recorded, so it does not re-ask). Essential questions — agent selection when unset, and network posture — are asked first and cannot be skipped this way.
- Writes the user-level launcher config — whichever of `config.{yaml,yml,json,jsonc,json5}` exists, else `config.yaml` — through the shared `internal/configfile` editor: comments and key order are preserved in YAML, and JSON-family files round-trip as valid JSON.
- Answered state is remembered in `<UserStateDir>/config-wizard.yaml` (`version`, `first_run_done`, `asked`, mode `0600`) so the wizard never nags. The wizard is a no-op in a non-interactive run.

Credential handling remains a **flow**, not a config question, and runs after the wizard establishes the agent and network posture because migration output feeds the network policy.

## `config harden`

A standalone, re-runnable audit over the *effective* configuration; separate from the wizard but sharing the comment-preserving config writer and validation. Each finding carries an optional remediation, applied step-by-step and interactively; declines are remembered. Representative findings:

- `network.profile: public` or over-broad `egress-allow` → offer to reduce to the required hosts.
- `provision-host-config: true` → offer the safe `config migrate` path.
- Native host config present with managed config absent → offer migration.
- Secret with `AllowAnyHostDangerous` or over-broad `hosts` → offer narrowing.
- `env.secret.yaml` mode not `0600` → offer fix.

`config harden` is also the home for "reduce egress later", so the public posture is always reversible through a one-time, non-nagging prompt rather than automatic.

## Doctor remediation and Get started docs

The prerequisite checks run automatically as a preflight on `run` and `shell` (`build` checks only the Docker prerequisite), so starting a sandbox does not require invoking `agents-sandbox doctor` by hand. The `doctor` command is the standalone diagnostic and the fresh-install verification; it deliberately does not go through that runtime preflight. `doctor` evaluates the runtime as one of its own checks, so a failed runtime install is reported as `msb runtime setup failed` with its troubleshooting link alongside the KVM and Docker results rather than aborting before they run.

Each failing check reports, in order:

1. a one-line diagnosis with a stable, searchable prefix (`/dev/kvm not found`, `container engine (Docker API) unreachable`, `msb runtime setup failed`);
2. the copy-pasteable command that fixes it, where one exists — platform-specific through the `doctor_linux.go` / `doctor_darwin.go` build-tagged pair (`dockerRemedyHint`, `kvmUnavailableError`, `appleSiliconRequiredError`);
3. a `Docs:` line linking `https://inoio.github.io/agents-sandbox/troubleshooting.html#<anchor>` for what a command cannot fix.

`docslink.SectionURL("troubleshooting", …)` builds the link. The anchors are the troubleshooting headings (and the `<a id="…"></a>` alias a rename leaves behind), and `ci/check-docs.sh` fails when a printed fragment matches no heading or explicit anchor (see `../ci/quality-gates.md`). Current pairs: `kvm-unavailable`, `container-engine-not-running`, `microsandbox-runtime`, `not-apple-silicon`.

There is no separate prerequisites page. `docs/install/installation.md` is the only home for:

- **Prerequisites** (before the install methods) — leads with the reassurance that agents-sandbox verifies the host automatically before a sandbox starts, then the supported platforms (Linux with KVM; macOS on Apple Silicon) and the container-engine requirement, and names the standalone `agents-sandbox doctor` run as an optional check available once installed.

Installation ends with the next step spelled out — run your first agent (`tutorial.md`) — and routes readers who already use an agent to `switch.md` and readers who want a self-contained setup to `manage-config.md`. The Get started page lists its four child pages as a labelled sequence (Installation → Your first agent → Switch → Manage config), and the tutorial is titled "Your first agent" (filename unchanged, so its URL is stable).

Pages about running agents-sandbox — the tutorial and the README quick start — do not tell the reader to run `doctor`, because the preflight already runs it. Only the Installation prerequisites section and the troubleshooting page name it by command.

`docs/troubleshooting.md` owns the fix prose; the doctor messages stay terse and defer to it. The Docker prerequisite is stated as any container engine that serves the Docker API, in three tiers: the tested engines named (Docker Engine, Docker Desktop, colima, podman), other engines as an open, untested list, and engines with no Docker API as unsupported. An engine that refuses an unqualified `FROM` — podman without search registries — is documented as its own build-failure subsection with the engine-side remedies.

## Deferred / open

- Auto-confirming in-sandbox login by statting the VM home (enhancement only).
- Whether `LoginHosts` needs a richer per-provider structure than a host list (e.g. device-flow hosts) after real-provider testing.
