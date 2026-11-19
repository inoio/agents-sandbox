# S11 — Inline config secrets and directory login stores

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. A fresh implementer per task; a fresh reviewer gates each task; one whole-subplan review at the end. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the two read-back gaps issue #749 names — a static secret kept **inline** in an agent config file, and a **login store that is a directory of files** — so the after-run can propose the secret and record the logins, instead of missing both. This is the read-back half of #749; the `config harden` half is S8's C7 and the write-back half is S10's per-harness form, both amended in the same PR that lands this plan.

**Architecture:** The inline classification extends the existing provider-entry walk in `internal/sandbox/readback/read.go`: the same traversal that finds a provider's endpoint host also finds a credential-bearing key inside that entry, with provider, host, and env-var name taken from the entry. The login-store change turns the exact-path `loginCredentialStores` map into a pattern list matched with `filepath.Match`, so a declared glob (`credentials/*.json`) records one login per file. Both are pure read-back changes; no new package, no runtime.

**Tech Stack:** Go 1.27, `internal/sandbox/readback`, `internal/agent`, `internal/configdecode`, `internal/{testutil}`.

**Spec:** `.operator-shared/specs/onboarding/learn-mode/home-read-back.md` (§What read-back establishes facts 2–3, §Open validation); the frozen contracts are in `2026-11-17-learn-mode-program.md`. The issue is #749; the kimi profile that motivates the directory store is #748 / PR #752.

**Depends on:** S5 (read-back, landed) and #748 / PR #752 (the kimi-code profile). **This branch is based on #752**, so the `kimi-code` profile and its declared `ReadBackPaths` (`config.toml`, `mcp.json`, `credentials/*.json`) are present and the kimi end-to-end tests run against the real profile.

**Execution order:** Task 2 (directory login stores) lands **before** Task 1 (inline config credentials), because Task 1's inline walk skips login-store entries through `loginStoreFor`, which Task 2 introduces. The commit order is Task 2 then Task 1; the task numbers stay as written.

**Produces:** `readback.walkProviderEntries`; `readback.configInlineCredentials`; `readback.derivedEnvVarName`; `readback.loginStoreFor`; the `.kimi-code/credentials/*.json` login-store pattern.

## Global Constraints

See `2026-11-17-learn-mode-program.md` → Global Constraints. Specifically:

- **No value in a manifest, a finding, or a log** (invariant 3). An inline credential's value is read into `Credential.Value` in memory only, exactly as a dotenv value is.
- **No curated knowledge in the learned path** (invariant 5). The env-var name is derived from the session's own entry (its declared name, else its provider id), never from a provider table.
- **A reference is not a secret.** `api_key_env` and a `${…}` value are skipped, so a config the launcher already placeholdered is not re-proposed.
- **A login store's value is never read.** The glob change records presence only, like the exact-path stores today.
- TDD: failing test first. Test a unit in the file named after it (`read.go` → `read_test.go`).
- `make fmt`, `make lint`, `make check` — never `go fmt`/`go vet` by hand.
- Every behavior change updates `README.md`/`docs/` and adds a `changelog.d/<PR>.<type>.md` fragment.
- This subplan touches only host files: **no human-accept task**.

## Review Focus

1. **A login store read as a static secret** — a `credentials/*.json` file (or any declared login store) must not run through the inline walk and yield a static credential from a `token` field. (Task 1.)
2. **A value in the output** — the inline path must keep the value in memory only, never in the manifest or a reported fact. (Task 1.)
3. **A derived env-var name overriding a harness's own** — where the entry declares the name (`[providers.<id>.env]`), the declared name must win over the derived one. (Task 1.)
4. **A glob crossing a separator** — `credentials/*.json` must not match `credentials/sub/x.json` (`filepath.Match`'s `*` does not cross `/`). (Task 2.)
5. **A reference proposed as a secret** — an `api_key_env`-only entry, or a `${…}` value, must yield no credential. (Task 1.)
6. **A non-string credential value** — `token = 4096` (a count) must not be reported. (Task 1.)

---

### Task 1: Inline config credentials

**Files:**
- Modify: `internal/sandbox/readback/read.go`, `internal/sandbox/readback/read_test.go`

**Interfaces:**
- Consumes: `configdecode.FormatFor`/`Decode`, `readCappedFile`, `providerNamedByEntry`, `isProviderContainer`, `entryEndpointHosts`, `looksLikeSecretKey`, `normalizeToken`.
- Produces:

```go
// walkProviderEntries calls visit for every node the document positively
// presents as a provider entry, with the provider name that entry declares.
// It factors the traversal the endpoint walk already does, so both walks agree
// on what a provider entry is.
func walkProviderEntries(node any, visit func(entry map[string]any, provider string))

// configInlineCredentials returns each inline static credential a declared
// config file holds: a credential-bearing key inside a provider entry, with the
// provider, host, and env-var name taken from that entry. A login store and a
// probe are skipped; a dotenv is handled by the dotenv path.
func configInlineCredentials(home string, manifest Manifest) []Credential

// entryCredentials returns the static credentials one provider entry holds: its
// own credential key (api_key/apiKey/key/token) and the secret-looking keys of
// its "env" sub-table, each scoped by an env-var name.
func entryCredentials(entry map[string]any, provider, source string) []Credential

// derivedEnvVarName returns the env-var name an inline credential is scoped by
// when its entry names none: <PROVIDER>_API_KEY, uppercased with
// non-alphanumerics folded to '_'.
func derivedEnvVarName(provider string) string

// isPlaceholderValue reports whether a config value is a ${...} reference.
func isPlaceholderValue(value string) bool
```

  `Read` appends `configInlineCredentials(home, collection.manifest)` to `readBack.Static` before the host fill.
  `fillStaticCredentialHosts` fills only credentials whose `Hosts` is empty, so an inline credential keeps the host
  its own entry named.

- [ ] **Step 1: Write the failing tests**

```go
func TestDerivedEnvVarName(t *testing.T)              // moonshot -> MOONSHOT_API_KEY, kimi -> KIMI_API_KEY, oh-my-pi -> OH_MY_PI_API_KEY
func TestReadClassifiesInlineConfigSecret(t *testing.T) // hermes .hermes/config.yaml model.api_key -> one Static credential with Provider/Hosts/EnvVar
func TestReadInlineSecretTakesHostFromItsEntry(t *testing.T) // the credential's Hosts == the entry's base_url host
func TestReadInlineSecretDerivesEnvVarName(t *testing.T)     // a bare api_key -> EnvVar <PROVIDER>_API_KEY
func TestReadInlineSecretUsesDeclaredEnvVarName(t *testing.T) // [providers.x.env] KIMI_API_KEY -> EnvVar KIMI_API_KEY
func TestReadInlineSecretSkipsPlaceholderValue(t *testing.T) // api_key = "${MOONSHOT_API_KEY}" -> no credential
func TestReadInlineSecretSkipsApiKeyEnvOnly(t *testing.T)    // entry has api_key_env but no api_key -> no credential
func TestReadInlineSecretSkipsNonStringValue(t *testing.T)   // token = 4096 -> no credential
func TestReadInlineSecretSkipsALoginStore(t *testing.T)      // .codex/auth.json with a "token" field -> no Static (it is a login)
func TestReadInlineSecretKeepsDotenvHostFill(t *testing.T)   // a dotenv credential still gets its host from configEndpointHosts
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run 'TestDerivedEnvVarName|TestReadInlineSecret|TestReadClassifiesInlineConfigSecret'`. Expected: FAIL.

- [ ] **Step 3: Implement.** Factor the provider-entry traversal out of `collectEndpointHosts` into `walkProviderEntries`, which calls `visit` for a node whenever `providerNamedByEntry` names a provider (a container child, a `provider` field, or an explicit `provider` key) — **not** gated on the entry having an endpoint, so a credential in an endpointless entry is still found. `collectEndpointHosts` then adds a host only when `entryEndpointHosts(entry)` is non-empty; its existing tests stay green because an endpointless entry contributed no host before either. The inline walk skips a `.env` entry, a probe, and any entry `loginStoreFor` matches — `loginStoreFor` is introduced by Task 2, which is executed first (see the header's Execution order). Add `configInlineCredentials`: for each manifest entry that is not a probe, not a `.env`, and not `loginStoreFor`-matched, read and decode it, then for each provider entry build `entryCredentials`. `entryCredentials` yields, for the entry's own `inlineCredentialKeys` (`apikey`/`key`/`token`, via `normalizeToken`) a credential whose `EnvVar` is `derivedEnvVarName(provider)`, and, for each `looksLikeSecretKey` key of the entry's `env` sub-table, a credential whose `EnvVar` is that key; both skip a non-string value and an `isPlaceholderValue` value, and both set `Kind: CredentialStatic`, `Source`, `Provider`, and `Hosts: entryEndpointHosts(entry)`. Set `Field` empty (the harness owns the header). `derivedEnvVarName` uppercases and folds non-alphanumerics to `_`, then appends `_API_KEY`. `isPlaceholderValue` reports a value whose trimmed form starts with `${`.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/readback/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/readback/
git commit -m "feat(readback): classify an inline config-file secret"
```

---

### Task 2: Directory login stores

**Files:**
- Modify: `internal/sandbox/readback/read.go`, `internal/sandbox/readback/read_test.go`

**Interfaces:**
- Consumes: `filepath.Match`, the `Read` login classification loop.
- Produces:

```go
//exhaustruct:ignore
type loginCredentialStore struct {
    pattern  string   // an exact sandbox-home-relative path or a glob
    provider string
    agents   []string // nil = every agent that declares the path
}
var loginCredentialStores = []loginCredentialStore{ /* .claude/settings.json, .codex/auth.json,
    .local/share/opencode/auth.json, .pi/agent/auth.json, .kimi-code/credentials/*.json */ }

// loginStoreFor returns the login-store rule matching a manifest entry key, if
// a pattern matches it. A pattern with no metacharacter matches its exact path.
func loginStoreFor(key string) (loginCredentialStore, bool)
```

  `Read` uses `loginStoreFor(entry.Key)` in place of the map lookup; `.kimi-code/credentials/*.json` is added with
  `provider: "kimi-code"`.

- [ ] **Step 1: Write the failing tests**

```go
func TestLoginStoreForMatchesExactPath(t *testing.T)         // ".codex/auth.json" -> the codex store
func TestLoginStoreForMatchesAGlob(t *testing.T)             // ".kimi-code/credentials/a.json" -> the kimi store
func TestLoginStoreForDoesNotCrossSeparators(t *testing.T)   // ".kimi-code/credentials/sub/a.json" -> no match
func TestLoginStoreForUnknownIsAbsent(t *testing.T)          // ".nope/x" -> false
func TestReadClassifiesADirectoryLoginStore(t *testing.T)    // two credentials/*.json files -> two logins, values empty
```

  The last test uses the real `kimi-code` profile (#752 declares `.kimi-code/credentials/*.json`): write two files
  under `.kimi-code/credentials/`, call `Read(home, "kimi-code", Manifest{})`, and assert two `CredentialLogin`
  entries with `Provider == "kimi-code"`, empty `Value`, and both files in `ConfigPaths` — no stub, no `agent.Register`.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sandbox/readback/ -run 'TestLoginStoreFor|TestReadClassifiesADirectoryLoginStore'`. Expected: FAIL.

- [ ] **Step 3: Implement** the slice and `loginStoreFor` (iterate, `filepath.Match(store.pattern, key)`; ignore the match error as a non-match), rewrite the `Read` lookup, and add the `.kimi-code/credentials/*.json` rule. Update the existing store table's comment to note a pattern may be a glob.

- [ ] **Step 4: Run** — `go test ./internal/sandbox/readback/`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/readback/
git commit -m "feat(readback): classify a glob-declared directory login store"
```

---

### Task 3: Docs, changelog, and the brain

**Files:**
- Create: `changelog.d/<PR>.added.md`
- Modify: `docs/` only if a user-facing page names the read-back's credential shapes (the learn-mode page, when it lands)
- The spec edits (`home-read-back.md`, `findings-and-apply.md`) land with this plan's PR; this task verifies them.

- [ ] **Step 1:** Write the changelog fragment (the inline config secret and the directory login store).
- [ ] **Step 2:** Verify the spec edits read true against the implementation; correct any sentence the code disproves. Update `.operator-shared/specs/onboarding/learn-mode/home-read-back.md`'s `read_if` if the finding set changed.
- [ ] **Step 3: Verify** — `make check && make brain-check`. Expected: PASS.
- [ ] **Step 4: Commit**

```bash
git add changelog.d/ docs/ .operator-shared/
git commit -m "docs(operator): record the inline-secret and directory-login read-back"
```

---

## Self-review

**Coverage.** #749 point 1 (inline static classification with provider and endpoint from the entry) is Task 1; the reference-not-a-secret rule is `isPlaceholderValue` + skipping `api_key_env`; the env-var naming decision is `derivedEnvVarName` + the declared-name-wins rule; #749 point 2 (directory login store) is Task 2. Points 3–4 (the per-harness write form, harden's inline-config finding) are the S8/S10 amendments in their own PRs, not this plan. The kimi acceptance is exercised against the real `kimi-code` profile this branch is based on (#752).

**Proportion.** Signatures, test names, and pinned values; the only new model shapes are one helper set in `readback` and one field on the login-store rule.

**Type consistency.** `walkProviderEntries`/`configInlineCredentials`/`entryCredentials`/`derivedEnvVarName`/`isPlaceholderValue` (Task 1); `loginCredentialStore.pattern`/`loginStoreFor` (Task 2). `Read` and `fillStaticCredentialHosts` are changed in Task 1; the `Credential` shape is unchanged.
