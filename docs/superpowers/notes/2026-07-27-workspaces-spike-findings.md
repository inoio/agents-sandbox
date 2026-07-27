# Workspaces Validation Spike — Findings

**Date:** 2026-07-27
**Spec:** `docs/superpowers/specs/2026-07-27-one-vm-per-project-design.md` (Section 1)
**opencode version:** 1.18.5
**msb version:** 0.6.7

## Summary

**Go/No-Go recommendation:** `GO WITH CAVEATS`

**Rationale:** Based on the spike findings:

1. **opencode version + workspaces support:** The `OPENCODE_EXPERIMENTAL_WORKSPACES=true` flag is injected into the VM and recognized by the serve daemon, but it is not surfaced in CLI help.
2. **Worktree control surface:** Worktrees are controllable programmatically via the HTTP API at `POST /experimental/worktree`; no CLI `worktree` subcommand exists.
3. **Shared project_id:** Both worktrees resolve to the same `project_id`, and sessions created in each worktree share that `project_id`.
4. **SQLite integrity:** The database remains intact (`PRAGMA integrity_check` returns `ok`) after concurrent worktree use.
5. **Bind-mount compatibility:** Linked worktrees function inside the VM, but they **pollute the host repo's `.git/worktrees/`** directory with entries referencing VM-internal paths. After the VM stops, these entries become `prunable` (stale) on the host. This is manageable with `git worktree prune` cleanup but is a real side effect of bind-mounting the host repo as `/workspace`.

**Blockers found:** `none`

**Caveats for the implementation plan:**

- A `opencode serve` daemon must be running for the workspaces API to be available.
- Worktree creation is API-only; there is no CLI worktree command.
- The API creates branches in the `opencode/<name>` namespace rather than checking out existing host branches.
- Worktree directories are created under `/home/dev/.local/share/opencode/worktree/<project-id>/` in the home volume, not under `/workspace`.
- The `/session` API returns `null` for `project_id` in this version; use `opencode db` for reliable session/project correlation.
- **Host `.git/worktrees/` pollution:** opencode's linked worktrees created from the bind-mounted `/workspace` write metadata into the host repo's `.git/worktrees/<name>/` directory. These reference VM-internal paths (`/home/dev/.local/share/opencode/worktree/...`) that don't exist on the host, so `git worktree list` on the host shows them as `prunable`. The launcher must run `git worktree prune` on the host repo as part of session/VM cleanup to avoid stale entries accumulating. Alternatively, the spec's fallback (clone `/workspace` into `/home/dev` as the primary worktree so `.git` is fully in-VM) avoids this entirely at the cost of host edit visibility.

**Recommendation for next steps:** Proceed to the full implementation plan for one-VM-per-project, incorporating the API-only worktree control surface and the home-volume worktree directory layout into the design.

## Task 2: opencode version + workspaces support

- Version: `1.18.5`
- `OPENCODE_EXPERIMENTAL_WORKSPACES` env var: `true`
- CLI help mentions worktree: `no`
- CLI help mentions workspace: `no`

**Assessment:** The workspaces flag is set in the environment but not surfaced in CLI help. Proceeding to serve + control surface discovery to determine whether the feature is active behind the daemon/API.

## Task 3: Serve daemon + worktree control surface

- Serve daemon: `healthy`
- Health response: `{"healthy": true, "version": "1.18.5"}`
- All API paths: listed in full OpenAPI spec (see `/doc`); notable paths include `/experimental/workspace`, `/experimental/worktree`, `/experimental/worktree/reset`, `/project/current`
- Worktree API endpoints found: `/experimental/worktree`, `/experimental/worktree/reset`
- Worktree CLI subcommand: `no`
- Current project_id: `56a2ecd18a6610ce515c91f45114dcb65edfb70a`
- Current project worktree: `/workspace`

**Control surface for creating worktrees:** HTTP API `POST /experimental/worktree` (no CLI worktree subcommand exists).

## Task 4: Shared project_id across worktrees

- Worktree creation method: `A` (HTTP API `POST /experimental/worktree`)
- Worktree A path: `/home/dev/.local/share/opencode/worktree/56a2ecd18a6610ce515c91f45114dcb65edfb70a/spike-a`
- Worktree B path: `/home/dev/.local/share/opencode/worktree/56a2ecd18a6610ce515c91f45114dcb65edfb70a/spike-b`
- Worktree list output: both directories listed via `/experimental/worktree`; `git worktree list` shows `/workspace`, plus the two opencode worktrees on branches `opencode/spike-a` and `opencode/spike-b`
- Session in worktree A: `success` — model replied "hello from spike-a"
- Session in worktree B: `success` — model replied "hello from spike-b"
- Project table query: one row, `id` = `56a2ecd18a6610ce515c91f45114dcb65edfb70a`, `sandboxes` includes both worktree paths
- Session project_ids: both sessions have the same `project_id` (`56a2ecd18a6610ce515c91f45114dcb65edfb70a`); `/session` API returned `null` values for `project_id` field

**Assessment:** Shared project_id across worktrees is `confirmed`. Sessions are `visible` across worktrees via the database; the `/session` API JSON shape differs from the db query.

## Task 5: SQLite integrity + bind-mount compatibility

- Integrity check: `{"integrity_check": "ok"}`
- Database path: `/home/dev/.local/share/opencode/opencode.db`
- Worktree .git metadata location: `/workspace/.git/worktrees/` inside the VM contains `spike-a` and `spike-b`. Since `/workspace` is a bind mount of the host repo, these entries **also appear in the host repo's `.git/worktrees/`**.
- Git operations in worktree: `success` — `git status` shows clean working tree on branch `opencode/spike-a`; `git log` works
- Host .git modified: `yes` — host `git worktree list` shows both opencode worktrees as `prunable` (paths reference VM-internal `/home/dev/.local/share/opencode/worktree/...` which don't exist on the host); host `.git/worktrees/` contains `spike-a` and `spike-b` directories

**Revised host worktree output (post-VM):**
```
=== host git worktree list ===
/home/ole/projects/inoio/inoio/saife2                                                      66dc9c32b1 [main]
/home/dev/.local/share/opencode/worktree/56a2ecd18a6610ce515c91f45114dcb65edfb70a/spike-a  66dc9c32b1 [opencode/spike-a] prunable
/home/dev/.local/share/opencode/worktree/56a2ecd18a6610ce515c91f45114dcb65edfb70a/spike-b  66dc9c32b1 [opencode/spike-b] prunable
=== host .git/worktrees ===
total 0
drwx------ 1 ole ole  28 Jul 27 13:18 .
drwxrwxr-x 1 ole ole 276 Jul 27 13:18 ..
drwx------ 1 ole ole  82 Jul 27 13:22 spike-a
drwx------ 1 ole ole  82 Jul 27 13:18 spike-b
```

**Assessment:** SQLite integrity is `intact`. Bind-mount compatibility is `confirmed with a caveat`: linked worktrees function inside the VM, but the host repo's `.git/worktrees/` is polluted with entries referencing VM-internal paths. After the VM stops, these entries are `prunable` on the host and require `git worktree prune` cleanup. The launcher should handle this cleanup automatically.
