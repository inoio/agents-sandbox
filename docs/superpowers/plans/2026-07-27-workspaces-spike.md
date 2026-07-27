# Workspaces Validation Spike Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate that opencode's experimental workspaces feature delivers shared session history + parallel branch isolation within one VM, de-risking the one-VM-per-project design.

**Architecture:** Run `opencode-msb shell` with `OPENCODE_EXPERIMENTAL_WORKSPACES=true` injected via `.opencode-msb/env`. Inside the single VM shell, start an `opencode serve` daemon, discover the worktree control surface, create worktrees for two branches, verify shared project_id, SQLite integrity, and bind-mount compatibility. Record findings to a notes file.

**Tech Stack:** opencode 1.18.5 (installed in runner image via `opencode.ai/install`), microsandbox 0.6.7, existing opencode-msb tooling, `jq` + `curl` (in runner image).

## Global Constraints

- `.opencode-msb/` is git-ignored — the spike env file will not be committed.
- The runner image is built from `internal/sandbox/data/Dockerfile` (Debian trixie-slim + opencode + Node.js 26 + common CLI tools including `jq`, `curl`, `git`, `ripgrep`).
- The VM user is `dev` (non-root) — cannot `apt-get install` packages inside the VM.
- `opencode-msb shell` creates a sandbox, attaches an interactive bash shell, and on exit stops+removes the sandbox. All spike work happens within one shell session.
- `/workspace` inside the VM is a bind mount of the host repo (the opencode-msb repo itself when run from `/workspace`).
- `/home/dev` inside the VM is a named home volume (persists opencode state across sessions for the same project+image).
- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Code style: self-explanatory code, minimal abstractions, no inline comments unless code is not self-explanatory.

---

## File Structure

| File | Responsibility | Action |
|------|---------------|--------|
| `.opencode-msb/env` | Injects `OPENCODE_EXPERIMENTAL_WORKSPACES=true` into the sandbox environment | Create (temporary, git-ignored) |
| `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` | Spike findings document — the deliverable | Create |
| Git branches `spike-a`, `spike-b` | Test branches for worktree creation | Create (temporary, cleaned up in Task 6) |

No Go source files are modified. This is a validation spike, not an implementation.

---

### Task 1: Spike setup — env file, test branches, findings scaffold

**Files:**
- Create: `.opencode-msb/env` (git-ignored)
- Create: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md`
- Create: git branches `spike-a`, `spike-b` on the host repo

**Interfaces:**
- Consumes: nothing (foundational setup)
- Produces: `.opencode-msb/env` with the workspaces env var (consumed by `opencode-msb shell` in Task 2); `spike-a`/`spike-b` branches (consumed by worktree creation in Task 4); findings doc scaffold (appended to in all subsequent tasks)

**Background:** The `.opencode-msb/env` file is read by `buildEnvMap` (`internal/sandbox/runner.go:208`) and merged into the sandbox environment via `msb.WithEnv` (`internal/sandbox/runner.go:695-696`). Any `KEY=value` lines in this file become environment variables inside the VM. This is the cleanest way to inject the experimental flag without modifying Go source. The findings doc is the spike deliverable — each subsequent task appends its results.

- [ ] **Step 1: Create `.opencode-msb/env` with the workspaces flag**

```bash
cat > .opencode-msb/env << 'EOF'
OPENCODE_EXPERIMENTAL_WORKSPACES=true
EOF
```

- [ ] **Step 2: Create test branches on the host repo**

```bash
git branch spike-a HEAD
git branch spike-b HEAD
```

- [ ] **Step 3: Scaffold the findings document**

Create `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md`:

```markdown
# Workspaces Validation Spike — Findings

**Date:** 2026-07-27
**Spec:** `docs/superpowers/specs/2026-07-27-one-vm-per-project-design.md` (Section 1)
**opencode version:** (to be filled in Task 2)
**msb version:** 0.6.7

## Summary

(To be filled in Task 6 — go/no-go recommendation for the one-VM-per-project design.)

## Task 2: opencode version + workspaces support

(To be filled in.)

## Task 3: Serve daemon + worktree control surface

(To be filled in.)

## Task 4: Shared project_id across worktrees

(To be filled in.)

## Task 5: SQLite integrity + bind-mount compatibility

(To be filled in.)
```

- [ ] **Step 4: Commit the findings scaffold**

```bash
git add docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md
git commit -m "add workspaces spike findings scaffold"
```

---

### Task 2: Launch VM + confirm opencode version + workspaces support

**Files:**
- Modify: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` (append findings)

**Interfaces:**
- Consumes: `.opencode-msb/env` from Task 1
- Produces: recorded opencode version + workspaces env-var recognition status in the findings doc

**Background:** `opencode-msb shell` builds the runner image (if not cached), creates the home volume, creates the sandbox with env from `.opencode-msb/env`, and attaches an interactive bash shell. Inside that shell, we confirm the opencode version and whether the experimental workspaces flag is recognized. The image installs opencode via `curl -fsSL https://opencode.ai/install | bash` (Dockerfile line 8), so the version is whatever was latest at image-build time.

- [ ] **Step 1: Launch the VM shell**

Run from the repo root (`/workspace`):

```bash
opencode-msb shell
```

This drops you into a bash shell inside the VM. The prompt should show `dev@` with a hostname. The working directory should be `/workspace` with the repo contents visible.

- [ ] **Step 2: Confirm opencode version**

Inside the VM shell:

```bash
opencode --version
```

Record the version. Expected: a version string like `1.18.5` or similar.

- [ ] **Step 3: Confirm the workspaces env var is set**

```bash
echo "OPENCODE_EXPERIMENTAL_WORKSPACES=$OPENCODE_EXPERIMENTAL_WORKSPACES"
```

Expected: `OPENCODE_EXPERIMENTAL_WORKSPACES=true`. If empty, the env injection failed — check that `.opencode-msb/env` exists on the host and has the right content.

- [ ] **Step 4: Check if opencode recognizes the workspaces flag**

Check the CLI help for any worktree-related commands:

```bash
opencode --help 2>&1 | grep -i worktree || echo "no worktree subcommand in help"
```

Also check if the env var appears in experimental flags documentation:

```bash
opencode --help 2>&1 | grep -i workspace || echo "no workspace mention in help"
```

- [ ] **Step 5: Append findings to the findings doc**

From within the VM, write to the bind-mounted findings file:

```bash
cat >> /workspace/docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md << 'EOF'

### opencode version

- Version: `<record from step 2>`
- `OPENCODE_EXPERIMENTAL_WORKSPACES` env var: `<record from step 3 — true or empty>`
- CLI help mentions worktree: `<record from step 4 — yes/no>`
- CLI help mentions workspace: `<record from step 4 — yes/no>`

**Assessment:** The workspaces flag is `<recognized / not recognized>`. Proceeding to serve + control surface discovery.
EOF
```

Replace the `<...>` placeholders with the actual observed values before moving on.

- [ ] **Step 6: Do NOT exit the shell yet**

The VM shell must stay open for Tasks 3–5. All subsequent tasks run inside this same shell session.

---

### Task 3: Start serve daemon + discover worktree control surface

**Files:**
- Modify: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` (append findings)

**Interfaces:**
- Consumes: the running VM shell from Task 2
- Produces: a running `opencode serve` daemon (consumed by Tasks 4–5); recorded worktree control surface commands (consumed by Task 4)

**Background:** The design's architecture (spec Section 2) relies on `opencode serve` running as a daemon that owns the SQLite db + workspaces control-plane. This task starts that daemon and discovers how to programmatically create worktrees. The opencode server publishes an OpenAPI 3.1 spec at `/doc` — this is the most reliable way to discover available API endpoints, including any undocumented worktree endpoints. The worktree module exists in opencode's source (`packages/opencode/src/worktree/index.ts`) with `create`/`list`/`remove`/`reset` functions, but how they're exposed (API endpoint, CLI command, or TUI slash command) is unknown — this task discovers that.

- [ ] **Step 1: Start the opencode serve daemon in the background**

Inside the VM shell:

```bash
nohup opencode serve --hostname 127.0.0.1 --port 4096 > /tmp/opencode-serve.log 2>&1 &
echo "serve PID: $!"
```

- [ ] **Step 2: Wait for the daemon to be healthy**

Poll the health endpoint:

```bash
for i in $(seq 1 15); do
  if curl -sf http://127.0.0.1:4096/global/health > /dev/null 2>&1; then
    echo "healthy after ${i}s"
    break
  fi
  sleep 1
done
curl -s http://127.0.0.1:4096/global/health | jq .
```

Expected: `{"healthy": true, "version": "<version>"}`. If it never becomes healthy, check `/tmp/opencode-serve.log` for errors.

- [ ] **Step 3: Discover API endpoints from the OpenAPI spec**

```bash
curl -s http://127.0.0.1:4096/doc | jq '.paths | keys[]' | sort
```

Look for any paths containing "worktree", "workspace", or "sandbox". Record all paths. If the `/doc` endpoint returns HTML (not JSON), try:

```bash
curl -s http://127.0.0.1:4096/doc -H "Accept: application/json" | jq '.paths | keys[]' | sort
```

- [ ] **Step 4: Check for a worktree CLI subcommand**

```bash
opencode worktree --help 2>&1 || echo "no worktree subcommand"
```

- [ ] **Step 5: Check the current project**

```bash
curl -s http://127.0.0.1:4096/project/current | jq .
```

Record the `id` and `worktree` fields. The `id` is the `project_id` — this is what should be shared across worktrees if the workspaces feature works. The `worktree` should be `/workspace`.

- [ ] **Step 6: Append findings to the findings doc**

```bash
cat >> /workspace/docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md << 'EOF'

### Serve daemon + worktree control surface

- Serve daemon: `<healthy / failed — record details>`
- Health response: `<record from step 2>`
- All API paths: `<record from step 3>`
- Worktree API endpoints found: `<list any, or "none found">`
- Worktree CLI subcommand: `<yes/no — record output>`
- Current project_id: `<record from step 5>`
- Current project worktree: `<record from step 5>`

**Control surface for creating worktrees:** `<record the exact command(s) to create a worktree — API endpoint, CLI command, or "TUI slash command only">`
EOF
```

Replace `<...>` with actual values. The "Control surface" line is critical — Task 4 uses whatever command/API was discovered here.

---

### Task 4: Create worktrees for two branches + verify shared project_id

**Files:**
- Modify: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` (append findings)

**Interfaces:**
- Consumes: the running serve daemon from Task 3; the discovered worktree control surface commands from Task 3; the `spike-a`/`spike-b` branches from Task 1
- Produces: recorded worktree creation status, project_id sharing verification, and session visibility across worktrees

**Background:** This is the core validation. opencode's worktree module (`packages/opencode/src/worktree/index.ts`) creates linked git worktrees under `~/.local/share/opencode/worktree/<project-id>/<name>`, registers each as a "sandbox" of the same project via `project.addSandbox(ctx.project.id, info.directory)`, and boots a separate opencode instance per worktree. If this works correctly, both worktrees should resolve to the **same `project_id`**, and sessions created in one worktree should be visible from the other. The exact command to create a worktree was discovered in Task 3, Step 6 — use whatever was found there.

- [ ] **Step 1: Create a worktree for branch `spike-a`**

Use the control surface discovered in Task 3. The most likely paths (try in order):

**Path A — if an API endpoint exists** (e.g., `POST /worktree` or similar):

```bash
curl -s -X POST http://127.0.0.1:4096/worktree \
  -H "Content-Type: application/json" \
  -d '{"name":"spike-a","branch":"spike-a"}' | jq .
```

(Adjust the path and body based on the OpenAPI spec from Task 3, Step 3.)

**Path B — if a CLI subcommand exists**:

```bash
opencode worktree create --name spike-a --branch spike-a 2>&1
```

(Adjust flags based on `opencode worktree --help` from Task 3, Step 4.)

**Path C — manual git worktree** (fallback if no API/CLI):

```bash
git worktree add -b opencode/spike-a /home/dev/spike-a spike-a
```

Then check if the serve daemon recognizes it:

```bash
curl -s http://127.0.0.1:4096/project/current | jq .sandboxes
```

Record which path worked and the output.

- [ ] **Step 2: Create a worktree for branch `spike-b`**

Repeat Step 1 with `spike-b` instead of `spike-a`.

- [ ] **Step 3: List worktrees**

```bash
# If API:
curl -s http://127.0.0.1:4096/worktree | jq .
# Or if CLI:
opencode worktree list 2>&1
# Or raw git:
git worktree list
```

Record the output. Confirm both worktrees exist.

- [ ] **Step 4: Send a message to a session in worktree A**

Use `opencode run` in non-interactive mode, targeting the worktree A directory:

```bash
opencode run --attach http://127.0.0.1:4096 --dir /home/dev/spike-a "Reply with exactly: hello from spike-a" 2>&1 | tail -5
```

(If the worktree directory is different — e.g., under `~/.local/share/opencode/worktree/<project-id>/spike-a` — use that path instead. Check the worktree list output from Step 3 for the exact path.)

Record the session. If this fails, try starting the TUI instead:

```bash
opencode attach http://127.0.0.1:4096 --dir /home/dev/spike-a
```

(Type a message, observe, then exit the TUI with Ctrl+C.)

- [ ] **Step 5: Send a message to a session in worktree B**

```bash
opencode run --attach http://127.0.0.1:4096 --dir /home/dev/spike-b "Reply with exactly: hello from spike-b" 2>&1 | tail -5
```

- [ ] **Step 6: Verify shared project_id**

Query the project table:

```bash
opencode db "SELECT id, worktree, sandboxes FROM project" --format json | jq .
```

Expected: one project row with the same `id` as Task 3's current project_id, and `sandboxes` listing both worktree directories.

- [ ] **Step 7: Verify sessions are visible across worktrees**

List all sessions:

```bash
opencode db "SELECT id, project_id, directory FROM session ORDER BY time_created DESC LIMIT 10" --format json | jq .
```

Expected: both sessions (from steps 4 and 5) have the **same `project_id`**. If `project_id` differs, the workspaces feature is NOT sharing history — this is a blocking finding for the design.

Also try the session API:

```bash
curl -s http://127.0.0.1:4096/session | jq '.[].project_id'
```

- [ ] **Step 8: Append findings to the findings doc**

```bash
cat >> /workspace/docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md << 'EOF'

### Shared project_id across worktrees

- Worktree creation method: `<which path worked — A/B/C>`
- Worktree A path: `<record>`
- Worktree B path: `<record>`
- Worktree list output: `<record from step 3>`
- Session in worktree A: `<success/failed — record output>`
- Session in worktree B: `<success/failed — record output>`
- Project table query: `<record from step 6>`
- Session project_ids: `<record from step 7 — are they the same?>`

**Assessment:** Shared project_id across worktrees is `<confirmed / NOT confirmed>`. Sessions are `<visible / NOT visible>` across worktrees.
EOF
```

---

### Task 5: Verify SQLite integrity + bind-mount compatibility

**Files:**
- Modify: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` (append findings)

**Interfaces:**
- Consumes: the running serve daemon + worktrees from Task 4
- Produces: recorded SQLite integrity check result, worktree `.git` metadata location, and bind-mount compatibility assessment

**Background:** The design's root corruption fix relies on one VM/kernel owning the SQLite db (fcntl locks work within one kernel). This task verifies the db is intact after concurrent worktree use. Additionally, the design bind-mounts the host repo at `/workspace` — opencode's worktree module creates linked worktrees from the project's primary worktree, which means `.git/worktrees/` metadata would land in the bind-mounted `.git`. This task checks whether that works (the host `.git` is shared across worktrees) or whether it causes issues.

- [ ] **Step 1: Run SQLite integrity check**

```bash
opencode db "PRAGMA integrity_check" --format json | jq .
```

Expected: `{"integrity_check": "ok"}`. If not `ok`, the db is corrupt — record the full output.

- [ ] **Step 2: Find the database path**

```bash
opencode db path
```

Record the path. It should be under `/home/dev/.local/share/opencode/` or similar.

- [ ] **Step 3: Check where worktree `.git` metadata lives**

The host repo's `.git` is at `/workspace/.git` (bind-mounted). opencode's worktrees are "linked" — they share the main repo's `.git` directory. Check:

```bash
ls -la /workspace/.git/worktrees/ 2>/dev/null || echo "no .git/worktrees in /workspace"
ls -la /home/dev/.local/share/opencode/worktree/ 2>/dev/null || echo "no opencode worktree dir"
```

Record what you find. The key question: did opencode create worktree entries in the bind-mounted `/workspace/.git/worktrees/`, or somewhere else (e.g., under `/home/dev`)?

- [ ] **Step 4: Verify git operations work in a worktree**

Pick one worktree directory (from Task 4, Step 3) and check git status:

```bash
cd <worktree-a-path>
git status
git log --oneline -3
cd /workspace
```

Expected: git operations work normally in the worktree. If the worktree's `.git` points to the bind-mounted `/workspace/.git/worktrees/<name>`, this confirms bind-mount compatibility.

- [ ] **Step 5: Check if host-side `.git` was modified**

From a **second host terminal** (not the VM shell), check:

```bash
git worktree list
ls -la .git/worktrees/ 2>/dev/null
```

Record whether the host repo's `.git` now has worktree entries. This is expected if opencode creates linked worktrees from the bind-mounted repo. If it causes issues (e.g., host git gets confused), record that.

- [ ] **Step 6: Append findings to the findings doc**

```bash
cat >> /workspace/docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md << 'EOF'

### SQLite integrity + bind-mount compatibility

- Integrity check: `<record from step 1>`
- Database path: `<record from step 2>`
- Worktree .git metadata location: `<record from step 3 — /workspace/.git/worktrees or elsewhere>`
- Git operations in worktree: `<success/failed — record from step 4>`
- Host .git modified: `<yes/no — record from step 5>`

**Assessment:** SQLite integrity is `<intact / corrupt>`. Bind-mount compatibility is `<confirmed / problematic — record issues>`.
EOF
```

---

### Task 6: Compile findings + cleanup

**Files:**
- Modify: `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md` (write summary)
- Delete: `.opencode-msb/env` (temporary spike file)
- Delete: git branches `spike-a`, `spike-b` (temporary test branches)

**Interfaces:**
- Consumes: all findings from Tasks 2–5
- Produces: a finalized findings document with a go/no-go recommendation; clean repo state (no spike artifacts)

**Background:** This task compiles the spike findings into a summary with a go/no-go recommendation for the one-VM-per-project design. The spec (Section 1) says: "If shared-history or bind-mount compatibility fails, fall back to Approach 3 (per-invocation, no daemon) or revisit." The findings document is the decision input. After writing the summary, the spike env file and test branches are cleaned up.

- [ ] **Step 1: Exit the VM shell**

Inside the VM shell:

```bash
exit
```

This stops and removes the sandbox (via `opencode-msb shell`'s cleanup). The home volume persists.

- [ ] **Step 2: Write the summary to the findings doc**

On the host, edit `docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md`. Replace the `## Summary` placeholder at the top with the go/no-go assessment. Use this template, filling in based on the findings:

```markdown
## Summary

**Go/No-Go recommendation:** `<GO / NO-GO / GO WITH CAVEATS>`

**Rationale:** Based on the spike findings:

1. **opencode version + workspaces support:** `<one sentence — is the flag recognized?>`
2. **Worktree control surface:** `<one sentence — is there a programmatic way to create worktrees?>`
3. **Shared project_id:** `<one sentence — do worktrees share the same project_id?>`
4. **SQLite integrity:** `<one sentence — is the db intact after concurrent use?>`
5. **Bind-mount compatibility:** `<one sentence — do linked worktrees work with a bind-mounted /workspace?>`

**Blockers found:** `<list any, or "none">`

**Caveats for the implementation plan:** `<list any — e.g., "worktrees must be created via API, not CLI" or "bind-mount requires cloning .git into /home/dev first">`

**Recommendation for next steps:** `<proceed to full implementation plan / revise design / fall back to Approach 3>`
```

- [ ] **Step 3: Clean up the spike env file**

```bash
rm .opencode-msb/env
```

- [ ] **Step 4: Clean up test branches**

```bash
git branch -D spike-a spike-b
```

- [ ] **Step 5: Clean up any host-side git worktree entries**

If Task 5, Step 5 found worktree entries in the host repo's `.git`:

```bash
git worktree prune
git worktree list
```

Confirm no stale worktree entries remain.

- [ ] **Step 6: Commit the finalized findings doc**

```bash
git add docs/superpowers/notes/2026-07-27-workspaces-spike-findings.md
git commit -m "add workspaces spike findings — <GO/NO-GO>"
```

Replace `<GO/NO-GO>` with the actual recommendation.

---

## Known Limitations

1. **Single shell session:** All spike work happens within one `opencode-msb shell` session. If the TTY drops (network issue, terminal crash), the VM is cleaned up and the spike must restart from Task 2. Findings recorded to the bind-mounted `/workspace` persist on the host.

2. **Exploratory control surface:** The exact commands to create worktrees (Task 4) depend on what Task 3 discovers. If no programmatic control surface exists (API or CLI), the spike falls back to manual `git worktree add` — but this may not trigger opencode's worktree registration (`project.addSandbox`), in which case shared project_id won't work. This itself is a finding (the workspaces feature may not be externally controllable yet).

3. **opencode version drift:** The runner image installs opencode via `opencode.ai/install` at build time. If the image was built days ago, the version might differ from the host's `1.18.5`. Rebuild with `opencode-msb build -r` if the version is too old to support workspaces.

4. **No code changes:** This spike validates the design without modifying Go source. The implementation plan (written after the spike) will contain the actual code changes.
