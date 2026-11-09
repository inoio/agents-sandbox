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
- the files the project layer's `home:` entries copy from a guest-writable location — a source whose
  **resolved** path lies inside the project config directory or inside the checkout bound as `/workspace`,
  however the entry spells it (relative, absolute, or `~/…`), startup hook scripts included. Their content
  is gated, not only the entry: the guest can rewrite such a file, and a hook script runs in the VM. A source
  outside both is a host path the guest cannot write; the entry value naming it is gated as launcher config.

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
  The update extends the recorded approval by **only the written key** (`projecttrust.ApproveSettingWrite`);
  it does not re-read and approve the file, so anything else the agent changed in the same file meanwhile
  still needs approval. Without a recorded approval there is nothing to extend and nothing is recorded.
  The approval is recorded first and restored if the write then fails; a failed approval writes nothing,
  and a failed restore leaves the approval ahead of the file, which only makes the next run ask again.

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
- The gate resolves the process path factory (`configpaths.Get`) when it runs, not when `buildRootCmd` builds
  the tree, so a test seam that swaps the factory afterwards is honoured. It captures that factory before
  installing its own `configpaths.WithProjectLayer` wrapper, since `configpaths.Use` would otherwise make the
  wrapper self-referential.
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

What reaches the image and the VM must be exactly what was approved, byte for byte. The readers take their
project inputs at different stages — viper at config resolution, the env and secret files in ensure-home, the
agent config in plan-reconfig, the Dockerfile in ensure-image — and a guest can rewrite a file at any moment,
including flipping it back and forth. Re-reading and comparing at a checkpoint cannot close that: a reader may
have used other bytes than the checkpoint saw.

The contract is **verify on read**:

- The gate reads each project file once, parses or shows those very bytes, and keeps the digest of every file
  it read (in memory only, never persisted — a secret file's digest never reaches state). A file read twice by
  the gate (an `env` file also copied by `home:`) must read the same bytes, or the gate fails.
- After the approval, every reader of a project input reads through `projectguard.ReadFile` and verifies the
  digest of **the bytes it got**, so no window remains between verifying and using: every config file
  `configdecode.DecodeFile` reads (viper's config merge, the `home:` manifest, the setting writer's owner
  lookup), the `home:` source reads, the env and secret files, agent config snippets and the mirror
  walk (`filewalk`), and the project Dockerfile. Different bytes are refused, and so is an approved file that
  no longer reads (deleted, grown past a reader's bound). Paths are compared absolute and cleaned, so a
  `home:` source spelled with `..` is verified like any other.
- Under the project config dir, a file the approval does not hold may only be missing; an added file is
  refused. A `home:` source the gate skipped stays absent and is never read.
- Deleting an approved file is refused where it would be discovered, not only where it would be read: the
  config file lookup (`configdecode.FindFile`, every candidate extension), the `home:` source check, the
  snippet listing, and the mirror walk report what they did not find (`projectguard.Missing`,
  `projectguard.CheckListing`), so a reader never silently falls back to the lower layers or drops approved
  content — e.g. an approved `network: none` deleted to let the default `public` apply. "Gone" follows links
  as discovery does: an approved file swapped for a link to nothing is gone, and a listed entry counts as
  found only once it was read.
- A refused read is recorded as the command's violation. A reader that tolerates a read failure (a walk
  that skips, an env loader that warns) cannot carry it on: the image build and the VM stage stop on a
  recorded violation. The Dockerfile is read before the image build, so no image is built from a changed one;
  the env, secret, and agent config files are read after it, so a change there stops the run before the VM.
- A launcher write to the project config reads the file through the same check (a changed file is never
  edited into an approved one) and makes the bytes it wrote the approved bytes.

## Provenance

The projection is computed from the project layer alone, not from viper's merged view, because the merge
discards which file set a key. `projecttrust.Compute` reads the project config dir on its own: the launcher
config file once, parsed by `viperconfig.ParseLayerSettings` (as a merged layer would be) and
`homeconfig.ParseHomeConfig` from the same bytes, the env and secret files through the project scope's file
joins and the `envsecret` data parsers, every agent config dir any
registered agent reads (primary names and aliases, `agent.ConfigDirNames`), the Dockerfile, and the `home:`
manifest through `homeconfig`, resolving each source and following symlinks before the guest-writable test.
Provenance is used only to build the projection; the projection and its digest are persisted (State), nothing
else.

## State

- The approval is per **checkout**, not per agent: one checkout's config content is approved once and its
  agents share that approval. It is a section keyed by the checkout slug (`../identity/checkout-identity.md`) in the
  machine-global `RootState` — the per-`(slug, agent)` `HomeState` is the wrong scope, and a new file is
  forbidden (`../state/state-model.md`). The section is `RootState.project_trust` (`RootStateSchema` 2). It holds the approved
  projection and its digest; the projection is retained so a later change can be shown as resolved
  old → new, and it holds no value from the project secret files. Losing `RootState` loses the approval, so
  the next run re-approves — the safe direction.

## Decisions

Recorded on #229 (2026-11-08):

- The gate runs at config resolution and an unapproved project layer is not loaded, rather than a gate in
  `prepare` (Where the gate runs).
- Project-layer `home:` sources that resolve into the project config directory or the `/workspace` checkout,
  startup hook scripts included, are gated by content, decided on the resolved path (What is gated).
- Consistency between approval and use is verify-on-read of the bytes each reader got, not a recompute at a
  checkpoint, which a guest flipping a file back and forth could pass (Consistency between approval and use).

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

## Presentation

- The prompt lists one block per changed input — its status (`new`, `changed`, `removed`), source path, and
  kind — with `- old` / `+ new` lines from an order-aware line diff (a moved or repeated line shows), every
  value escaped for the terminal (`termio.EscapeControl`). It does not reuse `config harden` machinery. A first
  approval shows every input in full.
- Each snippet file is its own input with its content, not the merged output, so a snippet edit shows as an
  edit to that file.
- A file input carries the digest of every byte next to its display lines, so any byte change — a final
  newline, a line that reads like a marker — changes the projection; a change the lines do not show is noted
  as such. A missing final newline is shown as its own line. A file over 64 KiB or not UTF-8 text is shown
  as its `sha256:` entry alone. Files are hashed as a stream with the display buffer bounded at 64 KiB, so
  no input escapes the gate by size and none exhausts memory.

## Projection rules

- An input is identified by its path **and** kind: a file read two ways (an `env` file that `home:` also
  copies) is two inputs, so a change to its raw content is gated even when its parsed form is unchanged. A
  project secret file copied by `home:` is gated by digest only, so no secret value reaches the projection.
- The `home:` entries are projected as home provisioning parses them, case-sensitive (`homeconfig`), in
  addition to the lowercased launcher config keys: a case-only edit (`Root:` → `root:`) that changes what
  runs changes the projection.
- A project input the gate cannot read, or one that is no regular file (a symlink to a device), is an
  error, never an absent input. A `home:` source provisioning cannot copy (missing, a directory, unreadable,
  not regular) is skipped, as provisioning skips it.
