---
description: Contract for the project config trust guard (#229): the project config layer is untrusted in its entirety, whatever the launcher consumes from it requires approval-on-change recorded per checkout, an unapproved change refuses to start non-interactively, and the prompt shows the resolved old → new effect and its source.
read_if: Working on project config trust (#229), the approve-on-change gate, config resolution in the root command's `PersistentPreRunE`, project-layer provenance, or `config harden`; or diagnosing a run that refuses to start, or a command that ignores the project config, because `.agents-sandbox` changed.
---

# Project config trust

## Why

The project-config directory — `<checkout>/.agents-sandbox`, or `<bare-root>/.agents-sandbox` in a bare
layout (`configpaths.ProjectConfigDir`) — is merged **above** the user config (`viperconfig.ConfigLayers`:
generic user dir, per-slug user dir, project dir, env, flags) and treated as fully trusted, while the
sandbox can write into that same tree through the writable `/workspace` bind. An agent that edits a file
there, or plants one in a repo, has the next host run act on it.

Some config keys reach host state directly: `mounts` exposes any host directory inside the VM
(`mounts.ResolveBindMounts` only checks that the source exists and is a directory), and `home` copies any
host file into the VM (`homeconfig` resolves the source as an absolute path or `~/…`). Both reach host
paths the user never granted — the whole filesystem, the host Docker socket, `~/.ssh`, the user's own
secret files. `network` (egress), `provision-host-config`, `docker`, and the project secret files widen
the same boundary.

Host-affecting keys are not the only exposure, so a key list is the wrong boundary. Everything under the
project config directory is agent-writable and the launcher acts on all of it: the agent config
directories provision files into the VM — snippets, and files mirrored verbatim such as plugins — and a
plugin or config directive can reach outside the sandbox. Trusting only the keys that happen to look
dangerous today leaves the rest as a live path; the **layer**, not a key list, is the trust boundary.

Guest-side file controls cannot enforce this trust decision: the deny-list (#12/#39) and ro-list (#536)
act on the VM's view of `/workspace`, but the launcher reads the project config **host-side**, and a
config already committed or planted before a policy applies still reaches the host run. The remedy is a
host-side gate on the project layer.

## Trust model

- Two layers, two trust levels. The **user layer** (the user's own config and secret files, authored on
  the host) is trusted and is never gated. The **project layer** (anything under the project-config
  directory, inside the guest-writable checkout) is untrusted in its entirety.
- Approval is **by content, per checkout**. The user approves an exact projection of the layer once; every
  later run whose projection matches is silent. When the projection changes, it is gated again.
- The gate is authority, not reconfiguration. It decides whether the project layer may contribute at all;
  it does **not** decide whether the VM must be recreated or restarted (`specs/vm/reconfig-decision.md`). The two
  are separate decisions and separate prompts.

## What is gated

The **whole project config layer** is gated — every input the launcher consumes from `<project-config>/**`:

- the launcher config file (`config.{yaml,yml,json,jsonc,json5}`) — every key it contributes, not a
  dangerous subset.
- the project env and secret files (`env`, `env.secret`, `env.secret.yaml`).
- the project agent config directories (`<project-config>/<agent-config-dir>/`) — the snippets that merge
  into the agent's VM config and the files mirrored verbatim (plugins, sub-configs, …).
- the project Dockerfile.
- the files the project layer's `home:` entries copy out of the project config directory — a relative
  source, startup hook scripts included. Their content is gated, not only the entry: a hook script runs in
  the VM, so changing it is as consequential as changing the entry. An absolute or `~/…` source is a host
  path the entry value already names; that value is gated as launcher config.

Rationale: the directory sits in the guest-writable checkout, so the agent can rewrite any of it; config
keys reach host state, and agent snippets configure the agent and its plugins, which run in the VM but can
reach beyond it. Gating the whole layer is closed — there is no per-key list to keep current and no
provisioned file to forget. The cost, that a benign project-layer edit re-prompts, is accepted, because
the layer is untrusted by construction.

Files the launcher does not consume (a stray `README`, say) are not gated; the layer's *consumed* inputs
are.

## Approval contract

- On each run that resolves the project layer, compute the **project trust projection**: for every input
  the layer contributes, its source path and its canonical effect. Canonicalize (sorted paths and entries)
  so reordering does not change the digest.
- Effect depends on the input kind:
  - **structured launcher config** → the resolved values of every key the project layer sets.
  - **secret files** → the substitution structure (env-var names, hosts, header fields, flags, the
    `secrets.common.allow-placeholder-for` passthrough), **not** the values. A value rotation does not
    re-prompt, and the prompt never prints a value from the secret files.
  - **file inputs** (the Dockerfile, agent snippets and mirrored files, project files copied by `home:`) →
    the file's content.
- The projection is the project layer's **own declared contribution**, not composed with the higher
  layers: a change to the user layer or a flag must not invalidate an approval, and a project value
  currently shadowed by a flag is still gated (approval is about what the project layer could impose, not
  this run's effective value).
- The projection carries each input's source path, so a rename or re-source counts as a change.
- Hash the projection and compare it with the digest recorded for the checkout.
  - **Match** → proceed silently.
  - **Empty projection** — a project layer that contributes nothing the launcher consumes — is silently
    allowed; there is nothing to approve.
  - **No recorded digest** with a non-empty projection (first run, a fresh clone, lost state) → treated as
    changed.
  - **Differ** → approval is required.
- Approval is interactive. The prompt names each changed input with its **resolved old → new effect and
  the source file** — resolved values for config and file inputs, structure for secret files (so no value
  from a secret file is shown; this is #230's requirement, owned here for project-sourced changes) — then
  asks to continue. The old side is diffed from the previously approved projection (State), so it is recoverable
  even though the project files have already changed. Approving records the new projection and its digest;
  declining aborts the run before any side effect and returns a cancellation the caller treats as a user
  decline.
- The approved projection and its digest are written through the `RootState` transaction
  (`state.WithRootState`, `../state/state-model.md`), preserving every field the transaction does not
  own. The projection is stored canonically. It holds no value from the project secret files (their effect
  is structure-only by construction), while launcher-config values and file inputs are stored in their
  resolved/content form — so it is safe at rest only to the same degree the project files themselves are; a
  literal secret a launcher-config value or a Dockerfile carries is stored as written. A file input is
  stored by content for a human diff; a large or binary input may be stored as a digest where a content
  diff is not useful.
- **A launcher-initiated edit to the project layer must update the approved projection.** When the
  launcher itself writes a project-config file — e.g. `resourceproposal.PersistSetting` raising `memory` or a disk limit
  (`specs/resources/resource-limit-proposal.md` §4) — it must record the resulting projection and digest in the same act,
  or the next run gates the launcher's own change. The write and the projection update are one operation;
  a write whose projection update fails must not leave the file changed without the matching approval.

## Non-interactive behavior

- When approval is required and no digest is recorded, a non-interactive run **refuses to start**. The
  error names the changed inputs and their sources and says how to resolve it (run once interactively to
  review and approve). "Non-interactive" is any UI without a TTY, including `--serve-only`.
- The generic `--yes` flag does **not** satisfy the gate. `--yes` authorizes destructive actions; it must
  never be read as authorizing untrusted project input. There is no non-interactive approval path in v1:
  the first approval of any given projection is a human act. A dedicated explicit opt-in may be added
  later only against a concrete headless need.
- A non-interactive run whose projection is already approved proceeds normally.

## Where the gate runs

- At **config resolution**: the root command's `PersistentPreRunE`, after the checkout identity is resolved
  and before `viperconfig.NewResolver` merges the layers and `applyCLISettings` applies them. The gate decides
  whether the project layer is part of this run at all; it is not a stage of `prepare.PrepareSandbox`.
  Several project keys act before `PrepareSandbox` runs — `yes` sets assume-yes for the whole process,
  `upgrade.*` drives the self-upgrade, `auto-prune-age` the startup prune, and `quiet`/`log-level` could hide
  the very prompt and warnings this contract relies on — so a gate inside `prepare` would come too late.
- **An unapproved project layer is not loaded.** Every consumer derives its project inputs from
  `configpaths.ConfigScopes` (`specs/configuration/config-scopes.md`), so leaving out the project scope removes the
  launcher config file, the env and secret files, the agent config directories, and the `home:` sources at
  once; the project Dockerfile, read outside the scopes, follows the same decision.
- **Who asks.** The commands that turn project input into VM or image state — `run`/`shell` (including the
  default command) and the image-build entry points (`build`, `image build`) — ask for approval when
  interactive and refuse when not (Non-interactive behavior). Every other command runs without the project
  layer and warns once that the project config changed, is not approved, and is ignored, naming how to
  approve it.
- The approval prompt ignores assume-yes (`--yes`, a `yes:` key) and never takes a non-interactive default:
  `termio`'s `Confirm` returns true under assume-yes and `Select` returns its default without a TTY, so the
  gate checks interactivity itself and neither path can approve.
- `--dry-run` is a read-only preview (`specs/architecture/dry-run-preview.md`): it reports that approval would be required and
  what would change, previews without the project layer, and never prompts or writes.

## Consistency between approval and use

What reaches the image and the VM must be what was approved. The consumers still read their project inputs
at different stages — viper at config resolution, the env and secret files in ensure-home, the agent config
in plan-reconfig, the Dockerfile in ensure-image — so a guest edit after the approval could slip unreviewed
content in. The contract is enforced by recomputing the projection from the inputs actually consumed,
before the image build and before the VM is created or started, and aborting the run on a mismatch with the
approved digest. This replaces a single snapshot reader, which would mean rerouting every consumer.

## Provenance

The projection must be computed from the project layer alone, not from viper's merged view, because the
merge discards which file set a key. The project layer is read on its own — the files are named by
`configpaths.ProjectConfigDir()`, the project secret-file accessors, and `reprovision.ResolveConfigSource`
(which resolves the project agent config dir). `homeconfig.LoadLayers` already keeps one `Layer` per config
dir and can attribute its entries; `mounts` is loaded through the merged `viper` view today and needs the
same per-layer treatment. Provenance is used only to build the projection; the projection and its digest
are persisted (State), nothing else.

## State

- The approval is per **checkout**, not per agent: one checkout's config content is approved once and its
  agents share that approval. It is a section keyed by the checkout slug (`../identity/checkout-identity.md`) in the
  machine-global `RootState` — the per-`(slug, agent)` `HomeState` is the wrong scope, and a new file is
  forbidden (`../state/state-model.md`). The section holds the approved
  projection and its digest; the projection is retained so a later change can be shown as resolved
  old → new, and it holds no value from the project secret files. Losing `RootState` loses the approval, so
  the next run re-approves — the safe direction.

## Decisions

Recorded on #229 (2026-11-08):

- The gate runs at config resolution and an unapproved project layer is not loaded, rather than a gate in
  `prepare` (Where the gate runs).
- Project files copied by `home:`, startup hook scripts included, are gated by content (What is gated).
- Consistency between approval and use is enforced by a recompute-and-compare check, delivered after the gate
  itself; until it lands, an edit between approval and consumption is a known gap (Consistency between
  approval and use).

## Defense in depth (related, not this contract)

These narrow the window or surface drift; none replaces the gate, because a config already in the checkout
still reaches the host run:

- **Deny-list the project secret files** (#535) — confidentiality; a read-only mount still permits reads,
  so secrets need hiding, not freezing.
- **Read-only-protect `.agents-sandbox`** (#536) — freezes mid-session writes; redundant once this gate
  exists.
- **`config harden`** (#118) — audits the effective configuration and remediates drift.

## Non-goals

- Not a sandbox-escape or VM-boundary fix: it governs the launcher's trust in the project config layer, not
  the VM boundary itself.
- Not the general reconfiguration-visibility feature (#230), though it owns #230's requirement that a
  project-sourced change be shown with its resolved effect.
- The user layer is never gated, and no setting is gated for the user layer.
- No `--yes` override and no non-interactive approval path in v1.

## Open questions

- The prompt's exact shape, and whether it reuses `config harden`'s finding/remediation machinery.
- How the projection represents the merge of several snippet files: each file's content, or the merged
  output the launcher actually provisions.
