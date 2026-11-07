# Shared Operator Instructions

`AGENTS.md` is authoritative for code, toolchain, and dev commands; this file adds only
what `AGENTS.md` does not cover.

## Workflow

- Agree on the approach before large or creative work; proceed directly on small, clear,
  well-specified changes.
- When dispatching subagents, always use available, fitting agents (`implementer-small/-large`, `reviewer-small/-large`,
  `general`) using their predefined models instead of explicitly choosing a model.

## Brain

- When adding or editing a catalog entry, spec, plan, or guide, follow
  `guides/operator-brain-maintenance.md`.

## Skills

- Two skill libraries cover overlapping ground: the **superpowers** plugin (always loaded) and this repo's
  `.agents/skills/`. Where both cover the same
  concept, prefer **superpowers** — its discipline is stricter and it is present
  in every session. Keep a `.agents/skills/` skill only when it produces a
  distinct artifact superpowers has no equivalent for (the architecture/domain
  vocabulary and the design review), or when it is repo-specific: the release,
  bare-worktree, maintainer, PR-feedback, and PR-body skills, plus the
  operating runbooks retired from the brain (opencode2-self-management,
  opencode2-probe, terminal-reset).
- Do not run both libraries' skills for the same step. Pick one per task.

## Private / Shared Policy

- **Shared** (`.operator-shared/`, published with the repo): durable project truth.
- **Private** (`.operator/`, local only): operational or coordination detail that should
  not ship with the repo. Currently none.
- A private document may reference shared content; a shared document must never reference
  private files, paths, or data.
