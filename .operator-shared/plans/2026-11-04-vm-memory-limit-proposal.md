# VM Memory-Limit Detection and Raise Proposal — Implementation Plan (Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect when a project VM is OOM-killed, tell the user, and offer to raise the persisted `memory` setting; recover the host terminal automatically.

**Architecture:** A new leaf `internal/sandbox/pressure` package classifies a memory hit from the guest OOM kernel-log line or from metrics headroom. A watcher in `internal/sandbox/session` samples metrics during an attach so post-mortem attribution has evidence. A new `internal/sandbox/resourceproposal` orchestrates prompt + persisted config write, called from the post-attach path (`session`) and the next-launch path (`prepare.resolveReconfigDecision`); the existing `reconfig` machinery applies the raise live. Disk detection/grow is Phase 2.

**Tech Stack:** Go 1.27, `github.com/superradcompany/microsandbox/sdk/go v0.7.11`, cobra/viper, the repo's `termio.Prompter`, `internal/atomicwrite`, `internal/yamlfmt`.

**Spec:** `.operator-shared/specs/resource-limit-proposal.md` (read it; this plan argues from it).

## Global Constraints

- Module `github.com/inoio/agents-sandbox`, Go 1.27. No new external dependencies (the huh backend is explicitly out of scope).
- New UI work goes behind the `internal/termio` role interfaces; the prompt uses `termio.Prompter` only. A narrowed consumer takes the narrowest role, not `termio.UI`.
- Reference code by symbol/package, not `file:line`, in docs and comments.
- Tests are TDD: write the failing test, watch it fail, implement, watch it pass, commit.
- Use `make fmt` and `make lint` (never `go vet`); run `make test` (which passes `-tags microsandbox_ffi_path`) rather than a bare `go test ./...`.
- Finalization runs `make check` and adds a `changelog.d/<slug>.<type>.md` fragment.
- The durable contract must land in `.operator-shared/specs/` and the guides under `.operator-shared/` must stay current.

## Review Focus

The spec implies these but no single task's tests fully cover them; each is pinned in the named task.

1. A raise above the VM's boot maximum must produce a **recreate**, never a silent clamp (Task 8).
2. A log read or a metrics read that **fails or is unavailable** (stopped VM, unsupported runtime) must degrade to "no proposal", never crash or false-propose (Tasks 4, 9).
3. **Non-interactive** runs (`--yes`, piped stdin, no TTY) must never block on the prompt (Tasks 7, 9).
4. A **non-YAML config file** (`.json5`/`.jsonc`/`.json`) must never be rewritten as YAML; persistence skips with a warning (Task 6).
5. A declined proposal at sustained high pressure must **not re-nag** on every launch (Tasks 3, 9).
6. A **live memory resize is asynchronous** — `Modify` returns `STATE=converging` — so the raise is reported applied only after `Metrics.MemoryLimitBytes` reaches the requested value (Task 8b).

---

### Task 1: Add `Metrics` and `Logs` to the msb seam

**Files:**
- Modify: `internal/sandbox/msb/msb.go` (interfaces `Sandbox`/`SandboxHandle`; `realSandbox`/`realSandboxHandle`; the method-expression seam vars)
- Modify: `internal/sandbox/msb/testmock.go` (`MockSandbox`, `MockSandboxHandle`)
- Test: `internal/sandbox/msb/msb_test.go`, `internal/sandbox/msb/msb_coverage_test.go`

**Interfaces:**
- Consumes: `msbSdk.Sandbox.Metrics(ctx) (*msbSdk.Metrics, error)`, `msbSdk.Sandbox.Logs(ctx, msbSdk.LogOptions) ([]msbSdk.LogEntry, error)` and the `*msbSdk.SandboxHandle` equivalents (all present in the pinned SDK).
- Produces: methods on the launcher's `msb.Sandbox` and `msb.SandboxHandle`:
  - `Metrics(ctx context.Context) (*msbSdk.Metrics, error)`
  - `Logs(ctx context.Context, opts msbSdk.LogOptions) ([]msbSdk.LogEntry, error)`

- [ ] **Step 1: Write the failing adapter test**

In `internal/sandbox/msb/msb_test.go` add a test that swaps the package-level seam var (the file's existing pattern, e.g. `realSandboxAttachWith`) with a stub returning a fixed `*msbSdk.Metrics`, and asserts `realSandbox.Metrics` forwards it. Also add a mock test: build `&MockSandbox{MetricsResult: &msbSdk.Metrics{MemoryBytes: 1}}` and assert `Metrics` returns it, and `MockSandbox{MetricsErr: errBoom}` returns `errBoom`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/msb/ -run 'Metrics|Logs' -tags microsandbox_ffi_path`
Expected: FAIL (method/field undefined).

- [ ] **Step 3: Implement the seam**

Add the two methods to both interfaces, the real adapter methods (delegating through new package-level seam vars declared alongside `realSandboxAttachWith`), and the mock fields/results:
```go
// Mock fields (on MockSandbox and MockSandboxHandle)
MetricsResult *msbSdk.Metrics
MetricsErr    error
LogsResult    []msbSdk.LogEntry
LogsErr       error
// plus LogsOptions []msbSdk.LogOptions recording each call
```
Follow the file's existing recorder style for the `Logs` options slice.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sandbox/msb/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/msb/
git commit -m "feat(msb): add Metrics and Logs to the sandbox seam"
```

---

### Task 2: `pressure` — memory-hit classification

**Files:**
- Create: `internal/sandbox/pressure/pressure.go`
- Create: `internal/sandbox/pressure/pressure_test.go`

**Interfaces:**
- Consumes: `msbSdk.LogEntry` (fields `Source LogSource`, `Timestamp time.Time`, `Data []byte`), `msbSdk.Metrics` (`MemoryBytes`, `MemoryLimitBytes uint64`).
- Produces:
```go
package pressure

type Kind string
const (KindMemory Kind = "memory") // Phase 2 adds root-disk/workspace/tmp

type Evidence string
const (
    EvidenceOOMLog   Evidence = "oom-log"
    EvidenceHeadroom Evidence = "headroom"
)

type Signal struct {
    Kind     Kind
    Evidence Evidence
    Victim   string // process comm of the OOM victim; "" for headroom
    UsedMiB  uint32
    LimitMiB uint32
}

type Threshold struct {
    Percent      float64 // offer at >= this used fraction (0.90)
    FloorMiB     uint32  // or free <= this (1024)
    ReArmPercent float64 // re-arm below this (0.80)
}

func DefaultThreshold() Threshold

// FromSystemLog returns the most recent OOM kill found in the system-source
// entries, or ok=false.
func FromSystemLog(entries []msbSdk.LogEntry) (Signal, bool)

// UnderPressure reports whether used/limit trips t (percent OR floor).
func UnderPressure(usedMiB, limitMiB uint32, t Threshold) bool

// FromMetrics classifies memory pressure from a metrics snapshot, or ok=false.
func FromMetrics(m *msbSdk.Metrics, t Threshold) (Signal, bool)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestFromSystemLogParsesOOMKill(t *testing.T) {
    entries := []msbSdk.LogEntry{{Source: msbSdk.LogSourceSystem,
        Data: []byte("[    3.046430] Out of memory: Killed process 468 (cat) total-vm:1628kB, oom_score_adj:0")}}
    got, ok := FromSystemLog(entries)
    if !ok || got.Kind != KindMemory || got.Evidence != EvidenceOOMLog || got.Victim != "cat" { t.Fatalf("%+v ok=%v", got, ok) }
}
func TestFromSystemLogIgnoresNonOOM(t *testing.T) {
    entries := []msbSdk.LogEntry{{Source: msbSdk.LogSourceSystem, Data: []byte("reboot: Power down")}}
    if _, ok := FromSystemLog(entries); ok { t.Fatal("want no signal") }
}
func TestFromSystemLogReturnsNewest(t *testing.T) { /* two OOM entries, distinct Timestamps; expect the later Victim */ }
func TestUnderPressurePercentOrFloor(t *testing.T) {
    t8 := DefaultThreshold()
    if !UnderPressure(950, 1000, t8) { t.Error("95% should trip") }        // percent
    if !UnderPressure(2000, 3000, t8) { t.Error("1000 MiB free below the floor should trip") } // floor only: 66% < 90%
    if UnderPressure(2048, 4096, t8) { t.Error("50% with 2 GiB free must not trip") }
}
func TestFromMetricsHeadroom(t *testing.T) {
    m := &msbSdk.Metrics{MemoryBytes: 950 << 20, MemoryLimitBytes: 1000 << 20}
    got, ok := FromMetrics(m, DefaultThreshold())
    if !ok || got.Kind != KindMemory || got.Evidence != EvidenceHeadroom { t.Fatalf("%+v ok=%v", got, ok) }
}
func TestFromMetricsZeroLimit(t *testing.T) {
    if _, ok := FromMetrics(&msbSdk.Metrics{MemoryBytes: 1, MemoryLimitBytes: 0}, DefaultThreshold()); ok {
        t.Fatal("zero limit must not trip")
    }
}
func TestDefaultThresholdValues(t *testing.T) {
    d := DefaultThreshold()
    if d.Percent != 0.90 || d.FloorMiB != 1024 || d.ReArmPercent != 0.80 { t.Fatalf("%+v", d) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/pressure/ -tags microsandbox_ffi_path`
Expected: FAIL (package/one or more symbols undefined).

- [ ] **Step 3: Implement `pressure`**

Parse with a literal `"Out of memory: Killed process "` prefix followed by `<pid> (<comm>)`; take the entry with the greatest `Timestamp`. `FromMetrics` converts bytes to MiB and calls `UnderPressure` only when `MemoryLimitBytes > 0`. `UnderPressure` returns true when `used*100 >= limit*percent*100` (integer-safe) OR `limit-used <= FloorMiB`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/pressure/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/pressure/
git commit -m "feat(pressure): classify a memory hit from the OOM log or metrics headroom"
```

---

### Task 3: Persist the detected hit in `HomeState`

**Files:**
- Create: `internal/sandbox/state/resource_pressure.go`
- Modify: `internal/sandbox/state/state.go` (add the field to `HomeState`)
- Test: `internal/sandbox/state/resource_pressure_test.go`

**Interfaces:**
- Consumes: `WithHomeState(k, fn)`, `HomeState`.
- Produces:
```go
type ResourcePressureState struct {
    Kind      string    `yaml:"kind,omitempty"`
    Evidence  string    `yaml:"evidence,omitempty"`
    Victim    string    `yaml:"victim,omitempty"`
    OOMSeenAt time.Time `yaml:"oom_seen_at,omitempty"`
    // OfferedAt is set when the user has answered a raise offer for this hit
    // (accepted or declined), so the same hit is not re-offered. It stays zero
    // for a hit that was only observed (e.g. a non-interactive run), so the
    // proposal survives to the next interactive launch.
    OfferedAt time.Time `yaml:"offered_at,omitempty"`
}
// field on HomeState:
//   ResourcePressure *ResourcePressureState `yaml:"resource_pressure,omitempty"`

func RecordResourcePressure(k Key, rp ResourcePressureState) error
func MarkResourcePressureOffered(k Key, at time.Time) error
func ClearResourcePressure(k Key) error
```

- [ ] **Step 1: Write the failing tests**

Mirror `state/agent_version_test.go`: writing records the field when state exists; recording against a missing state is a no-op (returns nil); `MarkResourcePressureOffered` sets `OfferedAt` preserving `Kind`/`OOMSeenAt`; `ClearResourcePressure` removes the field; a round-trip through the YAML writer preserves `OOMSeenAt` (guard against the `yaml` dependency's behaviour, see `specs/yaml-library.md`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/state/ -run ResourcePressure -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement using `WithHomeState`**

Each writer opens one `WithHomeState` transaction and mutates the field; write only when the value actually changed (the existing `RecordAgentVersion` shape).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/state/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/state/
git commit -m "feat(state): record a resource-pressure marker in HomeState"
```

---

### Task 4: Metrics watcher during an attach

**Files:**
- Create: `internal/sandbox/session/pressure_watch.go`
- Test: `internal/sandbox/session/pressure_watch_test.go`
- Modify: `internal/sandbox/session/run.go` (start/stop it inside the shared `runAttach`)

**Interfaces:**
- Consumes: `msb.Sandbox.Metrics`, `pressure.FromMetrics`, `pressure.Threshold`, `termio.Logger`.
- Produces:
```go
// pressureWatchInterval is a test seam for the sampling period.
var pressureWatchInterval = 2 * time.Second

// pressureSummary is the worst snapshot observed during a session: the minimum
// memory headroom fraction seen (1.0 when memory was never the tightest
// resource), and whether any sample tripped the threshold.
type pressureSummary struct {
    MinHeadroomFraction float64
    Tripped             bool
}

// startPressureWatcher samples metrics until stop is called and returns the
// summary. It never blocks the caller, tolerates a failing Metrics call, and a
// nil sandbox (dry-run) returns a no-op that yields a zero summary.
func startPressureWatcher(ctx context.Context, sb msb.Sandbox, t pressure.Threshold, ui termio.Logger) func() pressureSummary
```

- [ ] **Step 1: Write the failing tests**

Use `&msb.MockSandbox{MetricsResult: …}` and a tiny `pressureWatchInterval`. Pin: a snapshot at 95% trips and stores the fraction; a failing `Metrics` (`MetricsErr`) is tolerated and yields an un-tripped summary; `stop` returns promptly and stops sampling; a nil sandbox yields a zero summary without a call.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run Pressure -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement the watcher**

A goroutine on a ticker calls `sb.Metrics(ctx)`; on success computes `free/limit` and calls `pressure.FromMetrics`; on error logs at verbose and continues. `stop` cancels and waits (mirroring `startNotifyWatcher`'s cancel+done channel).

- [ ] **Step 4: Wire it into the shared `runAttach`**

Start it inside `runAttach` (not `Run`), after the attach begins, so both `Run` and `Shell` — which each call `runAttach` — get one watcher. Stop it and read the `pressureSummary` inside `runAttach` before the post-attach check and before `withClientSession` releases the lease and reaps (Task 10 consumes it then). Do not start it in `previewSandbox`/dry-run paths.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/sandbox/session/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/session/
git commit -m "feat(session): sample memory metrics during an attach"
```

---

### Task 5: Terminal reset and signal-safe restore

**Files:**
- Create: `internal/sandbox/session/termreset.go`
- Test: `internal/sandbox/session/termreset_test.go`
- Modify: `internal/sandbox/session/run.go` (emit the reset after the attach returns)

**Interfaces:**
- Produces:
```go
// terminalReset is the sequence that clears the guest TUI's emulator modes
// (leave alternate screen, disable mouse, show cursor, SGR reset). Emitted to
// the host terminal after an attach returns, before any launcher message.
const terminalReset = "\x1b[?1049l\x1b[?1000l\x1b[?1006l\x1b[?25h\x1b[0m"

// resetTerminal writes terminalReset to w unless w is nil.
func resetTerminal(w io.Writer)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestResetTerminalWritesSequence(t *testing.T) {
    var b bytes.Buffer
    resetTerminal(&b)
    if b.String() != terminalReset { t.Fatalf("got %q", b.String()) }
}
func TestResetTerminalNilWriter(t *testing.T) { resetTerminal(nil) } // must not panic
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run ResetTerminal -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement and emit it after attach**

Implement `resetTerminal`. In `session.runAttach`, after `sb.AttachWith(...)` returns and before `withClientSession` reaps, call `resetTerminal(opts.UI.StdOut())` (or the run's stdout stream — match how the notify/attach paths access it). Add a `signal.Notify(SIGINT, SIGTERM)` install at the top of `Run` that calls `resetTerminal` before returning a non-nil error, mirroring the existing `signal.NotifyContext` wiring in `commands_cli.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/session/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/session/
git commit -m "feat(session): reset the host terminal after the attach returns"
```

---

### Task 6: Persist a setting to the owning config layer

**Files:**
- Create: `internal/viperconfig/setting_writer.go`
- Test: `internal/viperconfig/setting_writer_test.go`

**Interfaces:**
- Consumes: `configpaths.ConfigPaths` (`UserConfigDir`, `UserSlugConfigDir`, `ProjectConfigDir`), `configpaths.FindConfigFile(dir) (string, string, bool)`, `internal/atomicwrite`, `internal/yamlfmt`.
- Produces:
```go
// LayerSettingOwner returns the directory of the highest-precedence file layer
// that sets key (project config > per-slug user config > generic user config),
// or ok=false when no file layer sets it.
func LayerSettingOwner(paths configpaths.ConfigPaths, slug, key string) (dir string, ok bool)

// WriteSetting sets key to value in the config file in dir, creating config.yaml
// when the directory has no config file. It returns ErrUnsupportedConfigFormat
// (writing nothing) when dir holds a non-YAML config file, so a JSON5/JSON/JSONC
// file is never corrupted.
func WriteSetting(dir, key string, value any) error

var ErrUnsupportedConfigFormat = errors.New("unsupported config file format for automated edits")
```

- [ ] **Step 1: Write the failing tests**

Pin: `LayerSettingOwner` picks the project dir when the project config sets `memory` and the user dir also does; picks the user-slug dir when only it sets the key; returns false when none does (use `testutil.WriteYAML` into `cp.UserConfigDir()`/`cp.UserSlugConfigDir(slug)`/`cp.ProjectConfigDir()` and `WithMockConfigPaths`). `WriteSetting` on an empty dir creates `config.yaml` and a re-read via the resolver sees `memory: 8G`; on a YAML file preserves an unrelated key; **on a YAML file preserves a comment attached to an unrelated key and a top-of-file comment** (the writer must edit the node tree, not round-trip through a map); on a `.json5` file returns `ErrUnsupportedConfigFormat` and leaves the file byte-identical.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/viperconfig/ -run 'Setting|LayerSettingOwner' -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

`LayerSettingOwner` reads each layer's config file (via `FindConfigFile` + a small YAML/JSON decode) highest-precedence first and tests for the key. `WriteSetting`: if `FindConfigFile(dir)` reports a file whose extension is not `.yaml`/`.yml`, return `ErrUnsupportedConfigFormat`; otherwise **parse into a `yaml.Node` tree and set/insert the key on that tree — never round-trip through `map[string]any`, which drops the user's comments** — then marshal and write atomically (`atomicwrite`) after `yamlfmt` formatting. When no file exists, write `config.yaml`. When a format-preserving edit is not feasible, return an error rather than rewrite (surface the miss; the spec forbids corrupting the file).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/viperconfig/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/
git commit -m "feat(viperconfig): write a setting to the owning config layer, format-preserving"
```

---

### Task 7: The raise prompt and its persistence

**Files:**
- Create: `internal/sandbox/resourceproposal/proposal.go`
- Test: `internal/sandbox/resourceproposal/proposal_test.go`

**Interfaces:**
- Consumes: `pressure.Signal`, `pressure.Threshold`, `termio.Prompter`, `viperconfig.WriteSetting`/`LayerSettingOwner`, `configpaths.ConfigPaths`, `options.ParseMemory`.
- Produces:
```go
package resourceproposal

type Suggestion struct{ CurrentMiB, ProposedMiB, BootMaxMiB uint32 }

// Suggestion returns the prefilled raise (current + 25%, rounded up to the next
// GiB, and at least current+1GiB). BootMaxMiB 0 means "unknown" (no clamp).
func SuggestRaise(currentMiB, bootMaxMiB uint32) uint32

// Ask offers to raise memory for a detected memory hit and returns the chosen
// MiB. It returns ok=false when the user declines or when the UI is
// non-interactive (never blocks). It never writes anything.
func Ask(ui termio.Prompter, hit pressure.Signal, s Suggestion) (newMiB uint32, ok bool, err error)

// PersistMemory writes the accepted memory to the owning config layer and
// returns the directory. It returns an error rather than write a shadowed value
// when an environment variable or the --memory flag supplies the key.
func PersistMemory(paths configpaths.ConfigPaths, slug string, memoryMiB uint32, memoryFlagChanged bool) (string, error)
```

- [ ] **Step 1: Write the failing tests**

Pin: `SuggestRaise(4096, 0)` = `5120` (4→5 GiB); `SuggestRaise(4096, 4096)` clamps to `4096`; `Ask` with `termio.Mock{IsInteractiveResult:false}` returns `ok=false` without calling `Input`; with interactive + `InputResult:"6G"` returns `6<<10` and `ok=true`; a non-numeric input re-prompts or falls back to the default (pick one and pin it); `PersistMemory` with either `AGENTS_SANDBOX_MEMORY` or the deprecated `OPENCODE_SANDBOX_MEMORY` set returns an error and writes nothing; with `memoryFlagChanged` true returns an error and writes nothing; with none of those, writes `memory` into the owner layer and a resolver re-read yields the value.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/resourceproposal/ -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

`Ask` builds the message from the hit (including `hit.Victim` when evidence is the OOM log), `Select`s raise/keep, then `Input`s the GB value with the suggestion as default and `options.ParseMemory` to parse. `PersistMemory` returns an error (writing nothing) when `memoryFlagChanged` is true, or when either the registry-derived env name for `memory` (`AGENTS_SANDBOX_MEMORY`) **or its deprecated alias `OPENCODE_SANDBOX_MEMORY`** is set (`LegacyEnvPrefix` binds the same key — see `internal/viperconfig/load.go`); else `dir, ok := LayerSettingOwner(paths, slug, "memory")`, defaulting to `paths.UserSlugConfigDir(slug)` when `!ok`, then `WriteSetting(dir, "memory", fmt.Sprintf("%dM", memoryMiB))`. The caller passes `memoryFlagChanged` from `cmd.Flags().Changed("memory")` (spec §8).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/resourceproposal/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/resourceproposal/
git commit -m "feat(resourceproposal): prompt to raise memory and persist the choice"
```

---

### Task 8: A raise above the boot maximum forces a recreate

**Files:**
- Modify: `internal/sandbox/reconfig/plan.go` (`PlanReconfig`, `resourceModification`)
- Test: `internal/sandbox/reconfig/plan_test.go`, `reconfig_test.go`

**Interfaces:**
- Consumes: existing `addRecreate`, `Change`, `FormatSizeSpec`.
- Produces: `PlanReconfig` adds a recreate `Change{Label: "memory above boot maximum"}` and does **not** stage the memory `Modify` when the desired `memory` exceeds `cfg.MaxMemoryMiB` (with `MaxMemoryMiB > 0`).

- [ ] **Step 1: Write the failing tests**

```go
func TestMemoryAboveBootMaxForcesRecreate(t *testing.T) {
    cfg := &msbSdk.SandboxConfig{CPUs: 4, MemoryMiB: 4096, MaxMemoryMiB: 4096}
    d := PlanReconfig(cfg, planImage(), options.RunOptions{Memory: "8G"}, ChangeFlags{}, "")
    if !d.Recreate { t.Fatal("expected recreate") }
    if d.Resources != nil && d.Resources.MemoryMiB != 0 { t.Fatalf("must not stage a clamped live memory change: %+v", d.Resources) }
}
func TestMemoryAtOrBelowBootMaxStaysLive(t *testing.T) { /* MaxMemoryMiB 8192, want 8G → staged live, no recreate */ }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/reconfig/ -run BootMax -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `PlanReconfig`, before staging resources: if `opts.Memory != ""` and `cfg.MaxMemoryMiB > 0` and `options.ParseMemory(opts.Memory) > cfg.MaxMemoryMiB`, `addRecreate(...)` and leave `resourceModification` to skip the memory field for that case.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/reconfig/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/reconfig/
git commit -m "feat(reconfig): recreate when a memory raise exceeds the boot maximum"
```

---

### Task 8b: Wait for a live memory resize to converge

**Files:**
- Modify: `internal/sandbox/vm/vm_resources.go` (`reconcileResourceConfig`) and the resource-proposal call path that reports the raise applied
- Test: `internal/sandbox/vm/vm_resources_test.go`

**Interfaces:**
- Consumes: `msb.Sandbox.Metrics`, `msbSdk.Metrics.MemoryLimitBytes`, the requested MiB.
- Produces: a bounded wait that polls `Metrics` until `MemoryLimitBytes` reaches the requested value; returns an error on timeout or a read error; runs only when a live memory `Modify` was staged (spec §5: the resize is asynchronous — `Modify` returns `STATE=converging` and the limit lags — so do not read once and do not report applied on the first read).

- [ ] **Step 1: Write the failing tests**

Pin: a mock whose `MemoryLimitBytes` reports the old value then the new one converges within the deadline; a mock that never converges returns a timeout error; a mock returning `MetricsErr` returns that error. Use an injectable short interval/deadline seam.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/vm/ -run MemoryConverge -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

After `reconcileResourceConfig` stages a live memory increase, poll `Metrics.MemoryLimitBytes` on the seam-bounded interval until it reaches the requested limit or the deadline elapses; treat a metrics read error as terminal. The above-max/ recreate path never waits (the VM is rebuilt, not resized live).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/vm/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/vm/
git commit -m "feat(vm): wait for a live memory resize to converge"
```

---

### Task 9: Next-launch detection and proposal

**Files:**
- Modify: `internal/sandbox/prepare/reconfig.go` (`resolveReconfigDecision`) and its caller where the live handle is available
- Test: `internal/sandbox/prepare/reconfig_*_test.go`

**Interfaces:**
- Consumes: `liveProjectVM.handle` (`msb.SandboxHandle` with `Logs`), `pressure.FromSystemLog`/`FromMetrics`/`Threshold.ReArmPercent`, `state.ResourcePressure` field + writers, `resourceproposal.Ask`/`PersistMemory`/`SuggestRaise`, `options.RunOptions`.
- Produces: after `ResolveReconfig`, offer a raise when there is an **unanswered** hit or a **newer** one. Define the marker semantics the helper relies on: `OOMSeenAt` is the fingerprint (timestamp) of the newest observed hit; `OfferedAt` is set only when the user actually answers. Offer when `OOMSeenAt` is set and `OfferedAt` is zero (recorded, never answered — e.g. a prior non-interactive run), or when the newest OOM line is newer than the recorded `OOMSeenAt`. On a headroom/metrics trip, apply the re-arm rule: offer once, and do not re-offer until headroom has recovered below `ReArmPercent` (0.80) and tripped again; keep the recorded OOM timestamp so clearing pressure never replays an already-answered kill. On accept `PersistMemory`; on an answer (accept **or** decline) `MarkResourcePressureOffered`.

- [ ] **Step 1: Write the failing tests**

Using `msb.MockSandboxHandle{LogsResult: []msbSdk.LogEntry{oomEntry}}` and a `termio.Mock`, pin: an OOM line newer than the marker's recorded kill calls `Ask` once; after a decline the marker is advanced and a second `resolveReconfigDecision` with the *same* entry does not call `Ask`; a `LogsErr` yields no call and no error; **a non-interactive launch records the hit but does not answer it, so a following interactive launch with the same entry DOES call `Ask`**; a headroom trip offers once, a further trip that has not first recovered below `ReArmPercent` does not re-offer, and a trip after recovery below `ReArmPercent` does — while that later trip never replays an already-answered OOM kill.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/prepare/ -run 'Pressure|OOM' -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add a `detectAndOfferMemoryRaise(...)` helper called from `resolveReconfigDecision` (or its caller) after `PlanReconfig`; read `handle.Logs(ctx, msbSdk.LogOptions{Sources: []msbSdk.LogSource{msbSdk.LogSourceSystem}, Tail: 200})`, classify, dedup against `hs.ResourcePressure` per the marker rule above, and prompt/persist. When `Ask` cannot run (non-interactive), record the hit but do **not** advance `OfferedAt`, so the proposal is retried on the next interactive run.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/prepare/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/prepare/
git commit -m "feat(prepare): detect a prior memory hit and offer a raise on next launch"
```

---

### Task 10: Post-attach detection, prompt, and marker

**Files:**
- Modify: `internal/sandbox/session/run.go` (`Run`, `runAttach`)
- Test: `internal/sandbox/session/run_test.go`

**Interfaces:**
- Consumes: the watcher summary from Task 4, `pressure.FromSystemLog`, `resourceproposal.Ask`/`PersistMemory`, `state.RecordResourcePressure`/`MarkResourcePressureOffered`, `opts.ResolvedAgent`, `opts.UI`.
- Produces: after `AttachWith` returns and the terminal is reset, when the end was abnormal (signal `-1`) and either the log shows an OOM or the watcher summary tripped, read the system log, call `resourceproposal.Ask`, persist on accept, and record the marker — before `withClientSession` releases the lease and reaps. State the raise applies next run.

- [ ] **Step 1: Write the failing tests**

With `msb.MockSandbox{AttachCode: -1, LogsResult: oom}` and a `termio.Mock`, pin: an abnormal end with an OOM line prompts once and a decline records the marker; a clean exit (code 0) does not prompt; a missing log read does not error the session.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/session/ -run PostAttach -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement**

Extend the attach closure to run the detection/prompt/marker after `AttachWith` returns, guarded by the abnormal-end condition and the interactive check; keep all side effects best-effort (log, never fail the session).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/session/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/session/
git commit -m "feat(session): offer a memory raise after an OOM-killed attach"
```

---

### Task 11: CLI coverage, durable spec, docs, changelog

**Files:**
- Create: `cmd/agents-sandbox/cli_resource_pressure_test.go`
- Create: `.operator-shared/specs/resource-limit-proposal.md`
- Modify: `docs/` page(s) describing resource limits; `CHANGELOG` fragment `changelog.d/<slug>.<type>.md`
- Modify: `.operator-shared/catalog.md` (catalog the new spec)

**Interfaces:**
- Consumes: the CLI seams other `cli_*_test.go` files use to inject a mock msb client and UI.

- [ ] **Step 1: Write the CLI test**

Drive `run` with a mock sandbox whose attach ends abnormally and whose logs contain an OOM line, and a mock Prompter that accepts `6G`; assert the owning config file gained `memory: 6G` and the run exited cleanly. Add a non-interactive variant asserting no prompt and no hang.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/agents-sandbox/ -run ResourcePressure -tags microsandbox_ffi_path`
Expected: FAIL.

- [ ] **Step 3: Implement the missing wiring**

Thread whatever the CLI root must pass (config paths, slug, resolved agent) into `session.Run`/`prepare` so the mocked path reaches the new flow; no production behaviour change beyond Tasks 1–10.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/agents-sandbox/ -tags microsandbox_ffi_path`
Expected: PASS.

- [ ] **Step 5: Write the durable spec and docs**

`.operator-shared/specs/resource-limit-proposal.md`: the contract (detection evidence classes, threshold + re-arm, dedup marker, persistence layer selection, env-shadow rule, apply timing, recreate-above-max). Follow `guides/operator-brain-maintenance.md` for the catalog entry. Update the docs page(s) to describe the behaviour and add the changelog fragment.

- [ ] **Step 6: Finalize**

Run: `make fmt && make lint && make test && make compile-all`
Expected: all pass. Then `git add` the docs, spec, and changelog fragment and commit:
```bash
git commit -m "docs: document memory-limit detection and the raise proposal"
```

---

## Self-Review

- **Spec coverage:** §1a watcher → Task 4; §1b abnormal end → Tasks 4/10; §1c OOM attribution/log → Tasks 1/2/9/10; §1d marker + next launch → Tasks 3/9; §1e disk → Phase 2; §2 terminal recovery → Task 5; §3 prompt → Task 7; §4 persistence/precedence/env → Tasks 6/7; §5 apply (memory) → Tasks 7/8/8b; §5 root disk → Phase 2; §6 memory → done, disk/cpu → Phase 2/later; §7 non-interactive/host-bound → Tasks 7/9 (host-bound probe is Phase 2 with the disk work). CPU is out of scope per the spec.
- **Placeholders:** none — the one open point (`FindConfigFile`'s exact return order) is a code step the implementer confirms against the symbol.
- **Type consistency:** `pressure.Signal`/`Threshold`, `resourceproposal.Suggestion`/`Ask`/`PersistMemory`, `state.ResourcePressureState`, and the msb `Metrics`/`Logs` signatures are named once and reused verbatim.
- **Review Focus:** (1) Task 8; (2) Tasks 4, 9; (3) Tasks 7, 9; (4) Task 6; (5) Tasks 3, 9; (6) Task 8b.
- **Proportion:** the plan names signatures and tests rather than bodies; the only bodies are the reset constant and the threshold arithmetic.

## Deferred to Phase 2

Disk detection and grow/recreate proposals: extend `pressure` with root-disk/workspace/tmp kinds and the disk threshold, add `Metrics.UpperFreeBytes` and the in-guest `df` probe, enable live root-disk grow in `reconfig`/`vm.reconcileResourceConfig`, add the host available-memory bound (`sysinfo`), and write the disk CLI tests. A separate plan.
