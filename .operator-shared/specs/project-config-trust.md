---
description: Contract for the project config trust guard (#229): the project config layer is untrusted in its entirety, whatever the launcher consumes from it requires approval-on-change recorded per checkout, an unapproved change refuses to start non-interactively, and the prompt shows the resolved old → new values and their source.
read_if: Working on project config trust (#229), the approve-on-change gate, project-layer provenance, or `config harden`; or diagnosing a run that refuses to start because `.agents-sandbox` changed.
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
  it does **not** decide whether the VM must be recreated or restarted (`reconfig-decision.md`). The two
  are separate decisions and separate prompts.

## What is gated

The **whole project config layer** is gated — every input the launcher consumes from `<project-config>/**`:

- the launcher config file (`config.{yaml,yml,json,jsonc,json5}`) — every key it contributes, not a
  dangerous subset.
- the project env and secret files (`env`, `env.secret`, `env.secret.yaml`).
- the project agent config directories (`<project-config>/<agent-config-dir>/`) — the snippets that merge
  into the agent's VM config and the files mirrored verbatim (plugins, sub-configs, …).
- the project Dockerfile.

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
    re-prompt, and the prompt never prints a secret value.
  - **file inputs** (the Dockerfile, agent snippets and mirrored files) → the file's content.
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
- Approval is interactive. The prompt names each changed input with its **resolved old → new value and the
  source file** (this is #230's requirement, owned here for project-sourced changes), then asks to
  continue. Approving records the new digest; declining aborts the run before any side effect and returns a
  cancellation the caller treats as a user decline.
- The recorded digest is written through the `RootState` transaction (`state.WithRootState`,
  `project-state-transactions.md`) and preserves every field it does not own.

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

- In `prepare` (`prepare.PrepareSandbox`), before any build, volume, state, VM, or network side effect — in
  particular **before the image build** (`EnsureImage`), so a project `docker` or Dockerfile cannot be
  baked before approval. The gate reads the project layer itself (see Provenance); it does not wait for the
  later, single host env/secret read (`envsecret.LoadEnvAndSecrets`).
- The image-build entry points (`build`, `image build`) never call `PrepareSandbox` and would otherwise
  bypass the gate: they must run the same check before building, since they read the project Dockerfile and
  the project `docker` setting.
- `--dry-run` is a read-only preview (`dry-run-preview.md`): it reports that approval would be required and
  what would change, and never prompts or writes.

## Provenance

The projection must be computed from the project layer alone, not from viper's merged view, because the
merge discards which file set a key. The project layer is re-read on its own — the files are named by
`configpaths.ProjectConfigDir()`, the project secret-file accessors, and `reprovision.ResolveConfigSource`
(which resolves the project agent config dir). `homeconfig.LoadLayers` already keeps one `Layer` per config
dir and can attribute its entries; `mounts` is loaded through the merged `viper` view today and needs the
same per-layer treatment. Provenance is used only to build the projection; only the digest is persisted.

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

- Not a sandbox-escape or VM-boundary fix: it governs the launcher's trust in the project config layer, not
  the VM boundary itself.
- Not the general reconfiguration-visibility feature (#230), though it owns #230's requirement that a
  project-sourced change be shown with resolved values.
- The user layer is never gated, and no setting is gated for the user layer.
- No `--yes` override and no non-interactive approval path in v1.

## Open questions

- The prompt's exact shape, and whether it reuses `config harden`'s finding/remediation machinery.
- How the projection represents the merge of several snippet files: each file's content, or the merged
  output the launcher actually provisions.
