---
description: Contract for where a bare checkout's project config (`.agents-sandbox`) is read from: the bare root next to `.git`, selected by `core.bare`, with every project accessor routed through one base-dir resolver. Non-bare and non-repo behavior is unchanged; a bare checkout's worktree-level `.agents-sandbox` is ignored with a warning; the bare-worktrees recipe is marked beta.
read_if: Implementing or reviewing the bare-layout project-config root fix, the `core.bare` discriminator, the project config base-dir resolver, or the bare-worktrees recipe/Beta marking.
---

# Bare checkout project config root

Status: merged (PR #247).
Related: [bare-worktree-layout.md](bare-worktree-layout.md) (the `/workspace` bind rule this builds on; supersedes its "project config unchanged" implication). The `checkout-identity.md` extension of the same `configpaths` seam is not on main (#144's `config paths` shipped separately), so it is named rather than linked.

## Problem

In the bare layout the whole checkout root is bound at `/workspace` and issue #145's setup has the project's
`.agents-sandbox` filled at the bare root, next to `.git`:

```
mkdir bare-checkout
cd bare-checkout
git clone --bare <repo-url> .git
git --git-dir=.git worktree add main main
# fill .agents-sandbox
```

But project config is resolved relative to the launching process's CWD, and the documented recipe launches from a
worktree (`cd myproject/IDE; agents-sandbox`). So `<bare>/.agents-sandbox` is never read; the launcher reads
`<bare>/IDE/.agents-sandbox`, which a worktree need not have at all. The Dockerfile, `config.yaml`, `env`,
`env.secret`, home sources and agent config are all affected — `ProjectDockerfile`, `ProjectEnvFile` and
`ProjectEnvSecretFile` join the `projectConfigDir` const directly, and `ProjectConfigDir`,
`ProjectConfigDirAbs` and `ProjectAgentConfigDirCandidates` derive from it.

The intended shape is that the config branch is checked out at `<bare>/.agents-sandbox`, worktrees carry no
`.agents-sandbox`, and templates (e.g. AGENTS.md) refer to the config at the bound root.

## Decision

Split on the repository's `core.bare`:

- **bare checkout** (worktree inside the bare root) → project config is `<bare root>/.agents-sandbox` only;
- **non-bare checkout, and subdirectories of it** → unchanged, `$PWD/.agents-sandbox`;
- **not in a repository** → unchanged, `$PWD/.agents-sandbox`;
- **outside worktree** (bare or not; discouraged, and not possible in the documented bare layout) → unchanged,
  the worktree's own `$PWD/.agents-sandbox`. Do not reach across the tree.

No ancestor walk and no multi-directory layering in this change. A `.agents-sandbox` inside the launched worktree
of a bare checkout is **ignored** — the one intended behavior change. Because it is silent otherwise, warn when
such a directory exists and add a CHANGELOG (Unreleased) line. This deliberately reverses "the worktree's copy
wins" for the bare layout only; see [Deferred extension](#deferred-extension-layering).

Slugs, VM/volume/state names, home-volume resolution and the runner-image tag do not move. In the bare layout the
bare root is the bind root, so the config lands at `/workspace/.agents-sandbox` and stays visible to the agent.

## The resolver (contract)

One project-config **base dir** replaces the implicit `$PWD`:

- It is derived by one function from the CWD and must agree across every entry point that reads project config
  (`run`, `shell`, `build`/`dockerfile`, the `volume …` ops, `config …`) — the consistency lesson #144 paid for,
  where `/tmp` vs `/private/tmp` must not change the answer.
- Every project accessor resolves against it: `ProjectConfigDir`, `ProjectConfigDirAbs`, `ProjectDockerfile`,
  `ProjectEnvFile`, `ProjectEnvSecretFile`, `ProjectEnvSecretYAMLFile`, and
  `ProjectAgentConfigDirCandidates`/`ProjectAgentConfigDir`. Standing consumers (homeconfig home sources and
  hooks, `reprovision.ResolveConfigSource`, `envsecret.LoadEnvAndSecrets`, `viperconfig.mergeDir`,
  `image.readProjectDockerfile`) follow without their own special case. A partial routing — some accessors fixed,
  some not — is worse than no fix.
- It is pure: it resolves paths and inspects files, and never creates a directory.

Implementation facts that shape the resolver:

- The discriminator is `core.bare` from the common git dir's `config`, read with go-git
  (`config.ReadConfig` → `cfg.Core.IsBare`), the same route `internal/git/git.go`'s `originURL` uses. No host
  `git` binary. `internal/git` imports no internal package, so `configpaths` may depend on it without a cycle.
- `checkoutRoot` as `internal/git/workspace.go:25 ResolveWorkspaceLayout` already computes it is the bare root in
  the bare layout, so the base dir is `checkoutRoot/.agents-sandbox` when `core.bare` and the worktree is inside
  it. `ResolveWorkspaceLayout` cannot distinguish a bare root from a main checkout root today — both are
  `Dir(.git)` — which is exactly why `core.bare` is needed.
- `ProjectConfigDir()` is deliberately **relative** today while `ProjectConfigDirAbs()` is absolute; a bare base
  is an absolute bare root. The representation is settled below.

### Resolver ownership and representation (settled after the builder check)

- The real `configpaths` implementation resolves the base **lazily on each project accessor call** — no
  memoization and no install hook — so CLI commands, direct library callers, and tests all see the same answer.
  `configpaths` imports `internal/git` for the layout and `core.bare` fact (`internal/git` imports no internal
  package, so there is no cycle); the mock seam keeps its current fixed-dir shape and does not consult git.
- Non-bare and non-repo keep today's behavior exactly: the relative `projectConfigDir` constant, resolved against
  the **logical** CWD at call time, no `EvalSymlinks` (an unreadable CWD degrades to the relative constant, as
  `ProjectConfigDirAbs` already did). Every string those accessors return today is unchanged, so
  existing unit tests hold.
- Bare: the layout is derived from the **symlink-resolved** CWD (as the bind root is) and the base is the
  absolute `<checkoutRoot>/.agents-sandbox`, so it equals the bind root. Only this branch pays `EvalSymlinks`.
- "Byte-identical resolution to today" constrains the resolved file/dir for non-bare, not the returned
  representation, which stays as above.
- **Sequencing: this lands before `checkout-identity.md` (#144).** Assume #144 is absent. The project accessors
  stay singular; only the internal base-dir indirection is added, so #144's plural accessors and resolved identity
  extend them additively later. Do not depend on #144 types, and do not pre-empt its interface changes.

### The ignored-worktree warning

The resolver stays pure: it exposes the ignored directory as a fact through
`configpaths.ConfigPaths.IgnoredWorktreeConfigDir`, empty when there is no ignored copy. The fact is the **worktree root**'s
`.agents-sandbox` (`git.WorkspaceLayout.WorktreeRoot`, exposed for this; the layout already computed it), not one derived
from the CWD — so a launch from any subdirectory of the worktree still detects the copy the bare root replaces, which a
CWD-derived check missed (PR #247 review). A `.agents-sandbox` at a deeper subdirectory is not inspected; that is the
deferred layering extension's job. When the CWD is the bare root itself the worktree root *is* the bare root, so the
ignored path equals the base: that is the directory being read, not an ignored copy — no warning (guard:
`ignored == base`). One `termio`-facing helper, `image.WarnIgnoredProjectConfig`, warns from the image path
every VM-bound command passes through — `image.EnsureImage` (called by `PrepareSandbox`, `run`/`shell` and the `volume …`
ops), `image.Build` (`image build`), and `build dockerfile` — not from `PersistentPreRunE`. `config`, `doctor`, `list`,
`version`, `prune` and `df` stay quiet, and each such command reaches exactly one call site, so the warning cannot repeat
within a run without a process-global guard.

### Test seam

`WithMockConfigPaths` fixes the project dir and cannot express a bare base, so the four-row resolution is
unit-tested in `configpaths` against a real temp bare repo (`t.Chdir`); the CLI acceptance for `build dockerfile`
and `run` uses real config paths (XDG pinned for isolation) for the bare case. The real-resolver opt-in,
`WithRealConfigPaths`, pins the CWD to a temp dir (see [test-isolation.md](test-isolation.md)), so a project-path
accessor test that does not chdir cannot inherit the checkout layout the suite runs in; the bare cases chdir into
their own fixture after opting in.

## Docs and the beta recipe

- `docs/recipes/bare-worktrees.md` is updated for this behavior (project config at the bare root; a worktree's
  `.agents-sandbox` is ignored with a warning) and **marked beta**, since the layout and its config rules are
  still moving. There is no callout system in the site; use a leading blockquote notice (e.g.
  `> **Beta** — the bare-checkout layout and its configuration rules may still change.`).
- `.agents/skills/bare-worktrees/SKILL.md` is a 1:1 content copy of the recipe (required by
  [bare-worktree-layout.md](bare-worktree-layout.md)) and must carry the same Beta notice.
- Keep in sync: `configuration/launcher.md` (the `.agents-sandbox/<file>` table and "project directory"
  wording), `commands.md` (`build`/`dockerfile`, `.agents-sandbox/Dockerfile`), `how-it-works.md`,
  `sandboxes.md`, and the CHANGELOG Unreleased section.

## Non-goals

- No layering/overlay of multiple `.agents-sandbox` directories, and no ancestor walk.
- No change to non-bare checkouts, including the existing subdirectory behavior.
- No new slugs, state, or migration; no change to `--worktree`.
- No bare-layout support for outside worktrees; such a worktree keeps today's behavior.

## Anti-goals

- Silently changing what a non-bare project builds.
- Fixing some project artifacts and not others.
- Making the base dir depend on which entry point ran.

## Acceptance

- Bare layout, launched from `<bare>/IDE`: the image builds from `<bare>/.agents-sandbox/Dockerfile`, and
  `config.yaml`/`env`/`env.secret`/home sources/agent config all resolve from `<bare>/.agents-sandbox`.
- Bare layout with a `.agents-sandbox` in the launched worktree: the worktree copy is ignored and a warning names
  it.
- Non-bare checkout at its root, in a subdirectory, and a non-repo folder: byte-identical resolution to today.
- Outside worktree: unchanged behavior.
- `project-config-root` unit tests cover the four rows; CLI tests cover at least `build dockerfile` and `run`
  resolution from a bare checkout.

## Deferred extension (layering)

The natural next step, agreed as out of scope here, is to make the project tier an ordered list merged
lowest→highest instead of one dir:

- an ancestor walk from the CWD up to the exit trigger, exit = CWD outside a repo, the worktree root in a
  non-bare checkout, and the bare root when `core.bare`; an outside worktree exits at itself;
- the nearest dir wins per artifact, keeping each artifact's existing merge rule (viper deep-merge for
  `config.yaml`, key-wise for `env`/`env.secret`, one homeconfig `Layer` per dir, single-file for the
  Dockerfile);
- this also fixes the same defect in a non-bare checkout: today `cd repo/sub && agents-sandbox` ignores
  `repo/.agents-sandbox`;
- `configpaths` would move to plural project accessors, sequenced after #144, which already extends the
  interface.
