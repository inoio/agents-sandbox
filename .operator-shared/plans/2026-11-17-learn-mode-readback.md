# S5 — Home-volume read-back and baseline manifest

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Host-only steps are a human accept step.

**Goal:** Read the sandbox home volume back on the host and establish facts 1–3 of the analyser (agent config files and their credential entries; static secrets; login credentials), plus a baseline manifest for change detection.

**Architecture:** A new `internal/sandbox/readback` package resolves the project's home-volume host path from state, walks the agent's declared config and credential paths, and classifies auth entries as static (value extractable) or login (never copied). Per-agent facts come from a new optional capability on `internal/agent`; the opencode2 credential is an inline SQLite row, so a probe reads the agent DB rather than a file. The manifest records path/size/mtime/hash — never values — and doubles as the change detector.

**Tech Stack:** Go 1.27, `internal/sandbox/{readback,volume,state}`, `internal/agent`, `internal/configpaths`, `internal/filewalk`, `modernc.org/sqlite` (E1, opencode2 only).

**Spec:** `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (the per-harness truth and the manifest contract); hub `.operator-shared/specs/onboarding/learn-mode.md` (facts 1–3, invariant 2).

**Consumes:** E1 (`modernc.org/sqlite` — this subplan adds it). No channel dependency; runs in parallel with S2/S3.
**Produces:** `readback.Manifest`, `readback.ManifestEntry`, `readback.Credential`, `readback.CredentialKind`, `readback.ReadBack`, `readback.TakeManifest`, `readback.Read`; `agent.ReadBackDescriber`. S6 consumes all of these.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:
- A manifest entry holds path, size, mtime, and a content hash — **never a value**. Only `Credential.Value` carries a static value, in memory, and it never reaches a manifest or a persisted artefact.
- Login credentials are recorded, never copied (invariant 2).
- The home volume is read from the host by path; nothing is uploaded.

## Review Focus

1. **opencode2's large, churny DB** — `opencode.db` is ~13 MB, mostly a changing model catalog; a whole-file hash would fire on every session. The credential must be probed by querying the `credential` table, not by hashing the DB. (Task 2, Task 4.)
2. **A harness we have not verified** — an agent with no `ReadBackDescriber` must yield an empty, honest result, not a panic or a false "no credentials". (Task 3.)
3. **A credential file that is a login, not a key** — must be classified login and never have its value read out. (Task 3.)
4. **A file larger than the cap** — must degrade to an opaque entry (size+mtime), not fail the walk. (Task 2.)

---

### Task 1: Resolve the home-volume host path

**Files:**
- Create: `internal/sandbox/readback/readback.go`, `internal/sandbox/readback/readback_test.go`, `internal/sandbox/readback/main_test.go`

**Interfaces:**
- Consumes: `volume.InspectVolume(ctx, name) (VolumeDetail, error)`, `state.ReadState(k) (*HomeState, error)` (`.HomeVolume`), `msb.Get()` seam.
- Produces:
  ```go
  // HomeVolumePath returns the host filesystem path of the given project's home
  // volume, from the volume recorded in state.
  func HomeVolumePath(ctx context.Context, k state.Key) (string, error)
  ```
  The package declares one role interface for its volume dependency so tests inject a double:
  ```go
  type VolumeInspector interface { InspectVolume(ctx context.Context, name string) (volume.VolumeDetail, error) }
  ```

- [ ] **Step 1: Write the failing test** — with a fake inspector returning `VolumeDetail{Path: "/host/vol"}`, `HomeVolumePath` returns `/host/vol`; when `state` has no home volume it returns an error naming the missing volume.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run TestHomeVolumePath`. Expected: FAIL.
- [ ] **Step 3: Implement** the package, the role interface, and `HomeVolumePath` (state read → volume name → inspect → `.Path`).
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/readback/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/readback/
git commit -m "feat(readback): resolve the home volume host path"
```

---

### Task 2: The baseline manifest

**Files:**
- Create: `internal/sandbox/readback/manifest.go`, `internal/sandbox/readback/manifest_test.go`

**Interfaces:**
- Consumes: `filewalk.Walk`, `agent.ReadBackDescriber` (Task 3 — until it exists, iterate an explicit path list).
- Produces:
  ```go
  type ManifestEntry struct {
      Key     string    // sandbox-home-relative file path, or "<agent>:<probe>" for a DB probe
      Size    int64     // 0 for a probe
      ModTime time.Time // zero for a probe
      Hash    string    // sha256 hex; empty only for an opaque (over-cap) file entry; for a probe, the digest of the probed rows
  }
  type Manifest struct { Entries []ManifestEntry }
  // TakeManifest records a ManifestEntry for every declared path plus one probe
  // digest per agent credential probe. Files over maxHashedBytes are recorded
  // opaque (size+mtime, no hash).
  func TakeManifest(home, agentName string) (Manifest, error)
  // Changed returns the keys whose size, mtime, or hash differs from prev (a probe
  // entry is compared on its hash alone), plus keys present in one manifest and not
  // the other, sorted.
  func (m Manifest) Changed(prev Manifest) []string
  const maxHashedBytes = 256 * 1024
  ```

- [ ] **Step 1: Write the failing test**

```go
func TestManifestChangedDetectsContentAndPresence(t *testing.T) {
    home := t.TempDir()
    writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
    base, _ := TakeManifest(home, "opencode")
    writeFile(t, home, ".config/opencode/opencode.jsonc", "b")
    next, _ := TakeManifest(home, "opencode")
    if got := next.Changed(base); len(got) != 1 || got[0] != ".config/opencode/opencode.jsonc" {
        t.Fatalf("got %v", got)
    }
}
func TestManifestOpaqueOverCap(t *testing.T) {
    home := t.TempDir()
    writeFileSized(t, home, ".config/opencode/opencode.jsonc", maxHashedBytes+1)
    m, _ := TakeManifest(home, "opencode")
    found := false
    for _, e := range m.Entries {
        if e.Key == ".config/opencode/opencode.jsonc" {
            found = true
            if e.Hash != "" { t.Fatal("over-cap file must be recorded opaque") }
        }
    }
    if !found { t.Fatal("an over-cap declared file must still appear as an opaque entry") }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run TestManifest`. Expected: FAIL.
- [ ] **Step 3: Implement** `ManifestEntry`, `Manifest`, `TakeManifest` (walk declared paths, stat, hash under the cap), and `Changed`.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/readback/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/readback/manifest.go internal/sandbox/readback/manifest_test.go
git commit -m "feat(readback): add the baseline manifest"
```

---

### Task 3: Per-agent read-back declaration and file-based credentials

**Files:**
- Modify: `internal/agent/capabilities.go`, `internal/agent/opencode_shared.go`, `internal/agent/codex.go`, `internal/agent/hermes.go`, `internal/agent/oh_my_pi.go`, `internal/agent/claudecode.go`, `internal/agent/pi.go`; add `internal/agent/readback.go` and `internal/agent/readback_test.go`
- Modify: `internal/sandbox/readback/manifest.go`, `internal/sandbox/readback/read.go`, `internal/sandbox/readback/read_test.go`

**Interfaces:**
- Consumes: `agent.ConfigDirNames`, `ConfigMerger.VMConfigPath/ConfigFileNames`, `Provisioner.ProvisionRules`.
- Produces:
  ```go
  // internal/agent — optional, discovered by type assertion like the others
  type ReadBackDescriber interface {
      // ReadBackPaths are sandbox-home-relative paths (files or globs) whose
      // change the baseline tracks and whose content the read-back inspects.
      ReadBackPaths() []string
      // CredentialProbes names agent-specific credential readers (e.g.
      // "opencode2-sqlite"); file-based auth is covered by ReadBackPaths.
      CredentialProbes() []string
  }
  // internal/sandbox/readback
  type CredentialKind int
  const (CredentialStatic CredentialKind = iota; CredentialLogin)
  type Credential struct {
      Kind     CredentialKind
      Source   string   // sandbox-home-relative path, or "<agent>:<probe>"
      Provider string
      Field    string   // header-field hint, optional
      Hosts    []string // endpoints named for the entry, optional
      Value    string   // static only; never in a Manifest
  }
  type ReadBack struct { ConfigPaths []string; Static, Login []Credential; Changed []string }
  // Read inspects the agent's declared config and credential paths. An agent
  // without a ReadBackDescriber yields an empty ReadBack (never an error).
  func Read(home, agentName string, baseline Manifest) (ReadBack, error)
  ```

- [ ] **Step 1: Write the failing test**

```go
func TestReadClassifiesStaticAndLogin(t *testing.T) {
    home := t.TempDir()
    // claude-code: a settings.json login entry → Login, value never read
    writeFile(t, home, ".claude/settings.json", `{"oauthAccount":{"email":"x"}}`)
    rb, err := Read(home, "claude-code", Manifest{})
    if err != nil { t.Fatal(err) }
    if len(rb.Static) != 0 || len(rb.Login) == 0 { t.Fatalf("want login only, got %+v", rb) }
}
func TestReadUnknownAgentIsEmpty(t *testing.T) {
    rb, err := Read(t.TempDir(), "no-such-agent", Manifest{})
    if err != nil || len(rb.Static)+len(rb.Login) != 0 { t.Fatalf("want empty, got %+v %v", rb, err) }
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/agent/ ./internal/sandbox/readback/ -run 'TestRead'`. Expected: FAIL.
- [ ] **Step 3: Implement** the capability on each profile (opencode/opencode2: `.config/opencode/*` + `.local/share/opencode/auth.json`; codex: `.codex/auth.json` as login; claude-code: `.claude/settings.json` as login; hermes: `.hermes/config.yaml` + `.hermes/.env`; oh-my-pi: `.omp/agent/config.yml`, `models.yml`, `.env`; pi: `.pi/agent/settings.json`), and `Read`'s file classification. `Read` reuses the `manifest.TakeManifest` walk; an unknown agent returns an empty `ReadBack`.
- [ ] **Step 4: Run the tests** — `go test ./internal/agent/ ./internal/sandbox/readback/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add internal/agent/ internal/sandbox/readback/
git commit -m "feat(agent,readback): declare per-harness read-back paths and classify credentials"
```

---

### Task 4: opencode2 SQLite credential probe (E1)

**Files:**
- Modify: `go.mod`, `go.sum` (add `modernc.org/sqlite`)
- Create: `internal/sandbox/readback/probe_opencode2.go`, `internal/sandbox/readback/probe_opencode2_test.go`
- Modify: `internal/sandbox/readback/read.go` (dispatch probe ids)

**Interfaces:**
- Consumes: `Credential`/`CredentialKind` (Task 3); `.operator-shared/specs/agents/opencode2.md` (the `credential` table: `integration_id`, `label`, `value` JSON `{"type":"key"|"oauth", ...}`).
- Produces: probe id `"opencode2-sqlite"` reading `<home>/.local/share/opencode/opencode.db`, table `credential`. `value.type == "key"` → `CredentialStatic` with `.Value` from the JSON `key` field; `"oauth"` → `CredentialLogin`. Also contributes a probe `ManifestEntry` whose `Hash` is a digest of the `credential` rows only (so a catalog change does not fire it).

- [ ] **Step 1: Write the failing test** — build a temp `opencode.db` with `modernc.org/sqlite` containing one `credential` row (`integration_id=openai`, `value={"type":"key","key":"sk-…"}`), call `Read(home, "opencode2", Manifest{})`, assert one `CredentialStatic` with `Provider=="openai"` and that no manifest entry contains the value.
- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run TestOpenCode2Probe`. Expected: FAIL (`go mod tidy` first; `make lint` to catch the CGO-free SQLite import).
- [ ] **Step 3: Implement** the probe and dispatch; the probe reads only the `credential` table, never a whole-file hash of the DB.
- [ ] **Step 4: Run the tests** — `go test ./internal/sandbox/readback/`. Expected: PASS.
- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/sandbox/readback/
git commit -m "feat(readback): read opencode2 credentials from its SQLite store"
```

---

### Task 5: Host integration test and docs

**Files:**
- Create: `internal/sandbox/readback/readback_msb_test.go` (`msb` build tag)
- Modify: `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (record the verified harnesses), `changelog.d/<PR>.added.md` if any user-visible surface exists (none yet — skip the fragment if the change is internal)

**Interfaces:** none new.

- [ ] **Step 1: Write the `msb`-tagged test** creating a project VM with `AGENTS_SANDBOX_PROVISION_HOST_CONFIG=false`, completing a minimal opencode2 setup, then asserting `Read(home, "opencode2", …)` finds the static credential.
- [ ] **Step 2 (human accept):** run it on a KVM host (`CGO_ENABLED=1 go test ./internal/sandbox/readback/ -tags msb -run TestReadBackOpenCode2`), paste the result, and record in the spec which harnesses are now verified (S1 of the spike covered opencode2 only).
- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/readback/readback_msb_test.go .operator-shared/specs/onboarding/learn-mode/home-read-back.md
git commit -m "test(readback): verify opencode2 read-back against a real home volume"
```

---

## Self-review

**Coverage.** Facts 1–3 (analyser list) and the baseline-manifest contract map to Tasks 2–4; the opencode2 SQLite nuance (both the credential-row read and the no-whole-file-hash rule) is Tasks 2 and 4; the "unverified harness" honesty is Task 3's unknown-agent path. The session-record and decline-memory persistence the spec mentions are **not** in this subplan — they belong to S7 (session boundary), whose plan adds the `RootState` field. That is deliberate: this subplan is the reader, not the store.

**Proportion.** Signatures and assertions only.

**Type consistency.** `Manifest`/`ManifestEntry`/`Credential`/`CredentialKind`/`ReadBack` are defined once here and are the frozen contract S6 consumes; they replace the placeholders in the program index.
