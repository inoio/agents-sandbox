# GitHub issue workflow

Issues and specs for this repo (`inoio/agents-sandbox`) live as GitHub issues. Use the `gh` CLI for
every operation; it infers the repo from `git remote -v`. This is the tracker the `maintain` skill
reads from and writes to.

## Conventions

- **Create**: `gh issue create --title "..." --body "..."` (heredoc for multi-line bodies).
- **Read**: `gh issue view <number> --comments`, filtering with `jq`; fetch labels too.
- **List**: `gh issue list --state open --json number,title,body,labels,comments --jq '...'` with
  `--label` / `--state` filters.
- **Comment**: `gh issue comment <number> --body "..."`
- **Labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <number> --comment "..."`

GitHub shares one number space across issues and PRs, so a bare `#42` may be either: resolve with
`gh pr view 42` and fall back to `gh issue view 42`.

## Triage label vocabulary

The `maintain` skill's state machine maps onto these actual repo labels:

- **Category** (exactly one): `bug`, `enhancement`, `chore`, `documentation`.
- **State** (exactly one): `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`,
  `wontfix`.
- **Marker** (at most one): `upstream` — depends on an external project (microsandbox, opencode, …).
  An `upstream`-marked issue **may omit the state role**: the maintainer does not require one for it
  (an external dependency is not a triage stage). Issues that do carry a state keep it.
- **Priority** (orthogonal, not part of the state machine): `priority:critical`, `priority:high`,
  `priority:medium`, `priority:low` (rubric in `.agents/skills/maintain/PRIORITIES.md`).
- **Maintainer-only**: `security`.

Dependabot's ecosystem labels (`dependencies`, `github_actions`, `go`) belong to PRs, which this repo
does not triage.

## External pull requests

**Not a request surface.** Non-dependabot PRs are not a maintenance target; `maintain` handles only
dependabot PRs. To triage a named PR anyway, ask the maintainer first.

## When a skill says "publish to the issue tracker"

Create a GitHub issue. **"Fetch the relevant ticket"** → `gh issue view <number> --comments`.
