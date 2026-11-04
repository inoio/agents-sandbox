---
description: Contract for the project config trust guard (#229): the project config layer is untrusted, the host-affecting settings it contributes require approval-on-change recorded per checkout, an unapproved change refuses to start non-interactively, and the prompt shows the resolved old → new values and their source.
read_if: Working on project config trust (#229), the approve-on-change gate, project-layer provenance, or `config harden`; or diagnosing a run that refuses to start because `.agents-sandbox` changed.
---

# Project config trust

## Why

The project-config directory — `<checkout>/.agents-sandbox`, or `<bare-root>/.agents-sandbox` in a bare
layout (`configpaths.ProjectConfigDir`) — is merged **above** the user config (`viperconfig.ConfigLayers`:
generic user dir, per-slug user dir, project dir, env, flags) and treated as fully trusted, while the
sandbox can write into that same tree through the writable `/workspace` bind. An agent that edits
`config.yaml`, or plants one in a repo, has the next host run apply it.

Two settings do the damage: `mounts` exposes any host directory inside the VM (`mounts.ResolveBindMounts`
only checks that the source exists and is a directory), and `home` copies any host file into the VM
(`homeconfig` resolves the source as an absolute path or `~/…`). Both reach host paths the user never
granted — the whole filesystem, the host Docker socket, `~/.ssh`, the user's own secret files. `network`
(egress widening), `provision-host-config`, `docker`, and the project secret files widen the same
boundary.

Guest-side file controls cannot enforce this trust decision: the deny-list (#12/#39) and ro-list (#536)
act on the VM's view of `/workspace`, but the launcher reads the project config **host-side**, and a
config already committed or planted before a policy applies still reaches the host run. The remedy is a
host-side gate on the project layer.

## Trust model

- Two layers, two trust levels. The **user layer** (the user's own config and secret files, authored on
  the host) is trusted and is never gated. The **project layer** (anything under the project-config
  directory, inside the guest-writable checkout) is untrusted.
- Approval is **by content, per checkout**. The user approves an exact host-affecting projection once;
  every later run whose projection matches is silent. When the projection changes, it is gated again.
- The gate is authority, not reconfiguration. It decides whether the project layer may impose a
  host-affecting setting at all; it does **not** decide whether the VM must be recreated or restarted
  (`reconfig-decision.md`). The two are separate decisions and separate prompts.

## The approval set

Only the settings the project layer contributes to are gated, and only those that affect the host or the
VM's reach beyond itself:

- `mounts` — host directories exposed inside the VM.
- `home` — host files copied into the VM.
- `network` — the egress profile and rules.
- `secrets` — the project secret files' substitution **structure** (`env.secret`, `env.secret.yaml`, the
  structured secret file): env-var names, hosts, `header_fields`, `allow_any_host_dangerous`, and the
  `secrets.common.allow-placeholder-for` passthrough list. The gate covers *where* a secret may be sent,
  not the value: a value rotation does not re-prompt, and the prompt never prints a secret value.
- `provision-host-config` — copies host CLI config into the VM.
- `docker` — enables a container engine inside the VM.

Every other project-layer setting (resources, image, notify, upgrade, output, …) is not host-affecting and
is not gated: a change to it never prompts. The set is closed; adding a key is a deliberate change to this
contract.

## Approval contract

- On each run that resolves the project layer, compute the **project trust projection**: the values of the
  approval-set keys contributed by the project layer, together with the identity of the files that
  contributed them. Canonicalize (sorted keys/entries) so reordering, comments, and non-gated key edits do
  not change the digest.
- The projection is the project layer's **own declared contribution**, resolved to canonical values but not
  composed with the higher layers. A change to the user layer or a flag must not invalidate an approval,
  and a project value currently shadowed by a flag is still gated (approval is about what the project layer
  could impose, not this run's effective value).
- Hash the projection and compare it with the digest recorded for the checkout.
  - **Match** → proceed silently.
  - **Empty projection** — a project layer that contributes no approval-set key — is silently allowed;
    there is nothing to approve.
  - **No recorded digest** with a non-empty projection (first run, a fresh clone, lost state) → treated as
    changed.
  - **Differ** → approval is required.
- Approval is interactive. The prompt names each changed setting with its **resolved old → new value and
  the source file that set it** (this is #230's requirement, owned here for project-sourced changes), then
  asks to continue. Approving records the new digest; declining aborts the run before any side effect and
  returns a cancellation the caller treats as a user decline.
- The recorded digest is written through the `HomeState` transaction (`state.WithHomeState`,
  `project-state-transactions.md`) and preserves every field it does not own.

## Non-interactive behavior

- When approval is required and no digest is recorded, a non-interactive run **refuses to start**. The
  error names the changed settings and their sources and says how to resolve it (run once interactively to
  review and approve). "Non-interactive" is any UI without a TTY, including `--serve-only`.
- The generic `--yes` flag does **not** satisfy the gate. `--yes` authorizes destructive actions; it must
  never be read as authorizing untrusted project input. There is no non-interactive approval path in v1:
  the first approval of any given projection is a human act. A dedicated explicit opt-in may be added
  later only against a concrete headless need.
- A non-interactive run whose projection is already approved proceeds normally.

## Where the gate runs

- In `prepare` (`prepare.PrepareSandbox`), before any build, volume, state, VM, or network side effect — in
  particular **before the image build** (`EnsureImage`), so a project `docker` or Dockerfile cannot be
  baked before approval. The gate reads the project layer itself (see Provenance); it does not wait for the
  later, single host env/secret read (`envsecret.LoadEnvAndSecrets`).
- `--dry-run` is a read-only preview (`dry-run-preview.md`): it reports that approval would be required and
  what would change, and never prompts or writes.

## Provenance

The projection must be computed from the project layer alone, not from viper's merged view, because the
merge discards which file set a key. The project layer is re-read on its own — the files are named by
`configpaths.ProjectConfigDir()` and the project secret-file accessors. `homeconfig.LoadLayers` already
keeps one `Layer` per config dir and can attribute its entries; `mounts` is loaded through the merged
`viper` view today and needs the same per-layer treatment. Provenance is used only to build the
projection; only the digest is persisted.

## State

- The approval is per **checkout**, not per agent: one checkout's config content is approved once and its
  agents share that approval. It is a section keyed by the checkout slug (`checkout-identity.md`) in the
  machine-global `RootState` — the per-`(slug, agent)` `HomeState` is the wrong scope, and a new file is
  forbidden (`state-by-scope.md`, `project-state-transactions.md`). Losing `RootState` loses the approval,
  so the next run re-approves — the safe direction.

## Defense in depth (related, not this contract)

These narrow the window or surface drift; none replaces the gate, because a config already in the checkout
still reaches the host run:

- **Deny-list the project secret files** (#535) — confidentiality; a read-only mount still permits reads,
  so secrets need hiding, not freezing.
- **Read-only-protect `.agents-sandbox`** (#536) — freezes mid-session writes; redundant once this gate
  exists.
- **`config harden`** (#118) — audits the effective configuration and remediates drift.

## Non-goals

- Not a sandbox-escape or VM-boundary fix; it governs host-config authority only.
- Not the general reconfiguration-visibility feature (#230), though it owns #230's requirement that a
  project-sourced change be shown with resolved values.
- The user layer is never gated, and no setting is gated for the user layer.
- No `--yes` override and no non-interactive approval path in v1.

## Open questions

- Whether `docker` and the project Dockerfile belong in the approval set (recommendation: yes to both —
  `docker` grants a container engine, and the Dockerfile is code baked into the image). If so, the gate must
  also cover the image-build entry points (`build`, `image build`), which never call `PrepareSandbox` and
  would otherwise bypass the gate.
- Whether the projection also covers the raw contributing files, so that a change of source path (same
  value) re-prompts.
- The prompt's exact shape, and whether it reuses `config harden`'s finding/remediation machinery.
