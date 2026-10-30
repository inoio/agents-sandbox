# Shared Operator Instructions

`AGENTS.md` is authoritative for code, toolchain, and dev commands; this file adds only
what `AGENTS.md` does not cover.

## Workflow

- Agree on the approach before large or creative work; proceed directly on small, clear,
  well-specified changes.
- When dispatching subagents, always use the `litellm/DeepSeek v4.1 Flash 0910 (GreenPT)`
  model (the exact id the `models` tool reports; a slug like
  `litellm/deepseek-v4-1-flash-0910-greenpt` is rejected).

## Brain

- When adding or editing a catalog entry, spec, plan, or guide, follow
  `guides/operator-brain-maintenance.md`.

## Private / Shared Policy

- **Shared** (`.operator-shared/`, published with the repo): durable project truth.
- **Private** (`.operator/`, local only): operational or coordination detail that should
  not ship with the repo. Currently none.
- A private document may reference shared content; a shared document must never reference
  private files, paths, or data.
