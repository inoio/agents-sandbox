# Host Environment Defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Default the host's time zone, a UTF-8 locale, and the host git commit identity into every project VM's environment, so a guest's `date`, text handling, and commits match the host, while an explicitly configured env value always wins.

**Architecture:** A new leaf package `internal/sandbox/hostenv` resolves the host facts from injectable `Sources` (process env, `/etc/localtime` readlink, a host git reader) and returns a `map[string]string` of env defaults. `prepare.loadEnvFingerprints` overlays those defaults *under* the file-loaded env so it is fingerprinted and reaches the VM through `vm.buildProjectVMEnv`. Host reads sit behind a `prepare` package-var seam sealed in `TestMain`, so no test reads real host state.

**Tech Stack:** Go, `configpaths`, `homeconfig`, `os/exec` (host git), the existing `prepare` env-fingerprint path.

**Spec:** `.operator-shared/specs/vm/host-environment-defaults.md` (this plan's contract; read it alongside).

## Global Constraints

- Precedence is absolute: a value present in the file-loaded env wins over a host default. The defaults only fill absent keys.
- Never read real host state from a test. `hostenv` resolution is tested through injected `Sources`; the `prepare` seam is sealed in `TestMain`.
- Graceful absence: an unresolvable time zone, locale, or git identity is left unset with no warning.
- No generated git config file; no new config key; no host SSH/GPG/CA material.
- `make fmt` before every commit; `make lint` after at most three edits; `make check` when finalizing.
- The runner-image base contract is documented, not build-enforced (a custom base without zoneinfo stays on UTC).

## Review Focus

- Host `TZ` set to a non-IANA/POSIX string: passed through verbatim, never an error.
- `/etc/localtime` not a symlink, unreadable, or its target lacks `zoneinfo/`: `TZ` unset.
- Host `git` binary absent or the command fails: identity unset, no warning, no hang.
- Partial identity (name without email): no `GIT_*` variables.
- A `home:` entry provisioning `.gitconfig` or `.config/git/config` in any scope: the four `GIT_*` variables are suppressed.
- Host locale `en_US.UTF-8`: `LANG=C.UTF-8` (ungenerated locale would otherwise warn and fall back).
- Non-UTF-8 host locale (e.g. `C`): no `LANG` injected; a configured `LANG` still wins.
- A user env-file `TZ`/`LANG`/`GIT_*` value: wins over the host default and is what the fingerprint records.

---

## Task 1: `hostenv` resolver package

**Files:**
- Create: `.operator-shared/specs/vm/host-environment-defaults.md` (already drafted)
- Modify: `.operator-shared/catalog.md` (already drafted)
- Create: `internal/sandbox/hostenv/hostenv.go`
- Test: `internal/sandbox/hostenv/hostenv_test.go`
- Test: `internal/sandbox/hostenv/hostenv_internal_test.go`
- Test: `internal/sandbox/hostenv/main_test.go`

**Interfaces:**
- Produces (consumed by Task 2):
  - `type Sources struct { Getenv func(string) string; Readlink func(string) (string, error); GitIdentity func() (name, email string) }`
  - `func HostSources() Sources`
  - `func Resolve(s Sources, gitConfigProvisioned bool) map[string]string`
  - `func Under(base, defaults map[string]string) map[string]string`
  - `func ProvisionedGitConfig(scopes []configpaths.ConfigScope) bool`

- [ ] **Step 1: Write the failing resolver test**

Create `internal/sandbox/hostenv/main_test.go`, because the isolation contract requires every test package to install the fail-fast mocks (an external TestMain, like `internal/hostfigures/main_test.go`):

```go
package hostenv_test

import (
	"testing"

	mocks "github.com/inoio/agents-sandbox/internal/testutil/mocks"
)

func TestMain(m *testing.M) {
	mocks.InitFailFastMocks(m)
}
```

Then create `internal/sandbox/hostenv/hostenv_test.go` (`package hostenv_test`). Every `Sources` literal sets all three fields so the exhaustruct linter is satisfied.

```go
package hostenv_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/hostenv"
)

func fakeSources(tz, lang string, readlink func(string) (string, error), name, email string) hostenv.Sources {
	return hostenv.Sources{
		Getenv: func(key string) string {
			switch key {
			case "TZ":
				return tz
			case "LANG":
				return lang
			case "LC_ALL", "LC_CTYPE":
				return lang
			}
			return ""
		},
		Readlink:    readlink,
		GitIdentity: func() (string, string) { return name, email },
	}
}

func TestResolveTimeZone(t *testing.T) {
	linkTo := func(target string) func(string) (string, error) {
		return func(string) (string, error) { return target, nil }
	}
	tests := []struct {
		name     string
		tz       string
		readlink func(string) (string, error)
		want     string
	}{
		{"env wins verbatim", "Europe/Paris", linkTo("/usr/share/zoneinfo/Etc/UTC"), "Europe/Paris"},
		{"linux symlink", "", linkTo("/usr/share/zoneinfo/Europe/Berlin"), "Europe/Berlin"},
		{"macos symlink", "", linkTo("/var/db/timezone/zoneinfo/Europe/Berlin"), "Europe/Berlin"},
		{"readlink error", "", func(string) (string, error) { return "", errNotFound }, ""},
		{"target without zoneinfo", "", linkTo("/etc/foo/localtime"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostenv.Resolve(fakeSources(tt.tz, "", tt.readlink, "", ""), false)
			if got["TZ"] != tt.want {
				t.Errorf("TZ = %q, want %q", got["TZ"], tt.want)
			}
		})
	}
}

// errNotFound is a sentinel for the readlink-error case only; assert multi-uses.
var errNotFound = errors.New("not found")
```

Continue the same file with locale, identity, suppression, and `Under` cases:

```go
func TestResolveLocale(t *testing.T) {
	tests := []struct{ name, host, want string }{
		{"utf-8", "en_US.UTF-8", "C.UTF-8"},
		{"utf8 no dash", "en_US.utf8", "C.UTF-8"},
		{"c locale", "C", ""},
		{"unset", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostenv.Resolve(fakeSources("", tt.host, func(string) (string, error) { return "", errNotFound }, "", ""), false)
			if got["LANG"] != tt.want {
				t.Errorf("LANG = %q, want %q", got["LANG"], tt.want)
			}
		})
	}
}

func TestResolveGitIdentity(t *testing.T) {
	withIdentity := hostenv.Resolve(
		fakeSources("", "", func(string) (string, error) { return "", errNotFound }, "Ada", "ada@example.com"), false)
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		if withIdentity[key] != "Ada" {
			t.Errorf("%s = %q, want Ada", key, withIdentity[key])
		}
	}
	for _, key := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		if withIdentity[key] != "ada@example.com" {
			t.Errorf("%s = %q, want ada@example.com", key, withIdentity[key])
		}
	}

	partial := hostenv.Resolve(
		fakeSources("", "", func(string) (string, error) { return "", errNotFound }, "Ada", ""), false)
	if len(partial) != 0 {
		t.Errorf("partial identity produced %v, want no git vars", partial)
	}

	suppressed := hostenv.Resolve(
		fakeSources("", "", func(string) (string, error) { return "", errNotFound }, "Ada", "ada@example.com"), true)
	if _, ok := suppressed["GIT_AUTHOR_NAME"]; ok {
		t.Errorf("provisioned git config did not suppress the identity: %v", suppressed)
	}
}

func TestUnderKeepsBaseValues(t *testing.T) {
	got := hostenv.Under(
		map[string]string{"TZ": "From/File"},
		map[string]string{"TZ": "Host/Zone", "LANG": "C.UTF-8"},
	)
	if got["TZ"] != "From/File" {
		t.Errorf("TZ = %q, want the base value From/File", got["TZ"])
	}
	if got["LANG"] != "C.UTF-8" {
		t.Errorf("LANG = %q, want the default C.UTF-8", got["LANG"])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandbox/hostenv/`
Expected: FAIL — `undefined: hostenv.Resolve` / package does not exist.

- [ ] **Step 3: Implement `hostenv.go`**

Create `internal/sandbox/hostenv/hostenv.go`:

```go
// Package hostenv resolves the host facts the launcher defaults into a project
// VM's environment: the host time zone, a UTF-8 locale, and the host git commit
// identity. See specs/vm/host-environment-defaults.md.
package hostenv

import (
	"maps"
	"os"
	"os/exec"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
)

// Sources are the host accessors the defaults are resolved from.
type Sources struct {
	Getenv      func(string) string
	Readlink    func(string) (string, error)
	GitIdentity func() (name, email string)
}

// HostSources returns the real host accessors.
func HostSources() Sources {
	return Sources{Getenv: os.Getenv, Readlink: os.Readlink, GitIdentity: hostGitIdentity}
}

// Resolve returns the host-derived environment defaults for a project VM. A
// default is present only when the host fact resolves; the git identity is
// omitted when the user provisions a git config.
func Resolve(s Sources, gitConfigProvisioned bool) map[string]string {
	defaults := make(map[string]string)
	if zone := resolveTimezone(s.Getenv, s.Readlink); zone != "" {
		defaults["TZ"] = zone
	}
	if locale := resolveLocale(s.Getenv); locale != "" {
		defaults["LANG"] = locale
	}
	if !gitConfigProvisioned {
		name, email := s.GitIdentity()
		if name != "" && email != "" {
			defaults["GIT_AUTHOR_NAME"] = name
			defaults["GIT_AUTHOR_EMAIL"] = email
			defaults["GIT_COMMITTER_NAME"] = name
			defaults["GIT_COMMITTER_EMAIL"] = email
		}
	}
	return defaults
}

// Under returns defaults with base laid over it: a key present in base keeps
// its value. Neither input map is modified.
func Under(base, defaults map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(base))
	maps.Copy(merged, defaults)
	maps.Copy(merged, base)
	return merged
}

// ProvisionedGitConfig reports whether any config scope's home: key provisions
// a git global config. A config file that cannot be read is skipped; the config
// pipeline reports a malformed file separately.
func ProvisionedGitConfig(scopes []configpaths.ConfigScope) bool {
	for _, scope := range scopes {
		manifest, ok, err := homeconfig.ReadHomeFromConfigDir(scope.Dir)
		if err != nil || !ok {
			continue
		}
		if _, ok := manifest[".gitconfig"]; ok {
			return true
		}
		if _, ok := manifest[".config/git/config"]; ok {
			return true
		}
	}
	return false
}

const zoneinfoMarker = "zoneinfo/"

func resolveTimezone(getenv func(string) string, readlink func(string) (string, error)) string {
	if tz := getenv("TZ"); tz != "" {
		return tz
	}
	target, err := readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	i := strings.LastIndex(target, zoneinfoMarker)
	if i < 0 {
		return ""
	}
	return strings.TrimPrefix(target[i+len(zoneinfoMarker):], "/")
}

func resolveLocale(getenv func(string) string) string {
	effective := getenv("LC_ALL")
	if effective == "" {
		effective = getenv("LC_CTYPE")
	}
	if effective == "" {
		effective = getenv("LANG")
	}
	lower := strings.ToLower(effective)
	if strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8") {
		return "C.UTF-8"
	}
	return ""
}

// gitRunner runs the host git binary. A var so an internal test injects a fake.
var gitRunner = func(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

func gitConfigValue(key string) string {
	out, err := gitRunner("config", "--global", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func hostGitIdentity() (string, string) {
	return gitConfigValue("user.name"), gitConfigValue("user.email")
}
```

- [ ] **Step 4: Run the resolver tests to verify they pass**

Run: `go test ./internal/sandbox/hostenv/`
Expected: PASS.

- [ ] **Step 5: Write the failing `ProvisionedGitConfig` test and the internal git-reader test**

Append to `hostenv_test.go`:

```go
func TestProvisionedGitConfig(t *testing.T) {
	provisioned := t.TempDir()
	testutil.WriteFile(t, provisioned, "config.yaml", "home:\n  .gitconfig: /host/.gitconfig\n")
	other := t.TempDir()
	testutil.WriteFile(t, other, "config.yaml", "home:\n  .inputrc:\n")
	empty := t.TempDir()

	if !hostenv.ProvisionedGitConfig([]configpaths.ConfigScope{{Dir: provisioned}}) {
		t.Error("a provisioned .gitconfig was not detected")
	}
	if hostenv.ProvisionedGitConfig([]configpaths.ConfigScope{{Dir: other}}) {
		t.Error("an unrelated home entry was reported as a git config")
	}
	if hostenv.ProvisionedGitConfig([]configpaths.ConfigScope{{Dir: empty}}) {
		t.Error("an absent config was reported as a git config")
	}
}
```

Create `internal/sandbox/hostenv/hostenv_internal_test.go` (`package hostenv`):

```go
package hostenv

import "testing"

func TestHostGitIdentityReadsGlobalConfig(t *testing.T) {
	orig := gitRunner
	t.Cleanup(func() { gitRunner = orig })
	gitRunner = func(args ...string) (string, error) {
		switch args[len(args)-1] {
		case "user.name":
			return "Ada\n", nil
		case "user.email":
			return " ada@example.com \n", nil
		}
		return "", nil
	}
	name, email := hostGitIdentity()
	if name != "Ada" || email != "ada@example.com" {
		t.Errorf("hostGitIdentity() = (%q, %q), want (Ada, ada@example.com)", name, email)
	}
}

func TestGitConfigValueSwallowsError(t *testing.T) {
	orig := gitRunner
	t.Cleanup(func() { gitRunner = orig })
	gitRunner = func(...string) (string, error) { return "", errNotFound }
	if got := gitConfigValue("user.name"); got != "" {
		t.Errorf("gitConfigValue on error = %q, want empty", got)
	}
}
```

(`hostenv_test` and `hostenv` are distinct packages, so each may declare its own `errNotFound`; add `errors` to both imports.)

- [ ] **Step 6: Run and verify**

Run: `go test ./internal/sandbox/hostenv/`
Expected: PASS.

- [ ] **Step 7: Lint and commit**

Run: `make fmt && make lint`
Expected: no findings.

```bash
git add .operator-shared/specs/vm/host-environment-defaults.md .operator-shared/catalog.md internal/sandbox/hostenv
git commit -m "feat(vm): resolve host time zone, locale and git identity defaults"
```

---

## Task 2: Default the host env into `prepare`

**Files:**
- Modify: `internal/sandbox/prepare/prepare.go` (`loadEnvFingerprints`, near `prepare.go:879`)
- Modify: `internal/sandbox/prepare/main_test.go`
- Test: `internal/sandbox/prepare/prepare_sandbox_flow_test.go`

**Interfaces:**
- Consumes: `hostenv.Resolve`, `hostenv.HostSources`, `hostenv.Under`, `hostenv.ProvisionedGitConfig` (Task 1).
- Produces: package var `hostEnvDefaults func(scopes []configpaths.ConfigScope) map[string]string`, sealed in `TestMain`.

- [ ] **Step 1: Write the failing merge test**

Add to `internal/sandbox/prepare/prepare_sandbox_flow_test.go`:

```go
// TestLoadEnvFingerprintsAppliesHostDefaultsUnderEnvFileValues pins that a
// host-derived default fills only the keys the configured env leaves absent,
// and that the fingerprint records the merged env.
func TestLoadEnvFingerprintsAppliesHostDefaultsUnderEnvFileValues(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	writeEnvFile(t, configpaths.Get().UserConfigDir(), "TZ=From/File\n")

	orig := hostEnvDefaults
	hostEnvDefaults = func([]configpaths.ConfigScope) map[string]string {
		return map[string]string{"TZ": "Host/Zone", "LANG": "C.UTF-8"}
	}
	t.Cleanup(func() { hostEnvDefaults = orig })

	ui := termiotest.NewTestMock(t)
	env, fingerprints := loadEnvFingerprints(options.RunOptions{}, &ui)

	if env.Vars["TZ"] != "From/File" {
		t.Errorf("TZ = %q, want the configured From/File", env.Vars["TZ"])
	}
	if env.Vars["LANG"] != "C.UTF-8" {
		t.Errorf("LANG = %q, want the default C.UTF-8", env.Vars["LANG"])
	}
	if want := state.BuildEnvState(env.Vars); fingerprints.Env != want {
		t.Errorf("fingerprint = %#v, want the merged env %#v", fingerprints.Env, want)
	}
}
```

Use the file/helper names the surrounding tests already use for writing an env file (see `TestPrepareSandboxBakesAndFingerprintsTheLoadedEnv`; it calls `testutil.WriteFile(t, cp.UserConfigDir(), configpaths.EnvFileName, "...")`). Replace `writeEnvFile(...)` with that call and add the needed imports.

- [ ] **Step 1b: Add a test proving the default reaches the guest**

The `loadEnvFingerprints` test above is unit-level; add a sibling of `TestPrepareSandboxBakesAndFingerprintsTheLoadedEnv` (`TestPrepareSandboxBakesHostEnvDefaults`) that copies its mock setup, adds `testutil.WriteFile(t, cp.UserConfigDir(), configpaths.EnvFileName, "TZ=From/File\n")`, overrides `hostEnvDefaults` to return `map[string]string{"TZ": "Host/Zone", "LANG": "C.UTF-8"}` (with `t.Cleanup` restore), and asserts on the created spec and persisted state:

```go
	if spec.Env["TZ"] != "From/File" {
		t.Errorf("created VM TZ = %q, want the configured From/File", spec.Env["TZ"])
	}
	if spec.Env["LANG"] != "C.UTF-8" {
		t.Errorf("created VM LANG = %q, want the default C.UTF-8", spec.Env["LANG"])
	}
	wantEnv := state.BuildEnvState(map[string]string{"TZ": "From/File", "LANG": "C.UTF-8"})
	if st.EnvState.Hash != wantEnv.Hash {
		t.Errorf("persisted EnvState.Hash = %q, want %q", st.EnvState.Hash, wantEnv.Hash)
	}
```

Run: `go test ./internal/sandbox/prepare/ -run TestPrepareSandboxBakesHostEnvDefaults`
Expected: FAIL until Step 3 is implemented.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sandbox/prepare/ -run TestLoadEnvFingerprintsAppliesHostDefaultsUnderEnvFileValues`
Expected: FAIL — `undefined: hostEnvDefaults`.

- [ ] **Step 3: Implement the seam and the merge**

In `internal/sandbox/prepare/prepare.go`, above `loadEnvFingerprints`:

```go
// hostEnvDefaults resolves the host facts defaulted into the project VM
// environment (specs/vm/host-environment-defaults.md): the host time zone, a
// UTF-8 locale, and the host git commit identity. Sealed in TestMain so no test
// reads real host state; tests that assert the merge override it.
var hostEnvDefaults = func(scopes []configpaths.ConfigScope) map[string]string {
	return hostenv.Resolve(hostenv.HostSources(), hostenv.ProvisionedGitConfig(scopes))
}
```

Change `loadEnvFingerprints`' body to overlay the defaults:

```go
func loadEnvFingerprints(opts options.RunOptions, ui termio.Logger) (envsecret.EnvSecrets, state.Fingerprints) {
	scopes := configpaths.ConfigScopes(configpaths.Get(), opts.ConfigSlug)
	env := envsecret.LoadEnvAndSecrets(ui, scopes, opts.AllowPlaceholderFor)
	env.Vars = hostenv.Under(env.Vars, hostEnvDefaults(scopes))
	return env, desiredFingerprints(opts, env)
}
```

Update its doc comment to name the defaults. Add the `hostenv` import.

- [ ] **Step 4: Seal the seam in `TestMain`**

In `internal/sandbox/prepare/main_test.go`, inside `TestMain` before `mocks.InitFailFastMocks(m)`:

```go
	// Host-derived env defaults are sealed so prepare tests never read the host
	// time zone, locale, or git config; tests that assert the merge opt in.
	hostEnvDefaults = func([]configpaths.ConfigScope) map[string]string { return nil }
```

Add the `configpaths` import.

- [ ] **Step 5: Run the whole package and verify**

Run: `go test ./internal/sandbox/prepare/`
Expected: PASS, including the pre-existing `TestPrepareSandboxBakesAndFingerprintsTheLoadedEnv` (sealed to no defaults).

- [ ] **Step 6: Lint and commit**

Run: `make fmt && make lint`
Expected: no findings.

```bash
git add internal/sandbox/prepare
git commit -m "feat(vm): default host facts into the project VM env"
```

---

## Task 3: User-facing docs

**Files:**
- Modify: `docs/configuration/launcher.md` (the "Environment variables" section, ~line 349)
- Modify: `docs/runner-image.md` (the "base image contract" bullet list, ~line 254)

- [ ] **Step 1: Document the defaults**

After the "These are available to the agent and any child processes inside the sandbox." line in `docs/configuration/launcher.md`, add a short subsection: agents-sandbox also defaults three host facts into the sandbox env — `TZ` (the host's zone, from `$TZ` or the `/etc/localtime` symlink), `LANG` (`C.UTF-8` when the host locale is UTF-8), and the git commit identity (`GIT_AUTHOR_*`/`GIT_COMMITTER_*` from the host's global git config) — each only when the host resolves it, each overridden by a value you set in these files, and the git identity skipped entirely when a config scope's `home:` provisions `.gitconfig` or `.config/git/config`. Note the guest must carry zoneinfo for `TZ` to take effect.

- [ ] **Step 2: Document the custom-base zoneinfo requirement**

In the `docs/runner-image.md` base-image-contract bullet list (after the locale/PATH bullets), add a bullet: to have the guest use the host's time zone, the base must provide zoneinfo (`tzdata` on Debian/Ubuntu); the managed base already does, and a base without it stays on UTC.

- [ ] **Step 3: Verify the docs gates**

Run: `make check-docs` (or `ci/check-docs.sh`)
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs/configuration/launcher.md docs/runner-image.md
git commit -m "docs(vm): document host environment defaults"
```

---

## Task 4: Changelog and finalize

**Files:**
- Create: `changelog.d/<PR-number>.added.md`

- [ ] **Step 1: Create the fragment after the PR number is known**

The filename slug must equal the pull-request number; guess one past the highest if creating before the PR (see `.operator-shared/guides/github-pr-workflow.md`). Content:

```markdown
Default the host's time zone, a UTF-8 locale, and the host git commit identity into every sandbox, so the guest's `date`, text handling, and commits match the host. A value set in a config `env` file still wins; a provisioned git config disables the identity default.
```

- [ ] **Step 2: Run the full gate**

Run: `make check`
Expected: PASS (fmt, lint, test, docs, `brain-check`).

- [ ] **Step 3: Commit**

```bash
git add changelog.d
git commit -m "chore: add changelog fragment for #808"
```

---

## Host verification (manual, user-run)

Not runnable in this VM. On a KVM host: rebuild the runner image (`make build && ./agents-sandbox build -r`), then in a session run `date` and compare with the host's `date`; run `git -c user.name= var GIT_AUTHOR_IDENT` to confirm the host identity; confirm `locale` reports `LANG=C.UTF-8` when the host locale is UTF-8. Repeat with a `home:`-provisioned `.gitconfig` to confirm the identity default is suppressed.
