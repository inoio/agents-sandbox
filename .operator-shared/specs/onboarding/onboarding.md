---
description: Contract for the first-run onboarding flow — the three first-run choices (`internal/firstrun`), their decision record in `RootState`, the static-vs-login credential kinds, `config harden`, and the doctor-remediation + Get started docs flow; the declaration-model `config migrate` and the tiered `config wizard` are retired in favour of learn mode.
read_if: Working on the first-run `internal/firstrun` menu (`Offer`/`Prompt`/`BuildFacts`/`SeedFromHost`) or its `RootState` decision record, `provision-host-config`, `config harden`, first-run guidance, credential/auth handling, the doctor output and its troubleshooting anchors, or the Get started docs (`installation.md`, `tutorial.md`, README quick start).
---

# Onboarding

The secure default `provision-host-config: false` removes raw host credentials from the VM, at the cost of the
"it just works" convenience of the old drop-in copy. The first-run flow guides a user from a host machine with existing
agent configuration to a working, reviewable sandbox configuration without ever copying raw host credentials into the
VM. It is landed: `internal/firstrun` decides and drives the menu, and a learning choice hands off to
[learn mode](learn-mode.md). The declaration-model migration/wizard this replaces never landed on `main` (see
[Superseded design](#superseded-design)).

## Principles

1. **Prefer static secrets over interactive login.** Where a provider offers an API key or bearer token, the learned
   path turns it into a microsandbox secret (placeholder in the VM, raw value on the host, host-scoped substitution) —
   the most secure and most deterministic path.
2. **Never placeholder-migrate refreshable OAuth.** An OAuth access/refresh token is refreshed by the agent and rewritten
   inside the VM, so treating it as a static secret either materializes real tokens in the VM or depends on fragile
   proxy-mediated refresh. A login stays in the persistent sandbox home.
3. **Learning in the sandbox is the documented story.** A new user is offered three first-run choices: learn by using
   it, learn from the host config when one exists, or configure it myself ([learn mode](learn-mode.md)). The host-first
   route seeds a learning session from the host config; it derives managed config rather than declaring it, and it never
   silently does nothing when config is missing or unclassifiable.
4. **Public egress by default, least privilege as an informed choice.** The default network profile is `public` (first
   run just works). Learn mode treats least privilege (`none` plus the hosts a real session reached) as the recommended
   posture, and agents-sandbox never silently broadens or narrows a user's chosen policy.
5. **Always explicit outcomes.** Every interactive first run ends in a named, recorded outcome
   (`learned` / `manual`), or offers nothing. No path returns silently leaving the user with an unconfigured sandbox.
6. **Guide, don't block.** Non-interactive runs warn and continue; they never hang or fail on onboarding.

## First-run flow

`internal/firstrun` owns the decision, the host-config probe, the menu, and the UC4 seed.

**Offer predicate (`Offer`).** The menu is offered when no managed config layer exists for the agent **and** either no
decision is recorded or the recorded host-config source hash no longer matches the current one. A decided run whose host
config is unchanged stays quiet; a host-config change re-arms it exactly once (the next decision records the new hash).
`BuildFacts` assembles the facts: the agent, the repo slug, whether a managed config layer exists, the host config files
the seed would read, and their source hash. `Facts` holds no credential value — it lists paths and hashes their contents.

**The menu (`Prompt`, on `termio.Prompter.Select`).** One consent sentence, then the choices:

- **Learn by using it** — the recommended choice when the host has no agent config; starts a learning session with no
  host seed (UC1).
- **Learn from my host setup** — offered only when the host has agent config, and recommended in that case; seeds the
  per-slug scope from the host config, then starts the same learning session (UC4).
- **Configure it myself** — configures by hand. It is the only choice that turns on host-config provisioning
  (`provision-host-config: true`), and only after an explicit confirmation warning that provisioning copies host
  credentials into the sandbox.

**Non-interactive.** A non-interactive run warns that first-run setup needs an interactive terminal, offers nothing, and
continues (principle 6).

**The decision record.** `Outcome.Apply` records `state.FirstRunRecord` (`choice`, `source_hash`, `at`) in `RootState`
under the key `<repo slug>/<agent>`, so every clone of one remote shares the decision, matching the per-slug proposal
scope. A learning choice records `learned` and leaves the config-resolved provisioning setting untouched (a learning
session applies nothing); the manual choice records `manual` and carries the warning's opt-in onto the run options. No
separate onboarding state file exists.

## Credential kinds

Every credentialed entry is classified by one concept, independent of agent:

- **`Static`** — an inline, non-refreshable secret that can be replaced by a placeholder: an API key or bearer token.
  The learned path placeholders it (placeholder in the managed config, value in `env.secret.yaml`, host-scoped
  substitution).
- **`Login`** — refreshable OAuth or machine-bound login state agents-sandbox cannot read (Claude Code's
  `.credentials.json` / `~/.claude.json`, an OS keychain). It is never copied: the login stays in the persistent sandbox
  home and the user authenticates inside it.

The classification lives in the read-back, shared by the UC4 seed (`readback.ClassifyConfigContent`) and the after-run
analyser (`readback.Read`); its shapes, the env-var name that scopes a static secret, and the static-inline-key /
directory-login cases are in [home read-back](learn-mode/home-read-back.md). The UC4 seed reads the host config through
the agent's existing provision rules, classifies each credential, writes a placeholdered copy of each config file the
agent's snippet pattern merges to the per-slug managed config and each static value to the per-slug `env.secret.yaml`,
skips (and reports) a config file the merge would never read, and never copies a login store (and
refuses a file that still holds an unclassified secret value).

## `config harden`

A standalone, re-runnable audit over the *effective* configuration; separate from the first run but sharing the
comment-preserving config writer, the validation, and the applier. It writes nothing non-interactively or under
`--dry-run`. Each finding carries an optional remediation, applied step-by-step and interactively; declines are
remembered, keyed on a hash of the evidence. The shipped finding set is C1–C7 in
[learn mode findings and apply](learn-mode/findings-and-apply.md#config-harden). Representative findings:

- a network profile that is not the terminal `none`, or an over-broad `egress-allow` list → narrow the posture,
  keeping the current allow list (a broad profile with an empty allow list is reported, not written).
- `provision-host-config: true` → turn it off, after deriving a managed config with `run --learn`
  ([learn mode](learn-mode.md) supersedes the old host-first migration path).
- a static secret committed in the project scope → move it to the per-slug user scope (a project removal plus a
  per-slug addition).
- a secret with `allow_any_host_dangerous` → drop the flag, keeping its declared hosts.
- an `env.secret.yaml` mode not `0600` → tighten it (the generic user scope is skipped).
- a static secret left inline in a managed agent config → move it to `env.secret.yaml`; the finding names the
  reference to keep in the config, and the agent-config edit lands with S10.

`config harden` is also the home for "reduce egress later", so the public posture is always reversible through a
one-time, non-nagging prompt rather than automatic. Its network findings are structural only for now; deriving the
allow set from a real session's traffic is deferred to learn mode's observation evidence.

## Doctor remediation and Get started docs

The prerequisite checks run automatically as a preflight on `run` and `shell` (`build` checks only the Docker prerequisite), so starting a sandbox does not require invoking `agents-sandbox doctor` by hand. The `doctor` command is the standalone diagnostic and the fresh-install verification; it deliberately does not go through that runtime preflight. `doctor` evaluates the runtime as one of its own checks, so a failed runtime install is reported as `msb runtime setup failed` with its troubleshooting link alongside the KVM and Docker results rather than aborting before they run.

Each failing check reports, in order:

1. a one-line diagnosis with a stable, searchable prefix (`/dev/kvm not found`, `container engine (Docker API) unreachable`, `msb runtime setup failed`);
2. the copy-pasteable command that fixes it, where one exists — platform-specific through the `doctor_linux.go` / `doctor_darwin.go` build-tagged pair (`dockerRemedyHint`, `kvmUnavailableError`, `appleSiliconRequiredError`);
3. a `Docs:` line linking `https://inoio.github.io/agents-sandbox/troubleshooting.html#<anchor>` for what a command cannot fix.

`docslink.SectionURL("troubleshooting", …)` builds the link. The anchors are the troubleshooting headings (and the `<a id="…"></a>` alias a rename leaves behind), and `ci/check-docs.sh` fails when a printed fragment matches no heading or explicit anchor (see `../ci/quality-gates.md`). Current pairs: `kvm-unavailable`, `container-engine-not-running`, `microsandbox-runtime`, `not-apple-silicon`.

There is no separate prerequisites page. `docs/install/installation.md` is the only home for:

- **Prerequisites** (before the install methods) — leads with the reassurance that agents-sandbox verifies the host automatically before a sandbox starts, then the supported platforms (Linux with KVM; macOS on Apple Silicon) and the container-engine requirement, and names the standalone `agents-sandbox doctor` run as an optional check available once installed.

Installation ends with the next step spelled out — run your first agent (`tutorial.md`) — and routes readers who already use an agent to `switch.md` and readers who want a self-contained setup to `manage-config.md`. The Get started page lists its child pages as a labelled sequence (Installation → Your first agent → Switch → Manage config → Letting agents-sandbox learn your setup), and the tutorial is titled "Your first agent" (filename unchanged, so its URL is stable).

Pages about running agents-sandbox — the tutorial and the README quick start — do not tell the reader to run `doctor`, because the preflight already runs it. Only the Installation prerequisites section and the troubleshooting page name it by command. The tutorial and the README quick start state the first-run choices rather than the old assumption that the host config is copied.

`docs/troubleshooting.md` owns the fix prose; the doctor messages stay terse and defer to it. The Docker prerequisite is stated as any container engine that serves the Docker API, in three tiers: the tested engines named (Docker Engine, Docker Desktop, colima, podman), other engines as an open, untested list, and engines with no Docker API as unsupported. An engine that refuses an unqualified `FROM` — podman without search registries — is documented as its own build-failure subsection with the engine-side remedies.

## Superseded design

The declaration model was a first-run flow that copied (migrated) host config into a managed layer, driven by curated
knowledge: a `ConfigMigrationSpec` per agent (login auth types, per-provider login hosts), a curated known-provider host
table, a derived-state `Guide` decision tree, and a tiered `config wizard` (a T1–T4 `SetupQuestion` catalog). It never
landed on `main` — it existed only on the stale `issue-99-guided-config-setup` branch (PR #103). Learn mode supersedes
it: the user declares nothing, the launcher derives the configuration from one real session
([learn mode](learn-mode.md) §Intent), and the UC4 seed derives each host from the endpoint the config entry names,
never from a provider table. Its `config-migration.yaml` manifest is not recreated; the first-run decision lives in
`RootState`. The tiered `config wizard` is out of scope.

## Deferred / open

- Auto-confirming in-sandbox login by statting the VM home (enhancement only).
- Whether a per-provider login-host structure richer than a host list is ever needed after real-provider testing.
- The first-run end-to-end acceptance (a real KVM host) is the UC1/UC4 journey; it is the seed for the new-user-journey
  e2e (#252), not a harness specified here.
