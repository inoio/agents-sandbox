# S7 — `run --learn` and the after-run

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the learn-mode engine S4/S5/S6 landed into the run lifecycle: a `run --learn` session starts the observation channel, applies its overlay before the VM is prepared, records a baseline, and at teardown analyses the session and applies the proposal in this process; `config learn` replays the analysis for a session whose owning process is gone; and every temporary `//deadcode:ignore` marker S4/S5/S6 left is deleted.

**Architecture:** A new `internal/sandbox/learn` package owns the invocation (the channel `Begin`/`Overlay`/`Stop` facade, the baseline capture, and the after-run) and depends on `channel`, `readback`, `analyser`, `state`, `configpaths`, `network`, `options`, `termio`. `session.Run` calls it around the existing `prepareSandbox`/`runAttach` flow: `Begin` and the overlay before prepare, `CaptureBaseline` after prepare and before attach, `Finish` after attach. The session record lives in the per-`(checkout, agent)` `state.HomeState` (a schema bump), so `config learn` can rebuild the analysis from a later process. `config learn` shares `learn.Analyse` with the live path. The agent-config back-copy is **not** here — it is subplan **S10** (`2026-11-17-agent-config-writeback.md`), which extends `learn.Analysis` and therefore follows S7.

**Tech Stack:** Go 1.27, `internal/sandbox/{learn,channel,readback,analyser,state,session,options,network}`, `internal/{runoptions,configpaths,termio}`, `cmd/agents-sandbox`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode.md` (journeys, the after-run, invariants 1/3/6) and `.operator-shared/specs/onboarding/learn-mode/findings-and-apply.md` (§Attribution and the network diff, §Applying, §After-run ownership); `.operator-shared/specs/onboarding/learn-mode/observation-channel.md` (§Observation requires the policy at creation); `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (§Baseline manifest). The frozen cross-subplan contracts are in `2026-11-17-learn-mode-program.md`.

**Produces (for S8/S9/S10 and the program index):** `options.RunOptions.Learn`; `state.LearnSession` + `RecordLearnSession`/`ReadLearnSession`/`ClearLearnSession` (a `HomeState` field, `HomeStateSchema` bump); `learn.{Run,Begin,Passive,Analyse,Analysis,FinishInput,Summary,PruneArtefacts}`; `analyser.ApplyOptions.AcceptAll`; `channel.Delete`; the `config learn` command and the `--learn` flag.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:

- **`internal/sandbox/msb` is the only package that imports the microsandbox SDK.** `learn` reaches volumes only through `readback.HomeVolumePath`.
- **No value ever reaches state, a finding, a decline record, a log, or the artefact.** The static-secret value is threaded in memory from `readback.Credential.Value` through `ApplyOptions.SecretValue` to the writer (hub invariant 3). The `LearnSession` record holds paths and a manifest digest, never a value.
- **Propose, never apply silently** (invariant 1). Every finding is applied only after an explicit acceptance; a non-interactive session writes nothing and prints one line (invariant 6).
- **The proposal never removes and never widens** (invariant 4). The after-run calls `analyser.ObservationFindings`, whose writes are `ScopePerSlugUser` and non-removing.
- **The after-run is not hooked to VM detach, the reaper, or the idle timeout.** It runs in the process that started the session, after the attach returns; a session whose process dies leaves its `LearnSession` record for `config learn`.
- Host-only verification: the real streaming turn through the proxy and any `msb`-tagged run are a **human accept** step (see the plan's final task). `verification-before-completion` stops at the PR.
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.

## Design decisions (calls made; correct if wrong)

1. **The never-widen baseline is the pre-overlay policy.** `ObservationFindings` compares the proposed allow set against `SessionFacts.Current`. That must be `opts.Network` *before* the channel overlay is applied: the overlay injects the proxy address and upstream CA, and feeding it back would propose the channel's temporary settings as the user's policy. `session.Run` captures `base := opts.Network` before `learn.Begin` and passes it as `Current`.
2. **A failed channel is passive, not fatal.** `learn.Begin` returns a `*Run` and an error; on error the caller warns and uses `learn.Passive`, whose `Overlay` returns the base unchanged and whose `Finish` runs a read-back-only analysis. This matches "a passive-read-back session establishes facts 1–3".
3. **A passive session still proposes secrets, never a network change — and the spec says otherwise.** `observation-channel.md` and `home-read-back.md` say a passive session "proposes no network or secret-substitution change", but `findings-and-apply.md`'s attribution rule falls back to the read-back `Credential.Hosts`, and S6's `ObservationFindings.secretFindings` implements exactly that. S7 follows the implementation (secrets are proposed from `Credential.Hosts`; the network finding needs destinations and is absent) and corrects the two spec lines in Task 9.
4. **The session record is a `HomeState` field, not `RootState`.** It is per `(checkout slug, agent)` and names the volume it was taken against; `HomeState` already carries `HomeVolume`. The baseline manifest is mirrored into a state-local entry type because `state` must not import `readback` (`readback` imports `state`); the converter lives in `learn`.
5. **The `config-entry` fallback keeps the passive path useful.** No new decision — recorded to avoid re-litigating "passive proposes nothing".
6. **The exit prompt is S7's.** The hub's vocabulary fixes it: a one-line summary then *Review and save* / *Save all* / *Not now*. *Review and save* drives `analyser.Apply` per finding; *Save all* drives it with a new `ApplyOptions.AcceptAll`; *Not now* writes nothing and records nothing.
7. **`--learn` is incompatible with `--serve-only`** (a learning session attaches a TUI). It is rejected at option resolution. `--dry-run` returns before the channel starts; `--dry-run-vm` prepares and skips the VM, so the after-run does not run (the record is not written).
8. **No new "decline the recreate" branch.** The overlay makes the network fingerprint differ, so an existing VM recreates through the normal `ResolveReconfig` path (alone-apply warning, or keep/quit prompt when other clients are attached). A user who quits the run ends it; S7 does not add a "no recreate, continue passively" path.
9. **The `--learn` flag does not enter the config key registry.** It is a run flag only (like `--serve-only`), not a launcher-config key, so no `viperconfig.Config` field and no `config-keys.md` entry.
10. **The agent-config back-copy is S10.** S7 ships UC2's secret and network proposal end-to-end and leaves the changed-agent-config proposal to S10; the after-run's `Summary` counts only what S7 proposes.

## Review Focus

1. **The channel overlay leaking into the proposal** — the network finding must diff against the user's pre-overlay policy, never propose the proxy address or the upstream CA as egress. (Task 3, Task 4.)
2. **A killed or quit-early session** — the `LearnSession` record must be written before attach and survive, so `config learn` can replay it; a normal teardown must clear it. (Task 2, Task 3, Task 5, Task 6.)
3. **A home volume recreated since the recorded session** — `config learn` must report and discard, never analyse a different volume against a stale baseline. (Task 6.)
4. **Non-interactive / `--yes`** — the summary, the exit prompt, and the per-finding prompt must all write nothing and never hang; `--yes` must not answer a `Select` (invariant 6, the trust-gate rule). (Task 5.)
5. **A secret value reaching state or the artefact** — the `LearnSession` record and the saved artefact carry no value; the value crosses only in memory. (Task 2, Task 3, Task 5.)
6. **A passive session** — no network finding, and `flags 4–7` absent, yet no crash and no misleading "0 destinations" proposal. (Task 3, Task 4.)
7. **`--learn` with `--dry-run`, `--dry-run-vm`, or `--serve-only`** — no channel, no VM mutation, no hang; rejected or a no-op exactly as decided. (Task 1, Task 3.)
8. **A stale `//deadcode:ignore` marker** — after wiring, `make deadcode` must be green; a marker left on a now-reachable declaration fails the gate. (Task 8.)

---

### Task 1: The `--learn` flag and `RunOptions.Learn`

**Files:**
- Modify: `internal/sandbox/options/options.go`, `internal/runoptions/runoptions.go`, `internal/runoptions/runoptions_test.go`
- Modify: `cmd/agents-sandbox/constants.go`, `cmd/agents-sandbox/commands_cli.go`, `cmd/agents-sandbox/cli_run_options_test.go`

**Interfaces:**
- Consumes: `runoptions.Build`, `options.RunOptions`.
- Produces: `options.RunOptions.Learn bool`; `runoptions.FlagLearn = "learn"`; `runoptions.Build` sets `opts.Learn` from the flag and rejects `--learn --serve-only`.

- [ ] **Step 1: Write the failing test**

```go
// runoptions: the flag reaches RunOptions, and the incompatible pair is refused.
func TestBuildResolvesLearn(t *testing.T)      // --learn -> opts.Learn true; absent -> false
func TestBuildRejectsLearnWithServeOnly(t *testing.T) // --learn --serve-only -> error naming both flags
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/runoptions/ -run TestBuildResolvesLearn`. Expected: FAIL.

- [ ] **Step 3: Implement** the flag: add `FlagLearn` to the exported flag-name block, read it in `resolveFlags`, add `Learn bool` to `RunOptions` (a comment: a learning session; never from config), register it in `registerRunFlags` with help `"let agents-sandbox learn your config from this session"`, add `flagLearn` to `constants.go`, and reject `--learn` with `--serve-only` in `resolveAgentAndValidate` (or a sibling validator) with a message naming both.

- [ ] **Step 4: Run** — `go test ./internal/runoptions/ ./internal/sandbox/options/`. Expected: PASS. Then the CLI test asserts `--help` shows the `--learn` line and `agents-sandbox run --learn --dry-run` prints the preview without starting the channel.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/options/ internal/runoptions/ cmd/agents-sandbox/
git commit -m "feat(run): add the --learn flag"
```

---

### Task 2: The learn-session record in state

**Files:**
- Create: `internal/sandbox/state/learn_session.go`, `internal/sandbox/state/learn_session_test.go`
- Modify: `internal/sandbox/state/state.go` (the `HomeState` field), `internal/sandbox/state/schema.go` (`HomeStateSchema`), the pinned-field test

**Interfaces:**
- Consumes: `state.WithHomeState`, `ReadState`.
- Produces:

```go
// LearnSession records what the after-run needs to analyse a session later, when
// the owning process is gone. It holds paths, a timestamp, and a manifest — never
// a value.
type LearnSession struct {
    StartedAt    time.Time            `yaml:"started_at"`
    HomeVolume   string               `yaml:"home_volume"`
    ArtefactPath string               `yaml:"artefact_path,omitempty"`
    Baseline     []LearnManifestEntry `yaml:"baseline,omitempty"`
}
// LearnManifestEntry mirrors readback.ManifestEntry in the state package, which
// cannot import readback (readback imports state).
type LearnManifestEntry struct {
    Key     string    `yaml:"key"`
    Size    int64     `yaml:"size,omitempty"`
    ModTime time.Time `yaml:"mod_time,omitempty"`
    Hash    string    `yaml:"hash,omitempty"`
}
// HomeState gains: Learn *LearnSession `yaml:"learn,omitempty"`
func RecordLearnSession(k Key, s LearnSession) error // write, preserving other HomeState fields
func ReadLearnSession(k Key) (LearnSession, bool, error) // false when no record or no state
func ClearLearnSession(k Key) error // no-op when absent
```

- [ ] **Step 1: Write the failing test**

```go
func TestLearnSessionRoundTrips(t *testing.T)          // Record then Read returns the same baseline; a second Record replaces it
func TestLearnSessionAbsentIsFalse(t *testing.T)       // no state / no record -> (zero, false, nil)
func TestClearLearnSessionIsNoOpWhenAbsent(t *testing.T)
func TestLearnSessionDroppedFromYAMLWhenAbsent(t *testing.T) // omitempty
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/state/ -run TestLearnSession`. Expected: FAIL.

- [ ] **Step 3: Implement** the type, the three accessors through `WithHomeState`, the `HomeState.Learn` field, and bump `HomeStateSchema` by one (update `schema_test.go` and any pinned-shape test).

- [ ] **Step 4: Run** — `go test ./internal/sandbox/state/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/
git commit -m "feat(state): record a learn session for the after-run"
```

---

### Task 3: `learn.Begin`, the overlay, and the baseline

**Files:**
- Create: `internal/sandbox/learn/run.go`, `internal/sandbox/learn/run_test.go`, `internal/sandbox/learn/main_test.go`
- Modify: `internal/sandbox/session/run.go`, `internal/sandbox/session/run_test.go` (the prepare seam already exists)

**Interfaces:**
- Consumes: `channel.StartObservation`, `channel.Observer`; `readback.HomeVolumePath`, `readback.TakeManifest`, `readback.Manifest`; `state.RecordLearnSession`, `state.Key`; `network.Policy`.
- Produces:

```go
// Begin starts the observation channel over base and returns the Run that owns
// it. On failure it returns a nil Run and the error; the caller warns and uses
// Passive.
func Begin(base network.Policy) (*Run, error)
// Passive returns a Run with no channel: read-back-only analysis.
func Passive(base network.Policy) *Run
// Overlay is the policy the run applies to its options before the VM is
// prepared: the channel overlay when running, else base unchanged.
func (r *Run) Overlay() network.Policy
// Passive reports whether the channel is unavailable.
func (r *Run) Passive() bool
// Stop tears the channel down and returns its scrubbed artefact; idempotent.
// A passive Run returns the zero Observation.
func (r *Run) Stop() channel.Observation
// CaptureBaseline resolves the home volume's host path, takes the baseline
// manifest, and records the session in state. It returns the host path so the
// after-run reuses it. It is called after the VM is prepared and before attach.
func (r *Run) CaptureBaseline(ctx context.Context, k state.Key, agentName string) (string, error)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestBeginOverlayChangesThePolicy(t *testing.T)  // Overlay != base; the fingerprint changes (channel's own session_test pins this)
func TestPassiveOverlayIsBaseUnchanged(t *testing.T)
func TestCaptureBaselineRecordsTheSession(t *testing.T) // a state with a home volume -> LearnSession with StartedAt, HomeVolume, and one baseline entry per declared path
func TestCaptureBaselineNoHomeVolume(t *testing.T)      // readback.ErrNoHomeVolume surfaces as an error; nothing recorded
```

  The session test asserts `session.Run` with `opts.Learn` calls `Begin` before `prepareSandbox` (seam the two), applies the overlay to the network options the prepare seam sees, and calls `CaptureBaseline` between prepare and attach.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/learn/ -run TestBegin`. Expected: FAIL.

- [ ] **Step 3: Implement** `Run` (an `observer channel.Observer`, the base policy, the captured home path, and a `startedAt`), `Begin` via `channel.StartObservation("", base)`, `Passive`, `Overlay`, `Stop`, and `CaptureBaseline` (`readback.HomeVolumePath` → `readback.TakeManifest` → `manifestToState` → `state.RecordLearnSession`; convert `readback.Manifest` to `[]state.LearnManifestEntry` and back in this package).

- [ ] **Step 4: Wire `session.Run`** (see Design decisions 1, 2, 8): capture `base := opts.Network`; `if opts.Learn` → `learn.Begin`, warn+`Passive` on error, `defer learnRun.Stop()`, `opts.Network = learnRun.Overlay()`; after the `mode.SkipsVM()` early return and before `runAttach`, `learnRun.CaptureBaseline(ctx, k, k.Agent)` (warn on error). Add the two learn seams to `options.Seams` (or package vars in `session`, mirroring `prepareSandbox`) so a test drives the order without a real VM.

- [ ] **Step 5: Run** — `go test ./internal/sandbox/learn/ ./internal/sandbox/session/`. Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/learn/ internal/sandbox/session/ internal/sandbox/options/
git commit -m "feat(learn): start the channel and capture the baseline in a learning session"
```

---

### Task 4: The after-run analysis

**Files:**
- Create: `internal/sandbox/learn/analyse.go`, `internal/sandbox/learn/analyse_test.go`

**Interfaces:**
- Consumes: `readback.Read`, `readback.ReadBack`, `channel.Observation`, `channel.Load`, `analyser.ObservationFindings`, `analyser.SessionFacts`, `network.Policy`, `configpaths.ConfigPaths`.
- Produces:

```go
// Analysis is everything the after-run needs; the live path and config learn
// both build it.
type Analysis struct {
    Key          state.Key
    Slug         string // repo slug: the per-slug scope and the decline key
    CheckoutSlug string // the artefact store's directory
    Agent        string
    Paths        configpaths.ConfigPaths
    Home         string // home-volume host path
    Baseline     readback.Manifest
    Observation  channel.Observation
    Current      network.Policy // the pre-overlay policy (Design decision 1)
}
// Findings derives every finding the analysis supports (network + secrets).
func (a Analysis) Findings() []analyser.Finding
// SecretValue resolves a proposed static secret's value in memory, matched by
// env-var name; never persisted.
func (a Analysis) SecretValue() func(analyser.SecretWrite) (string, bool)
// Analyse reads the home back, derives the findings, and applies them
// interactively (Task 5 owns the prompt). It returns the summary.
func Analyse(ctx context.Context, a Analysis, ui termio.UI, mode options.Mode, setByFlag func(string) bool) (Summary, error)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestFindingsNetworkDiffUsesCurrentNotOverlay(t *testing.T) // Current has no proxy; the finding's allow set is Current.EgressAllow ∪ used, never the proxy
func TestFindingsSecretFromConfigEntryFallback(t *testing.T)    // a static credential with Credential.Hosts -> a secret finding with those hosts
func TestFindingsNoHostNoSecret(t *testing.T)                   // a static credential with no hosts and no attribution -> no finding
func TestSecretValueResolvesByEnvVar(t *testing.T)              // matched in memory; unknown env var -> false
func TestAnalyseReadsBackAndApplies(t *testing.T)               // a fake home + artefact -> Apply called once with the derived findings
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/learn/ -run TestFindings`. Expected: FAIL.

- [ ] **Step 3: Implement** `Findings` via `analyser.ObservationFindings(analyser.SessionFacts{Paths, Slug, ReadBack, Observation, Current})`, `SecretValue` over `readback.ReadBack.Static` keyed by `EnvVar`, and `Analyse` (call `readback.Read(a.Home, a.Agent, a.Baseline)`, derive, then Task 5's apply).

- [ ] **Step 4: Run** — `go test ./internal/sandbox/learn/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/learn/
git commit -m "feat(learn): derive the after-run findings from the read-back and capture"
```

---

### Task 5: The exit prompt, `AcceptAll`, and the live after-run

**Files:**
- Create: `internal/sandbox/learn/prompt.go`, `internal/sandbox/learn/prompt_test.go`
- Modify: `internal/sandbox/analyser/apply.go`, `internal/sandbox/analyser/apply_test.go`
- Modify: `internal/sandbox/learn/run.go` (`Finish`), `internal/sandbox/session/run.go`

**Interfaces:**
- Consumes: `analyser.Apply`, `analyser.ApplyOptions`, `state.ClearLearnSession`, `channel.Save`, `channel.Delete`.
- Produces:

```go
// FinishInput is what the live after-run needs beyond the Run itself.
type FinishInput struct {
    Key       state.Key
    Slug      string // repo slug
    Agent     string
    Paths     configpaths.ConfigPaths
    Home      string
    Base      network.Policy // pre-overlay policy
    UI        termio.UI
    Mode      options.Mode
    SetByFlag func(string) bool
}
// Finish tears the channel down, saves the artefact, runs the analysis in this
// process, prints the summary, and clears the session record. A passive Run
// saves no artefact.
func (r *Run) Finish(ctx context.Context, in FinishInput) error

// Summary is the counts the exit prompt reports.
type Summary struct {
    ServicesReached int
    KeysUsed        int
    Denied          []string
    Results         []analyser.ApplyResult
}

// ApplyOptions gains (analyser):
//   AcceptAll bool // apply every finding without asking; set only by an
//                  // explicit top-level "Save all", never by --yes
```

- [ ] **Step 1: Write the failing tests**

```go
// prompt_test.go
func TestExitPromptReviewAppliesPerFinding(t *testing.T) // "r" -> Apply with AcceptAll false
func TestExitPromptSaveAllAppliesWithoutAsking(t *testing.T) // "s" -> Apply with AcceptAll true
func TestExitPromptNotNowWritesNothing(t *testing.T)     // "n" -> no Apply, no decline recorded
func TestExitPromptNonInteractivePrintsOneLine(t *testing.T)

// analyser/apply_test.go
func TestApplyAcceptAllSkipsThePrompt(t *testing.T)      // AcceptAll -> every writable finding OutcomeApplied, no Select
func TestApplyAcceptAllStillRefusesAShadowedKey(t *testing.T) // a project-owned key -> OutcomeSkipped
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/learn/ ./internal/sandbox/analyser/ -run 'TestExitPrompt|TestApplyAcceptAll'`. Expected: FAIL.

- [ ] **Step 3: Implement** `AcceptAll` (in `applyFinding`, when set, treat as accepted and skip `askApply`; still run `planWrites`, so a shadowed/unattributable finding is refused), the summary renderer (`"agents-sandbox learned: %d services reached, %d keys used."` plus the denied list as a separate note), the `Select` menu (`Review and save`/`Save all`/`Not now`; non-interactive returns *Not now* and lets `Apply` print the one line), and `Finish` (Stop → `channel.Save(in.Key.Slug, startedAt, obs)` when not passive → build `Analysis` → `Analyse` → `state.ClearLearnSession(in.Key)` → `channel.Delete(artefact)` → print summary).

- [ ] **Step 4: Wire `session.Run`'s post-attach call**: after `runAttach` returns and when `opts.Learn` and `ctx.Err() == nil`, call `learnRun.Finish(ctx, ...)` with `Slug: opts.ConfigSlug`, `Base: base`, `Home` from `CaptureBaseline`; return the attach error unchanged. A cancelled ctx leaves the record for `config learn`.

- [ ] **Step 5: Run** — `go test ./internal/sandbox/learn/ ./internal/sandbox/analyser/ ./internal/sandbox/session/`. Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/learn/ internal/sandbox/analyser/ internal/sandbox/session/ internal/sandbox/channel/
git commit -m "feat(learn): apply the proposal at teardown behind the exit prompt"
```

---

### Task 6: `config learn`

**Files:**
- Modify: `cmd/agents-sandbox/commands_config.go`, `cmd/agents-sandbox/constants.go`, `cmd/agents-sandbox/commands.go` (`commandNeedsMSBRuntime`), `cmd/agents-sandbox/cli_config_test.go`

**Interfaces:**
- Consumes: `state.ReadLearnSession`, `state.ClearLearnSession`, `state.ReadState`, `readback.HomeVolumePath`, `channel.Load`, `learn.Analysis`, `learn.Analyse`; `git.ResolveIdentity`, `configpaths.Get()`.
- Produces: `cmdLearn = "learn"`; `buildConfigLearnCmd(ui)`; `commandNeedsMSBRuntime` returns true for `config learn`.

- [ ] **Step 1: Write the failing tests** (`cli_config_test.go`)

```go
func TestConfigLearnNoRecordedSession(t *testing.T)   // one line "No learning session to review."
func TestConfigLearnRecreatedVolumeIsDiscarded(t *testing.T) // recorded volume != current -> reported, record cleared, no analysis
func TestConfigLearnReplaysTheRecordedSession(t *testing.T)  // artefact loaded, findings applied (stubs the analysis seam)
func TestConfigLearnNonInteractiveWritesNothing(t *testing.T) // one line naming run --learn
```

- [ ] **Step 2: Run to verify it fails** — `go test ./cmd/agents-sandbox/ -run TestConfigLearn`. Expected: FAIL.

- [ ] **Step 3: Implement** the subcommand: resolve identity, read the recorded session for `{checkoutSlug, agent}`; absent → one line; `record.HomeVolume != state.HomeVolume` → report and clear; else resolve the home path, `channel.Load(record.ArtefactPath)`, convert `record.Baseline` to `readback.Manifest`, resolve the current effective policy from the resolver, and call `learn.Analyse`. Add `cmdLearn` to the constant block, register it in `buildConfigCmd`, and make `commandNeedsMSBRuntime` return true for `config learn` (it inspects a volume).

- [ ] **Step 4: Run** — `go test ./cmd/agents-sandbox/ -run TestConfigLearn`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agents-sandbox/
git commit -m "feat(config): add config learn to review the last learning session"
```

---

### Task 7: Prune abandoned artefacts

**Files:**
- Modify: `internal/sandbox/channel/store.go` (+`Delete`), `store_test.go`
- Create: `internal/sandbox/learn/prune.go`, `internal/sandbox/learn/prune_test.go`
- Modify: `cmd/agents-sandbox/commands_cli.go` (`runStartupStages`)

**Interfaces:**
- Consumes: `channel.Save`, `channel.Prune`, `configpaths.Get().UserStateDir()`.
- Produces:

```go
// channel/store.go
// Delete removes one saved artefact; a missing path is not an error.
func Delete(path string) error
// learn/prune.go
// LearnArtefactAge is how long an unreviewed artefact survives, so a session
// whose process died is still reviewable by config learn.
const LearnArtefactAge = 30 * 24 * time.Hour
// PruneArtefacts removes learn artefacts last modified before now-LearnArtefactAge.
func PruneArtefacts(now time.Time) (int, error)
```

- [ ] **Step 1: Write the failing test** — `Delete` removes one artefact and tolerates a missing path; `PruneArtefacts` removes an old artefact and keeps a fresh one; `runStartupStages` calls the prune seam once.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/channel/ ./internal/sandbox/learn/ -run 'TestDelete|TestPruneArtefacts'`. Expected: FAIL.

- [ ] **Step 3: Implement** `channel.Delete`, `learn.PruneArtefacts` (wrapping `channel.Prune(now.Add(-LearnArtefactAge))`), and call it from `runStartupStages` after `autoPrune` behind a `pruneLearnArtefacts` seam; a failure is a verbose warning, never a run failure.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/channel/ ./internal/sandbox/learn/ ./cmd/agents-sandbox/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/channel/ internal/sandbox/learn/ cmd/agents-sandbox/
git commit -m "feat(learn): prune abandoned observation artefacts at startup"
```

---

### Task 8: Delete the S4/S5/S6 dead-code markers

**Files:**
- Modify: every file carrying a temporary `//deadcode:ignore reason="… S7 …"` marker: `internal/sandbox/channel/{observation,session,store,proxy}.go`, `internal/sandbox/readback/{readback,read,manifest,probe_opencode2}.go`, `internal/agent/readback.go`, `internal/sandbox/analyser/{finding,observation,apply}.go`, `internal/sandbox/state/learn_decline.go`, `internal/viperconfig/setting_writer.go`.

**Interfaces:**
- Consumes: the wiring Tasks 3–7 added.
- Produces: a tree with no marker on a reachable declaration.

- [ ] **Step 1: Run the gate** — `make deadcode`. Expected: FAIL, naming each marker now on a reachable declaration.

- [ ] **Step 2: Delete each reported marker** (and only those). A declaration the gate still reports as unreachable keeps its marker only when nothing in `main` can reach it; if it is genuinely unused, delete the declaration instead. Do not blanket-strip: `make deadcode` is the authority.

- [ ] **Step 3: Run** — `make deadcode && make lint`. Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(learn): drop the dead-code markers the wiring made reachable"
```

---

### Task 9: Docs, changelog, and the brain

**Files:**
- Create: `changelog.d/<PR>.added.md`
- Modify: `docs/` (the learn-mode page the hub names, "Letting agents-sandbox learn your setup", plus its nav entry), `README.md` if the feature list needs it
- Modify: `.operator-shared/specs/onboarding/learn-mode.md` and `learn-mode/{findings-and-apply,observation-channel,home-read-back}.md` (record the settled mechanics: the `HomeState` session record and `config learn`; the artefact save/delete/30-day sweep; correct the passive-session sentences per Design decision 3; the exit prompt's three choices), `.operator-shared/index/sandbox.md` (the new `learn` package), `.operator-shared/catalog.md` if a new spec/subplan is named
- Modify: `.operator-shared/plans/2026-11-17-learn-mode-program.md` (flip S7 to landed; **add the S10 `agent-config-writeback` row and DAG edge**; drop the S7 stub)

- [ ] **Step 1:** Write the changelog fragment (`added`).
- [ ] **Step 2:** Add/refresh the docs page for `run --learn` / `config learn`, following `STYLEGUIDE.md` and the linking rules in `AGENTS.md`; keep it to what the user sees (the three choices are S9's).
- [ ] **Step 3:** Update the specs, the sandbox subindex, the catalog, and the program index as above. The program change is the reproposal: S10 depends on S5+S6, runs parallel to S7/S8, and S9 gains the S10 edge.
- [ ] **Step 4: Verify** — `make check && make brain-check`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add changelog.d/ docs/ README.md .operator-shared/
git commit -m "docs(operator): record the run --learn after-run and add S10"
```

---

### Task 10: Host accept (human, not an agent)

- [ ] On a KVM host: `make build`, then in a scratch project run `./agents-sandbox run --learn`, configure a provider inside the sandbox, make one real streaming turn, and exit. Confirm the exit prompt appears, the summary counts match, *Review and save* writes the per-slug config, and the VM recreated once for the observation overlay.
- [ ] Confirm a killed session (Ctrl-C or kill the launcher) leaves the record, and `./agents-sandbox config learn` replays the analysis.
- [ ] Confirm a passive run (`--network` fixed so the overlay is a no-op, or the channel forced off) proposes no network change and does not crash.

The subplan is not done until these are checked; they cannot run in the dev VM (`/dev/kvm` absent).

---

## Self-review

**Coverage.** The `--learn` surface, the overlay-before-prepare rule, and the serve-only incompatibility are Task 1 + Task 3; the after-run ownership (§After-run ownership) is Tasks 3/5 (live) and Task 6 (`config learn`); the session record and its survival is Task 2; the never-widen diff and the pre-overlay baseline are Tasks 3–4; the exit prompt vocabulary is Task 5; the artefact save/delete/sweep (invariant 3) is Tasks 5/7; the passive path is Tasks 3–4; the marker removal the program index mandates is Task 8; docs/brain are Task 9; the host-accept steps (the real turn through the proxy) are Task 10. The **agent-config back-copy** is deliberately out (Design decision 10, new subplan S10); the **`header_fields` scope** (#150) remains deferred.

**Proportion.** Signatures, test names, and assertions; no function bodies beyond what the tests do not determine. The wire-up reuses `channel`, `readback`, and `analyser` unchanged except for `AcceptAll` and `Delete`.

**Type consistency.** `options.RunOptions.Learn` (Task 1); `state.LearnSession`/`LearnManifestEntry` + `RecordLearnSession`/`ReadLearnSession`/`ClearLearnSession` (Task 2); `learn.Run`/`Begin`/`Passive`/`Overlay`/`Stop`/`CaptureBaseline` (Task 3); `learn.Analysis`/`Findings`/`SecretValue`/`Analyse` (Task 4); `learn.Finish`/`FinishInput`/`Summary` and `analyser.ApplyOptions.AcceptAll` (Task 5); `channel.Delete` and `learn.PruneArtefacts` (Task 7). Every name is defined once and consumed under the same spelling.

**Open item carried forward.** The two spec sentences that say a passive session proposes no secret-substitution change contradict `findings-and-apply.md` and S6's implementation; Task 9 corrects them per Design decision 3.
