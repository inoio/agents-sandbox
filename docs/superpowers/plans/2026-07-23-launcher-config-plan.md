# Launcher config and user-level env files — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add user-level and project-level launcher config files (for CLI
flag defaults) and user-level `env`/`env.secret` files, with project overriding
user in both cases.

**Architecture:** A new `internal/launcherconfig` package uses Viper to load
and merge `~/.config/opencode-msb/config.*` and `.opencode-msb/config.*`
(JSONC/JSON5 are normalized through the existing `titanous/json5` library).
`cmd/opencode-msb/cli.go` applies the loaded defaults to Cobra flags that the
user did not explicitly set. `internal/sandbox/runner.go` merges user and
project `env`/`env.secret` maps before passing them to the sandbox.

**Tech Stack:** Go 1.26, Cobra, Viper, `github.com/titanous/json5`.

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- Keep abstractions minimal; prefer small, focused files.
- Add inline comments only when code is not self-explanatory.
- Do not commit secrets or `.envrc` content.
- All changes must pass `go test ./...` and `golangci-lint run`.

---

## Task 1: Add Viper dependency

**Files:**
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces: `github.com/spf13/viper` available for import.

- [ ] **Step 1: Add Viper**

```bash
go get github.com/spf13/viper
```

- [ ] **Step 2: Sync module files**

```bash
go mod tidy
```

- [ ] **Step 3: Verify build still works**

```bash
go build ./...
```

Expected: clean build, no output.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build(deps): add viper for launcher config"
```

---

## Task 2: Implement launcher config loader

**Files:**
- Create: `internal/launcherconfig/config.go`
- Create: `internal/launcherconfig/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { ... }`
  - `func Load(userDir, projectDir string) (Config, map[string]bool, error)`
    - `Config` holds the merged defaults.
    - The `map[string]bool` reports which top-level keys were explicitly set.
    - Missing files are ignored; malformed files return an error.

- [ ] **Step 1: Write the failing package test**

Create `internal/launcherconfig/config_test.go`:

```go
package launcherconfig

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadMissingFilesReturnsDefaults(t *testing.T) {
	cfg, keys, err := Load("", "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
	if cfg.CPUs != 0 || cfg.Memory != "" || cfg.Yes || cfg.Verbose || cfg.Quiet || cfg.Rebuild {
		t.Errorf("expected zero defaults, got %+v", cfg)
	}
}

func TestLoadYAMLConfig(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "config.yaml", map[string]any{
		"cpus":    4,
		"memory":  "8G",
		"rebuild": true,
		"verbose": true,
	})

	cfg, keys, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 4 {
		t.Errorf("expected cpus 4, got %d", cfg.CPUs)
	}
	if cfg.Memory != "8G" {
		t.Errorf("expected memory 8G, got %q", cfg.Memory)
	}
	if !cfg.Rebuild || !cfg.Verbose {
		t.Errorf("expected rebuild and verbose true, got %+v", cfg)
	}
	if !keys["cpus"] || !keys["memory"] {
		t.Errorf("expected cpus and memory keys, got %v", keys)
	}
}

func TestLoadJSON5Config(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json5"), `{
		// a comment
		"cpus": 2,
		"memory": "512M",
		"yes": true
	}`)

	cfg, keys, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 2 || cfg.Memory != "512M" || !cfg.Yes {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if !keys["cpus"] || !keys["yes"] {
		t.Errorf("expected cpus and yes keys, got %v", keys)
	}
}

func TestLoadProjectOverridesUser(t *testing.T) {
	user := t.TempDir()
	project := t.TempDir()
	writeYAML(t, user, "config.yaml", map[string]any{
		"cpus":   2,
		"memory": "4G",
		"yes":    true,
	})
	writeYAML(t, project, "config.yaml", map[string]any{
		"memory": "8G",
		"yes":    false,
	})

	cfg, keys, err := Load(user, project)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CPUs != 2 {
		t.Errorf("expected cpus 2 from user, got %d", cfg.CPUs)
	}
	if cfg.Memory != "8G" {
		t.Errorf("expected memory 8G from project, got %q", cfg.Memory)
	}
	if cfg.Yes {
		t.Error("expected yes=false from project override")
	}
	if !keys["cpus"] || !keys["memory"] || !keys["yes"] {
		t.Errorf("expected all keys set, got %v", keys)
	}
}

func TestLoadInvalidCPUs(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "config.yaml", map[string]any{"cpus": 300})

	_, _, err := Load(dir, "")
	if err == nil {
		t.Fatal("expected error for cpus > 255")
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json5"), "{")

	_, _, err := Load(dir, "")
	if err == nil {
		t.Fatal("expected error for malformed config")
	}
}

func writeYAML(t *testing.T, dir, name string, v map[string]any) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	writeFile(t, filepath.Join(dir, name), string(data))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/launcherconfig/...
```

Expected: compile errors because `Load` and `Config` do not exist.

- [ ] **Step 3: Implement the loader**

Create `internal/launcherconfig/config.go`:

```go
package launcherconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	json5 "github.com/titanous/json5"
)

// Config holds launcher-level defaults that can be set in
// ~/.config/opencode-msb/config.* and .opencode-msb/config.*.
type Config struct {
	Yes     bool   `mapstructure:"yes"`
	Verbose bool   `mapstructure:"verbose"`
	Quiet   bool   `mapstructure:"quiet"`
	CPUs    uint8  `mapstructure:"cpus"`
	Memory  string `mapstructure:"memory"`
	Rebuild bool   `mapstructure:"rebuild"`
}

var supportedExts = []string{".yaml", ".yml", ".json", ".jsonc", ".json5"}

// Load reads launcher config files from userDir and projectDir. Missing files
// are ignored. Project values override user values. The returned map contains
// the top-level keys that were explicitly set in either file.
func Load(userDir, projectDir string) (Config, map[string]bool, error) {
	v := viper.New()
	if err := mergeDir(v, userDir); err != nil {
		return Config{}, nil, err
	}
	if err := mergeDir(v, projectDir); err != nil {
		return Config{}, nil, err
	}
	if err := validate(v); err != nil {
		return Config{}, nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, nil, fmt.Errorf("decode launcher config: %w", err)
	}
	keys := make(map[string]bool, len(v.AllSettings()))
	for k := range v.AllSettings() {
		keys[k] = true
	}
	return cfg, keys, nil
}

func mergeDir(v *viper.Viper, dir string) error {
	path, ext, ok := findConfigFile(dir)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launcher config %s: %w", path, err)
	}
	ct := configType(ext)
	if ct == "json5" || ct == "jsonc" {
		var m map[string]any
		if err := json5.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("parse launcher config %s: %w", path, err)
		}
		data, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("normalize launcher config %s: %w", path, err)
		}
		ct = "json"
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("load launcher config %s: %w", path, err)
	}
	return nil
}

func findConfigFile(dir string) (string, string, bool) {
	if dir == "" {
		return "", "", false
	}
	for _, ext := range supportedExts {
		path := filepath.Join(dir, "config"+ext)
		if _, err := os.Stat(path); err == nil {
			return path, ext, true
		}
	}
	return "", "", false
}

func configType(ext string) string {
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json", ".jsonc", ".json5":
		return "json"
	}
	return ""
}

func validate(v *viper.Viper) error {
	if !v.IsSet("cpus") {
		return nil
	}
	cpus := v.GetInt("cpus")
	if cpus < 0 || cpus > 255 {
		return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/launcherconfig/...
```

Expected: all tests pass.

- [ ] **Step 5: Run the linter**

```bash
golangci-lint run ./internal/launcherconfig/...
```

Expected: no issues (fix any that appear).

- [ ] **Step 6: Commit**

```bash
git add internal/launcherconfig/
git commit -m "feat(launcherconfig): add launcher config loader with tests"
```

---

## Task 3: Wire launcher config into CLI flags

**Files:**
- Modify: `cmd/opencode-msb/cli.go`
- Modify: `cmd/opencode-msb/cli_test.go`
- Modify: `internal/sandbox/runner.go` (Config struct only)

**Interfaces:**
- Consumes:
  - `launcherconfig.Config`
  - `launcherconfig.Load(userDir, projectDir)`
- Produces:
  - `sandbox.Config.UserLauncherDir`
  - `applyLauncherConfig(cmd, lc, keys)` helper

- [ ] **Step 1: Add `UserLauncherDir` to sandbox.Config**

In `internal/sandbox/runner.go`, change:

```go
type Config struct {
	StateDir      string
	UserConfigDir string
}
```

to:

```go
type Config struct {
	StateDir        string
	UserConfigDir   string
	UserLauncherDir string
}
```

- [ ] **Step 2: Write a failing CLI test for config application**

Add to `cmd/opencode-msb/cli_test.go` (also add the import for
`gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig`):

```go
import (
	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
	// ... existing imports
)

func TestApplyLauncherConfigSetsUnsetFlags(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	lc := launcherconfig.Config{CPUs: 4, Memory: "8G", Yes: true, Verbose: true}
	keys := map[string]bool{"cpus": true, "memory": true, "yes": true, "verbose": true}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8("cpus")
	if cpus != 4 {
		t.Errorf("expected cpus 4, got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString("memory")
	if mem != "8G" {
		t.Errorf("expected memory 8G, got %q", mem)
	}
	yes, _ := root.PersistentFlags().GetBool("yes")
	if !yes {
		t.Error("expected yes=true")
	}
	verbose, _ := root.PersistentFlags().GetBool("verbose")
	if !verbose {
		t.Error("expected verbose=true")
	}
}

func TestApplyLauncherConfigRespectsCLIOverrides(t *testing.T) {
	root := buildRootCmd()
	runCmd, _, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	if err := runCmd.ParseFlags([]string{"--cpus", "2", "--memory", "1G", "--yes=false"}); err != nil {
		t.Fatalf("ParseFlags failed: %v", err)
	}

	lc := launcherconfig.Config{CPUs: 8, Memory: "16G", Yes: true, Verbose: true}
	keys := map[string]bool{"cpus": true, "memory": true, "yes": true, "verbose": true}

	if err := applyLauncherConfig(runCmd, lc, keys); err != nil {
		t.Fatalf("applyLauncherConfig failed: %v", err)
	}

	cpus, _ := runCmd.Flags().GetUint8("cpus")
	if cpus != 2 {
		t.Errorf("expected cpus 2 (CLI override), got %d", cpus)
	}
	mem, _ := runCmd.Flags().GetString("memory")
	if mem != "1G" {
		t.Errorf("expected memory 1G (CLI override), got %q", mem)
	}
	yes, _ := runCmd.Flags().GetBool("yes")
	if yes {
		t.Error("expected yes=false (CLI override)")
	}
	verbose, _ := runCmd.Flags().GetBool("verbose")
	if !verbose {
		t.Error("expected verbose=true from config")
	}
}

func TestNewConfigSetsUserLauncherDir(t *testing.T) {
	t.Setenv("HOME", "/testhome")
	cfg := newConfig()
	if cfg.StateDir != "/testhome/.local/state/opencode-msb" {
		t.Errorf("unexpected state dir: %q", cfg.StateDir)
	}
	if cfg.UserConfigDir != "/testhome/.config/opencode-msb/opencode" {
		t.Errorf("unexpected user config dir: %q", cfg.UserConfigDir)
	}
	if cfg.UserLauncherDir != "/testhome/.config/opencode-msb" {
		t.Errorf("unexpected user launcher dir: %q", cfg.UserLauncherDir)
	}
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

```bash
go test ./cmd/opencode-msb/...
```

Expected: compile errors for `applyLauncherConfig` and `UserLauncherDir`.

- [ ] **Step 4: Implement the CLI wiring**

In `cmd/opencode-msb/cli.go`:

1. Add imports:

```go
import (
	"strconv"
	// ... existing imports
	"gitlab.inoio.de/inoio/opencode-msb/internal/launcherconfig"
)
```

2. Update `newConfig`:

```go
func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:        filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir:   filepath.Join(home, ".config", "opencode-msb", "opencode"),
		UserLauncherDir: filepath.Join(home, ".config", "opencode-msb"),
	}
}
```

3. Update the root `PersistentPreRunE`:

```go
root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
	cfg := newConfig()
	lc, keys, err := launcherconfig.Load(cfg.UserLauncherDir, projectLauncherDir)
	if err != nil {
		return err
	}
	if err := applyLauncherConfig(cmd, lc, keys); err != nil {
		return err
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		prompt.AssumeYes = true //nolint:reassign // CLI flag override, set once at startup
	}
	return nil
}
```

4. Add the helper constant and functions near `newConfig`:

```go
const projectLauncherDir = ".opencode-msb"

func applyLauncherConfig(cmd *cobra.Command, lc launcherconfig.Config, keys map[string]bool) error {
	apply := []struct {
		key string
		fn  func() error
	}{
		{"yes", func() error { return setBoolFlag(cmd, "yes", lc.Yes) }},
		{"verbose", func() error { return setBoolFlag(cmd, "verbose", lc.Verbose) }},
		{"quiet", func() error { return setBoolFlag(cmd, "quiet", lc.Quiet) }},
		{"rebuild", func() error { return setBoolFlag(cmd, "rebuild", lc.Rebuild) }},
		{"cpus", func() error { return setUint8Flag(cmd, "cpus", lc.CPUs) }},
		{"memory", func() error { return setStringFlag(cmd, "memory", lc.Memory) }},
	}
	for _, item := range apply {
		if keys[item.key] {
			if err := item.fn(); err != nil {
				return fmt.Errorf("apply launcher config %q: %w", item.key, err)
			}
		}
	}
	return nil
}

func setBoolFlag(cmd *cobra.Command, name string, val bool) error {
	f := cmd.Flags().Lookup(name)
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatBool(val))
}

func setUint8Flag(cmd *cobra.Command, name string, val uint8) error {
	f := cmd.Flags().Lookup(name)
	if f == nil || f.Changed {
		return nil
	}
	return f.Value.Set(strconv.FormatUint(uint64(val), 10))
}

func setStringFlag(cmd *cobra.Command, name string, val string) error {
	f := cmd.Flags().Lookup(name)
	if f == nil || f.Changed || val == "" {
		return nil
	}
	return f.Value.Set(val)
}
```

- [ ] **Step 5: Run the CLI tests**

```bash
go test ./cmd/opencode-msb/...
```

Expected: all tests pass.

- [ ] **Step 6: Run the linter**

```bash
golangci-lint run ./cmd/opencode-msb/...
```

Expected: no issues.

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-msb/cli.go cmd/opencode-msb/cli_test.go internal/sandbox/runner.go
git commit -m "feat(cli): wire launcher config into CLI flags"
```

---

## Task 4: Merge user and project env/env.secret files

**Files:**
- Modify: `internal/sandbox/runner.go`
- Modify: `internal/sandbox/runner_test.go`

**Interfaces:**
- Consumes: `cfg.UserLauncherDir`
- Produces: `mergeEnvMaps(maps ...map[string]string) map[string]string`

- [ ] **Step 1: Write the failing env-merge test**

Add to `internal/sandbox/runner_test.go`:

```go
func TestMergeEnvMapsProjectOverridesUser(t *testing.T) {
	userFile := filepath.Join(t.TempDir(), "env")
	writeFile(t, userFile, "FOO=user\nBAR=user\n")
	projectFile := filepath.Join(t.TempDir(), "env")
	writeFile(t, projectFile, "FOO=project\n")

	got := mergeEnvMaps(buildEnvMap(userFile), buildEnvMap(projectFile))
	want := map[string]string{"FOO": "project", "BAR": "user"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/sandbox/... -run TestMergeEnvMapsProjectOverridesUser
```

Expected: compile error because `mergeEnvMaps` does not exist.

- [ ] **Step 3: Implement the merge and wire it into createSandbox**

Add `mergeEnvMaps` to `internal/sandbox/runner.go` near `buildEnvMap`:

```go
func mergeEnvMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}
```

In `createSandbox`, replace:

```go
envMap := buildEnvMap(".opencode-msb/env")
secrets := BuildSecrets(buildEnvMap(".opencode-msb/env.secret"), logger)
```

with:

```go
envMap := mergeEnvMaps(
	buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env")),
	buildEnvMap(".opencode-msb/env"),
)
secrets := BuildSecrets(mergeEnvMaps(
	buildEnvMap(filepath.Join(cfg.UserLauncherDir, "env.secret")),
	buildEnvMap(".opencode-msb/env.secret"),
), logger)
```

- [ ] **Step 4: Run the sandbox tests**

```bash
go test ./internal/sandbox/...
```

Expected: all tests pass.

- [ ] **Step 5: Run the linter**

```bash
golangci-lint run ./internal/sandbox/...
```

Expected: no issues.

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/runner_test.go
git commit -m "feat(sandbox): merge user and project env/env.secret files"
```

---

## Task 5: Update documentation

**Files:**
- Modify: `README.md`

**Interfaces:**
- Produces: documented user-level and project-level env/secret/config behavior.

- [ ] **Step 1: Add documentation section**

Insert a new section in `README.md` after the existing **Project overrides**
heading:

```markdown
### User-level defaults

You can also put defaults in `~/.config/opencode-msb/` so they apply to every
project unless overridden:

- `~/.config/opencode-msb/env` — environment variables forwarded to every sandbox.
- `~/.config/opencode-msb/env.secret` — secret environment variables in
  `value@host` format.
- `~/.config/opencode-msb/config.*` — launcher defaults for CLI flags.

Supported config names are `config.yaml`, `config.yml`, `config.json`,
`config.jsonc`, and `config.json5`. The first one found in the directory is
used.

Example `~/.config/opencode-msb/config.yaml`:

```yaml
verbose: true
cpus: 4
memory: 8G
```

Example `.opencode-msb/config.yaml` that overrides the user default only for
this project:

```yaml
memory: 16G
rebuild: true
```

Precedence for both env files and launcher config is:

1. Built-in defaults
2. `~/.config/opencode-msb/`
3. `.opencode-msb/` — project-level values win
4. CLI flags — always win
```

- [ ] **Step 2: Proofread the README**

```bash
git diff README.md
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document launcher config and user env files"
```

---

## Task 6: Final verification

**Files:**
- All of the above

- [ ] **Step 1: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 2: Run the linter**

```bash
golangci-lint run
```

Expected: no issues.

- [ ] **Step 3: Dry-run the CLI**

```bash
go run ./cmd/opencode-msb --dry-run
```

Expected: setup validates successfully (it should not start opencode).

- [ ] **Step 4: Commit any final fixes**

```bash
git add -A
git commit -m "chore: final verification and fixes" || echo "nothing to commit"
```

---

## Self-review checklist

- [x] Spec coverage: env files from both locations, launcher config from both
  locations, project override, CLI override, Viper + JSON5 handling, tests,
  docs — all map to tasks above.
- [x] Placeholder scan: no TBD, TODO, or vague "add validation" steps.
- [x] Type consistency: `sandbox.Config` gains `UserLauncherDir`; `Config`
  struct, `Load` signature, and flag helper names are consistent across tasks.
