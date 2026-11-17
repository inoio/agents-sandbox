---
description: The learn-mode proposal and applier - the finding shape (evidence, proposed write, scope), the per-slug user scope and the project scope behind the trust gate, why the managed layer never owns the credential store, how a learned static secret is attributed before it is scoped, the network diff that never widens, the interactive per-finding apply and its decline memory, non-interactive behavior, `config harden` as the config-only subset, and how the after-run is owned.
read_if: Working on the learn-mode proposal, findings, or applier - the finding shape or evidence, the per-slug user scope or the project scope and its trust gate, the credential-store copy-back rule, static-secret attribution or the network diff, per-finding apply and decline memory, non-interactive or dry-run behavior, `config harden`, or which process owns the after-run.
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

- **The per-slug user scope** (`~/.config/agents-sandbox/<repo>-<hash>/`) is the default, because it is a trusted
  user layer and needs no approval gate. It is keyed by the **repo slug**, so it is shared by every clone of the
  remote — the proposal must state that.
- **The project scope** is offered as an explicit second choice only (team sharing, committed config), and the
  project trust gate then applies.

## The credential store

**The managed layer never owns the credential store.** `reprovision.Provision` writes merged and mirror files
unconditionally on every start, so a managed file that carries a login entry the agent refreshed in the sandbox
would destroy it. The copy-back therefore never targets a file the agent refreshes, and never adds a `home:` or
provision rule for the credential store. Static secrets go to `env.secret.yaml` and appear as placeholders in the
managed agent config; login entries stay only in the persistent sandbox home.

## Attribution and the network diff

- **A learned static secret is attributed before it is scoped.** Its host set comes from the destinations the
  session was observed sending that credential to; with no attribution (a client pins certificates, a non-HTTP
  protocol) it falls back to the endpoint in the same config entry, then to a question at apply time. When none is
  derivable the secret is not migrated: it stays in the sandbox home. A learned value never produces
  `allow_any_host_dangerous`.
- **Network changes are proposed as a diff**, normally `profile: none` plus the observed allow set, and never widen
  a policy the user already set. Denied destinations are reported separately and never auto-allowed.
- The applied network policy is creation-only, so it takes effect at the next VM creation through the existing
  recreate path; the proposal states that.

## Applying

- Applying is interactive and per finding. A decline is remembered, keyed on a hash of the evidence, and re-armed
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

## `config harden`

Harden is the analyser with no session: the same findings engine, the same applier, the config-only subset of
findings. It is re-runnable and audits the effective configuration (file modes, `provision-host-config: true`, an
over-broad posture, an over-broad secret scope), and it is the home of "reduce egress later". It runs the findings
that need configuration; the after-run analyser runs both configuration and observation findings.

Harden does not need a provider knowledge table. Its network findings are either structural (a broad profile, a
wildcard entry) or, when a session has been analysed, evidenced by that session.
