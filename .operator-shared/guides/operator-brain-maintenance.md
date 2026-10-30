# Maintaining the Operator brain

Conventions for the `.operator-shared/` brain so it stays small and findable. The brain is auto-injected at
session start, so its size is a per-session tax and its accuracy is a correctness risk.

## Catalog entries

The catalog is a map, not a second copy of the docs.

- **Description** — one line, present tense: *what the file is*. No changelog. Never write "Implemented on
  `<branch>` (PR #N)", review-round narratives, or commit hashes here; those belong in git, not the map.
- **Read If** — completes the sentence "Read if ___": the tasks, code areas, and symptoms that should send an
  agent to this file. Lead with anchors that a real task would contain — package/file/type/function names,
  commands, config keys, flags, error strings — and with diagnostic phrasing ("diagnosing an empty
  `cfg.Volumes`", "notify does nothing on opencode2"). Do **not** restate the Description, and avoid the empty
  prefixes "Implementing or reviewing …" / "Reviewing or resuming …" — the field already says when to read.
- Keep entries to `Description` + `Read If`. Entries and their order are stable; only the text changes.

## Plans vs specs

- A **spec** is durable truth: the contract, invariants, and non-obvious decisions that must stay true. Present
  tense, no implementation history.
- A **plan** is process, not durable truth. When its work lands, fold any clause the owning spec lacks into that
  spec, then **delete the plan**. Keep only plans whose work is not yet done. Git retains the history.
- Never let a spec point at a plan that will be deleted. If a spec cites a plan for detail, move the durable
  detail inline first.
- A superseded clause is **replaced**, not appended to. One source of truth per fact; private docs may reference
  shared ones, never the reverse.
- Reference code by **symbol and package name**, not `file:line`. Line numbers rot silently on every refactor;
  name the function/type/file and let the reader search. Keep a path only when the path itself is the fact.
- A shared document must resolve from the published repo alone. Never point at untracked or local-only tooling
  (the `.agents/skills/` runbooks, for example) as the home of the detail; inline what the reference would have
  carried, or drop the claim.

## Index vs catalog

- **Index** (`index/`) maps the *codebase*: architecture, packages, files.
- **Catalog** (`catalog.md`) maps the *brain*: which spec/guide/plan to read, and when.
- Do not cross the two. The catalog never lists repo source files; the index never lists brain documents.
