---
description: The learn-mode proposal and applier - the finding shape (evidence, proposed write, scope), the per-slug user scope and the project scope behind the trust gate, why the managed layer never owns the credential store, the agent-config write-back and its `${ENVVAR}` (or per-harness native) placeholder convention, how a learned static secret is attributed before it is scoped, the network diff that never widens, the interactive per-finding apply and its decline memory, non-interactive behavior, `config harden` as the config-only subset with its settled finding set C1–C7, and how the after-run is owned.
read_if: Working on the learn-mode proposal, findings, or applier - the finding shape or evidence, the per-slug user scope or the project scope and its trust gate, the credential-store copy-back rule, the agent-config write-back or its placeholder convention, static-secret attribution or the network diff, per-finding apply and decline memory, non-interactive or dry-run behavior, `config harden` or its finding set C1–C7 (the owner-scope rule, the file-mode write, the generic-scope skip, the deferred session-evidenced reduction), or which process owns the after-run.
---

# Learn mode — findings and apply

The analyser emits **findings**, each carrying the evidence, the proposed write, and the scope it would write to.
One finding shape serves both a learning session and config-only analysis (`config harden`).

## The finding shape

- **Evidence is part of the finding.** A proposed `egress-allow` entry names the destination, how often it was used,
  whether it succeeded, and which channel saw it. A proposed secret names the provider and the field, never the
  value.
- **Scope** is the write's target (see [Scopes](#scopes)).
- **A finding declares whether it needs configuration or observation.** `config harden` runs the former; the
  after-run analyser runs both.

## Scopes

- **The per-slug user scope** (`~/.config/agents-sandbox/<repo-slug>/`) is the default for the learn-mode proposal,
  because it is a trusted user layer and needs no approval gate. It is keyed by the **repo slug**, so it is shared
  by every clone of the remote — the proposal must state that.
- **The project scope** is written only by `config harden`, through the project trust gate. Learn mode's proposal
  never targets it: config precedence is `generic user < per-slug user < project`, so a per-slug write is an
  ineffective no-op where the project layer owns the key, and invariant 4 forbids the proposal overriding a
  user-set project entry. When the project layer owns a proposed key, the applier reports the finding and skips
  the write.
- **A per-slug write is YAML-only.** The applier edits a config file format-preservingly by rewriting its document
  tree, so a per-slug config in JSON, JSONC, JSON5, or TOML is reported and skipped rather than converted to YAML.
- **A harden move writes two scopes.** A static secret wrongly committed in the project scope is removed from the
  project config and added at the per-slug user scope; the removal is harden's "reduce" contract, which the
  proposal never invokes. Both writes are trust-gated and applied together (or neither).

## The credential store

**The managed layer never owns the credential store.** `reprovision.Provision` writes merged and mirror files
unconditionally on every start, so a managed file that carries a login entry the agent refreshed in the sandbox
would destroy it. The copy-back therefore never targets a file the agent refreshes, and never adds a `home:` or
provision rule for the credential store. Static secrets go to `env.secret.yaml` and appear as placeholders in the
managed agent config; login entries stay only in the persistent sandbox home.

## The agent-config write-back

The after-run copies an agent config file the session authored into the per-slug user scope's agent config
directory as a managed snippet, so the launcher owns it on the next run. Each **static secret value** in the copied
content is replaced by its **placeholder**, which is also recorded on the secret's `env.secret.yaml` entry, so the
runtime substitutes it in flight for the allowed hosts; the value never appears in the finding, the artefact, or a
log. Only a config file the agent authored is copied — never a login store or a credential probe's database.

The placeholder convention is `${<ENVVAR>}` **where the harness passes a config value through verbatim**: a literal
config string is not shell-expanded, so the placeholder reaches the request. A harness that does not expand `${VAR}`
in its config needs its **native env indirection** instead — kimi reads no key from the process environment and only
accepts `api_key_env = "<ENVVAR>"`, so the write-back emits that form, and msb puts the placeholder into the named
env var. Which form a harness needs is a per-harness property of the write-back, not of the finding.

## Attribution and the network diff

- **A learned static secret is attributed before it is scoped, and a secret that cannot be attributed is not
  proposed at all.** Its host set comes from the destinations the session was observed sending that credential to
  (channel attribution, matched to the credential's configured env-var name); with no channel attribution (a client
  pins certificates, a non-HTTP protocol) it falls back to the endpoint the same config entry names (the read-back
  `Credential.Hosts`). It is proposed once per env-var name: when the read-back finds the same name in more than one
  source, the proposal scopes it to the union of their hosts. When neither yields a host the secret is not migrated —
  no finding is emitted, and the proposal asks nothing at apply time. A static credential with no configured env-var
  name cannot be scoped and is likewise not proposed. A learned value never produces `allow_any_host_dangerous`.
- **Network changes are proposed as a diff**: `network.profile = none` plus `network.egress-allow` set to the
  current allow set **∪** the observed-and-used destinations, where observed-and-used means a destination the
  session reached successfully and that was not denied. It never widens: no current allow entry is removed and no
  denied destination enters the allow set. Denied destinations are reported separately and never auto-allowed.
- The applied network policy is creation-only, so it takes effect at the next VM creation through the existing
  recreate path; the proposal states that.

## Applying

- A learning session presents its whole proposal behind one prompt: **Review and save**, **Save all**, or **Not now**.
  Review and save drives the per-finding prompt below; Save all applies every finding without asking (an explicit
  top-level accept-all, never `--yes`); Not now writes nothing and records nothing.
- Applying is interactive and per finding, and a finding's writes each name their own scope and are applied together
  (a move applies both writes or neither). A decline is remembered, keyed on a hash of the evidence, and re-armed
  when the evidence changes. The record lives in the user state dir, keyed by repo slug, so it survives a config-dir
  reset and is shared by clones.
- A non-interactive run writes no proposal and prints one line naming the command that produces it. A dry run
  records nothing and proposes nothing.

## After-run ownership

The **invocation that started the learning session owns the after-run**: `run --learn` performs it at teardown, in
that process, with that terminal. It is not hooked to VM detach, the reaper, or the idle timeout, because a VM is
one-per-checkout with several attachable clients and the last-detach path runs with an often-cancelled context.

If the owning process is gone (killed harness, closed terminal), the session record stays in state and
`config learn` performs the same analysis later. A recorded session whose home volume has since been recreated is
reported and discarded, not analysed.

The record lives in the per-`(checkout, agent)` home state, holding the session's start time, the home volume it was
taken against, the capture's path, and the baseline manifest — paths and a digest, never a value (invariant 3). The
after-run is owned by the process that started the session, so it runs before the record is cleared and the capture
deleted; `config learn`, which resolves the same record for the current checkout and agent, is the replay for a process
that is gone.

## `config harden`

Harden is the analyser with no session: the same findings engine, the same applier, the config-only subset of
findings, and it is the home of "reduce egress later". It is re-runnable and audits the effective configuration —
the `viperconfig.Config`, the ordered scopes, and each scope's `env.secret.yaml`. It runs the findings that need
configuration; the after-run analyser runs both configuration and observation findings. It is the only writer of the
project scope, and the only home of a removal: moving a static secret committed there to the per-slug user secret is
a harden finding (a project removal plus a per-slug addition). Removing a secret from a file does not remove it from
git history — harden says so and points at rotating the credential.

**The finding set (settled).** Each finding is one of:

- **C1 — a static secret in the project scope.** One finding per committed key, with two writes: a project-scope
  removal and a per-slug addition carrying the same hosts and header fields. A project secret is moved whole; it is
  never also narrowed in place, which would let the narrowing rewrite re-create the entry the move removed.
- **C2 — an over-broad secret scope.** A secret with `allow_any_host_dangerous` and at least one host is rewritten in
  place without the flag. The applier's secret writer always writes `AllowAnyHostDangerous: false`, so the rewrite is
  the drop. C2 fires in the per-slug user scope, outside the project scope where C1 moves the secret whole. Two cases
  are reported, not written: a dangerous secret with **no** host has nothing to narrow to, and a dangerous secret in
  the **generic** user scope has no in-place target — its only write target is the per-slug scope, and moving a
  lower-precedence value up could overwrite the per-slug entry the user set for that repo.
- **C3 — an over-broad egress profile.** A policy that is not the terminal `none` profile is narrowed to `none`,
  keeping the current allow list. An **empty** allow list is reported, not written: narrowing to `none` with nothing
  to keep would deny all egress. An empty *entry* is not a reachable host — the network spec drops it (`network`'s
  `dedupe`) — so a list whose only entries are empty is reported like an empty one. An allow list whose only entries are
  wildcards C4 would drop is reported too, for the same reason. A default `public` (or empty-allow) config therefore
  fires C3 as a report, so `config harden` is never silent on a default config.
- **C4 — a wildcard egress entry.** An `egress-allow` entry `*`, `0.0.0.0/0`, or `::/0` is dropped, keeping the
  remaining entries. A list whose every entry is a wildcard (or empty) has nothing to keep, so C4 reports it rather than
  dropping the last entry. C3 and C4 therefore never combine to leave `none` with an empty allow list, which would deny
  all egress.
- **C5 — a secret file mode.** Each scope's `env.secret.yaml` whose mode is not `0600` is tightened by a file-mode
  write; a scope with no secret file yields no finding, and one is never created to chmod it.
- **C6 — host-config provisioning on.** `provision-host-config: true` is turned off, its text pointing at
  `run --learn` to derive a managed config first (see [onboarding](../onboarding.md) §`config harden`).
- **C7 — a static secret inline in a managed agent config.** A credential-bearing key inside a provider entry is
  moved to `env.secret.yaml`, using the read-back classification ([home
  read-back](home-read-back.md) fact 2): the provider, hosts, and env-var name come from the entry that holds the key,
  never a guessed host. The value is read in memory only to re-write it. The move target is the per-slug user scope, so
  an inline credential whose env var the per-slug `env.secret.yaml` already declares is reported, not moved: the write
  would replace the value and host scope the user set there (the same reason C2 reports a generic-scope dangerous
  secret, whose only target is likewise the per-slug scope).

**A write targets the key's owner.** A launcher-config key is written at the project scope when the project layer
owns it — `viperconfig.LayerSettingOwner` names the highest-precedence file layer that sets a key — and at the
per-slug user scope otherwise. A narrowing written at the per-slug scope where the project owns the key would be
shadowed, so the owner rule is what makes the change take effect. A project-scope write goes through the trust gate
and is reported, never forced when refused.

**C7's reference write is declarative for now.** C7 emits the secret write (the move to `env.secret.yaml`) plus a
text-only write naming the reference the config should keep: always the `${<ENVVAR>}` placeholder, since the finding is
harness-agnostic and the declarative write edits no config. Choosing the reference form — including a harness's native
`api_key_env = "<ENVVAR>"` where it does not expand `${VAR}` (kimi) — is a property of the write-back, not the finding.
The actual agent-config edit lands with the S10 managed-config write-back; the finding shape is shared, but the edit and
the native form are S10's.

**The generic user scope is not a file-mode target (R8).** C5 skips `ScopeGenericUser`: the applier's file-mode target
is only the per-slug user or project scope, and a file mode cannot be shadowed, so a generic-scope finding would
tighten the wrong file or be skipped forever. This is a deliberate narrowing of C5's "each scope".

**The session-evidenced network reduction is deferred.** Harden's network findings are structural only (a broad
profile, a wildcard entry) — no provider table, no session evidence. Offering to reduce egress to the hosts a session
actually used needs the S7 artefact store and lands later; until then harden never derives an allow set from
observation.
