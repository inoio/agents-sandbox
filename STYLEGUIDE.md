# Documentation Styleguide

This guide is how we write the user-facing documentation for agents-sandbox: the pages under `docs/` and the quick
start in `README.md`. The goal is a reader who can finish a task on the first attempt without leaving the page
confused.

## Who this is for

Anyone editing a page under `docs/` or the user-facing parts of `README.md`. You do not need to know the codebase or
how Jekyll works, though a new page needs front-matter (`title`, `layout`, `nav_order`) so it appears in the
navigation. You do need to keep the pages you touch consistent with the rules that follow.

A behaviour change and the documentation change that describes it go in the same pull request.

## Voice and audience

Assume a reader with no prior knowledge and ordinary patience. They may never have used a VM, a container, or any
coding agent. Write so the page makes sense on first contact, not merely on a second read.

- Address the reader as "you", not "the user".
- Use the active voice and short sentences. One idea per sentence.
- Prefer plain words to jargon. When a term is unavoidable, define it on first use in a plain sentence.
- Front-load the point. The first sentence of a paragraph carries it; the rest supports it.
- Never write "simply", "easy", "quickly", or "let's". Each one tells a stuck reader that the problem is theirs.

## Structure: one kind per page

Every page is one of three kinds. Do not mix more than one kind in the top-level flow.

- **Task** — a sequence of steps that reaches an outcome, such as a page on installing agents-sandbox.
- **Reference** — a complete, structured description you look things up in, such as the configuration field tables.
- **Explanation** — background that answers "why", such as how the isolation works.

This split is [Diátaxis](https://diataxis.fr/). A task page often needs one explanatory sentence; keep it to one and
link to the explanation page. A reference or explanation page may still carry a quick-reference table and a few
example commands. When a page grows a second kind as a first-class section, split the page instead.

## Ordering: the three rules

Three rules decide the order of the sections on a page and of the sentences inside a section. They apply to every
page.

**1. Explain before you name.** Introduce a concept in a plain sentence before its shorthand. Do not lead with a
parenthetical like "one VM per (checkout, agent)", and do not lead with an internal path like `/home/dev` that the
reader has no reason to know yet.

```text
Before:  Each project gets one sandbox per (checkout, agent).
After:   Each project checkout gets its own sandbox. Switching agents creates independent sandboxes.
```

**2. Lead a section with its applicability.** The first sentence of a conditional section names who it applies to, so
everyone else can skip it.

```text
Before:  The sandbox stops after a period of inactivity. opencode waits for active agent sessions before stopping.
After:   opencode only: the sandbox waits for active sessions before stopping.
         opencode2, pi, Claude Code, and Codex skip that wait and stop when the idle timeout expires.
```

**3. Order by universality and search intent, not taxonomy.** Actions that apply to everyone and are commonly
searched for come before conditional or specialized behaviour. "Stop a sandbox now" comes before the idle-timeout
section, even if a taxonomy would file them together.

```text
Before:  Idle-timeout details, then "Stop a sandbox now".
After:   "Stop a sandbox now", then the idle-timeout details.
```

Two supporting habits: an inverted pyramid per page and per section (the most useful sentence first), and one term
per concept for the whole site.

## Page template

Use this shape for every page:

1. **Lead sentence.** One or two plain sentences that say what the page is for and what the reader gets from it.
2. **"In short" callout or quick reference table.** A short summary or the handful of commands most readers want,
   before any detail. When you use the callout, write it as a `> **Note:**` block.
3. **Body in short chunks.** One idea per paragraph, under one heading that names it.
4. **Detail and the "why" last.** Edge cases, internals, and background go at the end, where only an interested
   reader reaches them.

```text
# Sandboxes

Every project runs in its own sandbox ...

> **Note:** A sandbox is a virtual machine. You rarely create one by hand.

## Quick reference

| I want to ... | Run |
| --- | --- |
| ... | ... |

## Lifecycle

...

## Why agents-sandbox runs its own runtime

...
```

## Terminology

Use one term per concept and the same term everywhere. These words are fixed:

| Use | Not |
|---|---|
| agent | harness |
| sandbox (for the VM as a whole); VM only for kernel or hypervisor detail | (do not alternate between the two for the same thing) |
| worktree session | branch session |
| `--docker` / `docker` | `--dind` / `dind` |
| `AGENTS_SANDBOX_*` | `OPENCODE_SANDBOX_*` (only the deprecations page names the old prefix) |

"agent" is the program that runs inside the sandbox: opencode, opencode2, pi, Claude Code, or Codex. "harness" is internal
test vocabulary and does not appear in `docs/`.

## Formatting

- **Headings** are sentence case: "Stopping a sandbox", not "Stopping A Sandbox". The page has exactly one `#`
  heading, and it matches the page title in its front matter.
- **Code blocks** are fenced and carry a language: `console` for shell sessions, with their output when it clarifies;
  `sh` for scripts, `yaml`, `json`, `markdown`, and `text` for the rest. Leave the language off only when none fits.
- **Links.** A page under `docs/` links to another with `{% link path/to/page.md %}`; a root-level document links into
  `docs/` with the rendered HTML URL (`https://inoio.github.io/agents-sandbox/<page>.html`).
- **Callouts** use one convention, on the first line of a blockquote: `> **Note:**`, `> **Warning:**`, or
  `> **Beta**`. Do not invent new markers.
- **Tables** carry enumerations: commands, flags, fields, and options. Prefer a table to a bulleted list of the same
  shape.
- **References within a page** never say "above" or "below". Name the section, or repeat the short fact.

## Review checklist

Walk this list before you open the pull request. It is the same list a reviewer will use.

- [ ] The page is exactly one kind: task, reference, or explanation. Which kind?
- [ ] The headings alone carry the page's point to a reader scanning them.
- [ ] Every conditional section names its audience in its first sentence.
- [ ] Every concept is explained in a plain sentence before it is named.
- [ ] No paragraph is longer than about six sentences.
- [ ] Every command example runs unedited.
- [ ] The terminology matches the Terminology table; the word "harness" is absent.
- [ ] Navigation depth from the docs home is at most two levels.
