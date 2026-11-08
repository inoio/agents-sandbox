# Project config trust gate (#229) — Implementation Plan

> **For agentic workers:** Implement task by task with TDD (test first, see it fail, implement, see it pass).
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An unapproved `.agents-sandbox` project layer is never acted on. The commands that turn project input
into VM or image state ask for approval (or refuse without a TTY); every other command warns and runs without the
project layer.

**Spec:** `specs/configuration/project-config-trust.md` (contract and the 2026-11-08 decisions). Related:
`specs/configuration/config-scopes.md` (the scope authority this plan narrows), `specs/state/state-model.md`
(`RootState` transaction, schema bump), `specs/vm/reconfig-decision.md` (change rendering and escaping),
`specs/resources/resource-limit-proposal.md` §4 (launcher writes to the project layer).

## Delivery

| PR | Scope | Closes |
|----|-------|--------|
| 1 (this plan) | Projection, approval state, gate at config resolution, layer exclusion, prompt/refusal, dry-run report, launcher-write approval, docs | the exploitable path of #229 |
| 2 | Consistency check: recompute the projection from the consumed inputs before the image build and before VM create/start; abort on mismatch | #229 |
| later | `config harden` (#118) reports the approval state | — |

## Architecture

```text
PersistentPreRunE
  identity := resolveIdentity(".")                     # CheckoutSlug keys the approval
  current  := projecttrust.Compute(projectInputs)      # project layer alone, read once
  approved := state.ProjectTrust[CheckoutSlug]
  switch projecttrust.Evaluate(current, approved)
    approved / empty layer  → configpaths unchanged
    changed, asking command → interactive: show diff, approve → record │ decline → cancel
                              non-interactive: refuse (names inputs, says how to approve)
                              --dry-run: report, exclude, never prompt or write
    changed, other command  → warn once, exclude
  exclude = configpaths.Get wrapped by WithoutProjectLayer   # ConfigScopes drops ScopeProject,
                                                             # ProjectDockerfile() == ""
  NewResolver(...)                                     # merges only what is allowed
  applyCLISettings(...)                                # yes/quiet/log-level from allowed layers only
```

- The gate lives in `internal/projecttrust`. It imports `configpaths`, `homeconfig`, `envsecret` (secret
  structure), `agent` (config dir names), `state`, and `termio`; nothing it imports may import it back.
- Exclusion is one decorator around the process `configpaths.Get` seam. Every project reader already derives
  its inputs from `configpaths.ConfigScopes` or `ConfigPaths.ProjectDockerfile()` (12 `ConfigScopes` call
  sites, 1 Dockerfile read), so no reader changes.
- Approval is per checkout (`identity.CheckoutSlug`) in `RootState`, shared by all agents.

## Resolved open questions (proposal, confirm in review)

- **Prompt shape:** one block per changed input, headed by its source path, with `- old` / `+ new` entry lines.
  It reuses the reconfiguration change rendering and its control-character escaping (moved to `termio`). It does
  not reuse `config harden` machinery, which does not exist yet.
- **Snippets:** each snippet file is its own input with its content, not the merged output. That matches the
  per-source-path rule, and a snippet edit shows as an edit to that file.

## Global constraints

- `--yes`, a `yes:` key, and `Select`'s non-interactive default can never approve. The gate checks
  `IsInteractive()` itself and uses a prompt path that ignores assume-yes.
- Secret files contribute structure only; no secret value reaches the projection, the state, or the prompt.
- Displayed values are escaped (control and `Cf` characters) before they reach the terminal.
- The user layer is never gated; a user-layer, env, or flag change never invalidates an approval.
- State writes go through `state.WithRootState`; the schema bump follows `TestRootStateSchemaFieldsArePinned`.
- `make fmt`, `make lint`, `make test`, `make deadcode`, `make compile-all` stay green.

## Review focus

1. No path from an unapproved layer to a side effect: assume-yes, quiet, upgrade, prune, env/secrets, agent
   config, `home:`, Dockerfile. Task 7 tests each key that acts before `PrepareSandbox`.
2. No secret value in the projection, state, or prompt. Tasks 2 and 5.
3. A user-layer change does not re-prompt; a project-layer reorder does not re-prompt. Task 2.
4. The launcher's own project-layer write does not re-prompt, and never leaves the file changed without the
   matching approval. Task 8.

---

### Task 1: Exclude the project layer through `configpaths`

**Files:** `internal/configpaths/scopes.go`, `internal/configpaths/configpaths.go`, a new
`internal/configpaths/without_project.go`; tests in the unit-named test files.

- [ ] Test: `ConfigScopes(WithoutProjectLayer(paths), slug)` returns only the user scopes;
      `ProjectDockerfile()` and `ProjectConfigDir()` return `""`; every other accessor delegates.
- [ ] Test: `image.ReadProjectDockerfile` returns nil for an empty Dockerfile path (no `os.ReadFile("")`).
- [ ] Implement the decorator. `ConfigScopes` omits the project scope when `ProjectConfigDir()` is empty.
- [ ] Check `resourceproposal.requireWritableConfigDir` and `viperconfig.LayerSettingOwner` with the decorator:
      no write may target the project layer while it is excluded.

### Task 2: `projecttrust.Compute` — the projection

**Files:** `internal/projecttrust/projection.go` (+ `_test.go`).

**Interfaces:** `type Input struct { Path string; Kind InputKind; Entries []string }`,
`type Projection struct { Inputs []Input }` (sorted by path), `func (Projection) Digest() string`,
`func Compute(sources ProjectSources) (Projection, error)` where `ProjectSources` names the project config dir,
the `/workspace` checkout root, and the agent config dir names to read.

- [ ] Tests, one per input kind:
  - launcher config file: one entry per key the file sets, `key: <canonical JSON value>`, sorted, nested keys
    flattened with `.`; a key order or formatting change keeps the digest.
  - `env`: content entries (non-secret by contract).
  - `env.secret`, `env.secret.yaml`: one structure entry per secret (name, hosts, passthrough, header fields,
    flags); the test asserts the value string appears in no entry.
  - each file under every registered agent's config dir names (primary and aliases): content entries.
  - `home:` sources whose **resolved** path lies in the project config dir or the checkout, however spelled
    (relative, absolute, `~/…`), hook scripts included: content entries; a source outside both adds no input.
  - the project Dockerfile: content entries.
  - a large (> 64 KiB) or non-UTF-8 file: a single `sha256:<hex>` entry.
- [ ] Test: an empty or absent project dir yields an empty projection.
- [ ] Test: a moved file (same content, new path) changes the digest.
- [ ] Implement. Read each file once through `internal/configread`'s bound (16 MiB, ADR 0001).

### Task 3: Approval state in `RootState`

**Files:** `internal/sandbox/state/root.go`, `schema.go`, `schema_test.go` (+ `root_test.go`).

- [ ] Test: `RootState.ProjectTrust map[string]ApprovedProjection` (`project_trust`, keyed by checkout slug,
      holding `digest` and `inputs`) round-trips; other `RootState` fields are preserved by a write.
- [ ] Bump `RootStateSchema` to 2 and pin the new field set.
- [ ] Implement `ApprovedProjection(slug)` / `RecordApprovedProjection(slug, projection)` on top of
      `WithRootState`.

### Task 4: `projecttrust.Evaluate` — the decision

**Files:** `internal/projecttrust/evaluate.go` (+ `_test.go`).

- [ ] Tests: empty projection → approved; equal digest → approved; no record → changed with every input as
      added; differing digest → one change per added, removed, or modified input, each with `- old` / `+ new`
      entry lines.

### Task 5: Prompt, refusal, and rendering

**Files:** `internal/termio` (escaping helper moved from `reconfig.displayValue`), `internal/projecttrust/prompt.go`
(+ `_test.go`), `internal/sandbox/reconfig/plan.go` (use the moved helper).

- [ ] Test: the moved escaping keeps `TestChangeListEscapesControlCharacters` green in `reconfig`.
- [ ] Tests: an interactive UI shows every change (escaped) and records approval on yes; no records on no and
      returns a cancellation error; assume-yes set → still asks; a non-interactive UI → refusal error naming
      each changed input and the remedy, with no prompt.

### Task 6: Command classes

**Files:** `cmd/agents-sandbox/commands.go` (+ `cli_*_test.go`).

- [ ] Test: `commandAppliesProjectConfig(cmd)` is true for the root default command, `run`, `shell`, `build`,
      and `image build`, and false for every other command in the tree (walk the real tree so a new command
      must be classified).

### Task 7: The gate in `PersistentPreRunE`

**Files:** `cmd/agents-sandbox/commands.go`, a new `cmd/agents-sandbox/trust.go`; CLI tests in
`cmd/agents-sandbox/cli_trust_test.go`.

- [ ] CLI tests (mock config paths with a project dir and a TTY/non-TTY UI):
  - approved projection → project values in effect, no prompt.
  - changed + `run`, interactive yes → recorded, values in effect; no → cancellation, nothing recorded,
    no runtime prepared.
  - changed + `run`, non-interactive or `--serve-only` → refusal error, no side effect.
  - changed + `run --yes` → still asks.
  - changed + `run --dry-run` → report, no prompt, no state write, preview without the project layer.
  - changed + `config paths` (any other command) → one warning, project values not in effect.
  - each early-acting key from an unapproved layer has no effect: `yes`, `quiet`, `log-level`,
    `upgrade.mode`/`upgrade.channel`, `auto-prune-age`.
- [ ] Implement: compute after `resolveIdentity`, decide, swap `configpaths.Get` for the excluded case, then
      `NewResolver` and `applyCLISettings` as today.

### Task 8: Launcher writes keep their approval

**Files:** `internal/sandbox/resourceproposal/proposal.go` (+ `_test.go`).

- [ ] Test: `PersistSetting` into the project layer records the resulting projection, so the next gate
      evaluation is approved; a failed state write restores the previous file content.
- [ ] Implement the write and the projection update as one operation.

### Task 9: Docs, changelog, brain

- [ ] User docs: a section on project config approval (what is gated, the prompt, the refusal and how to
      approve, the ignore warning), linked from the security/trust pages; `README.md` boundary note.
- [ ] `changelog.d/<pr>.changed.md` (#229).
- [ ] Specs: update `project-config-trust.md` (Provenance names `projecttrust`; Open questions resolved),
      `config-scopes.md` (the excluded project scope), `resource-limit-proposal.md` §4 (no longer "until #229").
- [ ] Catalog entries; delete this plan once PR 2 has landed.
