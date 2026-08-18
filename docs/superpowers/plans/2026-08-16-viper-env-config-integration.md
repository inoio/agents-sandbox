# Cobra/Viper env + config integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire cobra + viper so launcher config settings resolve with `flag > env > config > default` precedence via a single `OPENCODE_SANDBOX_` env prefix, and split durable defaults from per-command action toggles.

**Architecture:** A typed `viperconfig.Resolver` built in cobra's `PersistentPreRunE` becomes the single read source for config-backed values. It merges config files, sets up env (`OPENCODE_SANDBOX_` prefix), binds config-backed flags (`BindPFlag`) and mirrors their defaults (`SetDefault`), then unmarshals into the existing `Config` struct (minus `rebuild`). The resolver travels on the command context; commands read via typed getters. Action toggles (rebuild, dry-run, force, ...) stay on `cmd.Flags()`.

**Tech Stack:** Go, `github.com/spf13/cobra`, `github.com/spf13/viper`, `github.com/go-viper/mapstructure/v2`, `github.com/titanous/json5`.

**Spec:** `docs/superpowers/specs/2026-08-16-viper-env-config-integration-design.md`

## Global Constraints

- Config-backed keys (env + config): `cpus`, `memory`, `tmp-size`, `disk-size`, `yes`, `verbose`, `quiet`, `auto-prune-age`, `manual-prune-age`, `auto-stop-on-active-sessions`, `auto-stop-timeout`, `auto-stop-max-session-retries`.
- Excluded from env/config (CLI-only action toggles): `rebuild`, `dry-run`, `dry-run-vm`, `force`, `remove`, `worktree`, `serve-only`, `root`, `age`, `opencode-version`.
- Single env prefix `OPENCODE_SANDBOX_`, key replacer `-` → `_` (e.g. `auto-stop-on-active-sessions` → `OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS`).
- Precedence: flag (only when explicitly changed) > env > config file (project overrides user) > default.
- An unspecified flag with a default must NOT override env/config.
- `rebuild` is removed from the `Config` struct and the config schema; a config file setting it is ignored silently.
- `docs/configuration.md` and `README.md` must be updated to match.
- All code must pass `make check` (fmt, lint, test).

---

### Task 1: Add `Resolver`, `NewResolver`, and typed getters to `viperconfig`

**Files:**
- Modify: `internal/viperconfig/viperconfig.go`
- Test: `internal/viperconfig/viperconfig_test.go`

**Interfaces:**
- Produces:
  - `type Resolver struct { ... }` with unexported `cfg Config`.
  - `func NewResolver(cmd *cobra.Command) (*Resolver, error)` — loads config files, sets env, binds config-backed flags on `cmd`, mirrors flag defaults, validates, unmarshals into `Config`.
  - `func NewResolverWithConfig(cfg Config) *Resolver` — test-support constructor; returns a resolver whose getters return `cfg` unchanged.
  - Getters: `CPUs() uint8`, `Memory() string`, `TmpSize() string`, `DiskSize() string`, `Yes() bool`, `Verbose() bool`, `Quiet() bool`, `AutoPruneAge() time.Duration`, `ManualPruneAge() time.Duration`, `AutoStopOnActiveSessions() bool`, `AutoStopTimeout() time.Duration`, `AutoStopMaxSessionRetries() int`, `IdleTimeout() time.Duration`.
  - `Config` struct stays as-is (still contains `Rebuild bool` for now; it is not read by any getter and is removed in Task 3).
  - Existing `Load`, `ParseHumanDuration`, `durationDecodeHook`, `Config`, `validate`, `mergeDir`, `findConfigFile`, `configType` remain.

**Notes for the implementer:**
- `NewResolver` must be additive; it does not modify or remove the existing `Load`. The `cmd` package still uses `Load` until Task 3.
- `cobra` and `pflag` imports are new for this package.

- [ ] **Step 1: Write the failing tests**

Add to `internal/viperconfig/viperconfig_test.go` (keep existing tests untouched):

```go
func TestResolverGettersReturnConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cfg := Config{
		CPUs: 4, Memory: "8G", TmpSize: "4G", DiskSize: "32G",
		Yes: true, Verbose: true,
		AutoPruneAge: 7 * 24 * time.Hour, ManualPruneAge: 14 * 24 * time.Hour,
		AutoStopOnActiveSessions: true, AutoStopTimeout: 30 * time.Second, AutoStopMaxSessionRetries: 5,
	}
	r := NewResolverWithConfig(cfg)
	if r.CPUs() != 4 || r.Memory() != "8G" || r.TmpSize() != "4G" || r.DiskSize() != "32G" {
		t.Errorf("resource getters mismatch: %+v", cfg)
	}
	if !r.Yes() || !r.Verbose() || r.Quiet() {
		t.Error("UI getters mismatch")
	}
	if r.AutoPruneAge() != 7*24*time.Hour || r.ManualPruneAge() != 14*24*time.Hour {
		t.Error("prune getters mismatch")
	}
	if !r.AutoStopOnActiveSessions() || r.AutoStopTimeout() != 30*time.Second || r.AutoStopMaxSessionRetries() != 5 {
		t.Error("autostop getters mismatch")
	}
	if r.IdleTimeout() != 30*time.Second {
		t.Errorf("IdleTimeout = %v; want 30s", r.IdleTimeout())
	}
}

func TestResolverIdleTimeoutDefault(t *testing.T) {
	r := NewResolverWithConfig(Config{})
	if r.IdleTimeout() != 10*time.Second {
		t.Errorf("IdleTimeout default = %v; want 10s", r.IdleTimeout())
	}
}

func TestResolverEnvPrecedenceOverConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("OPENCODE_SANDBOX_CPUS", "6")

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 6 {
		t.Errorf("CPUs = %d; want 6 (env overrides config)", r.CPUs())
	}
}

func TestResolverConfigNoFlag(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 3})

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 3 {
		t.Errorf("CPUs = %d; want 3", r.CPUs())
	}
}

func TestResolverEnvKeyReplacement(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS", "true")

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if !r.AutoStopOnActiveSessions() {
		t.Error("expected AutoStopOnActiveSessions true from env")
	}
}

func TestResolverEnvInvalidCPUs(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("OPENCODE_SANDBOX_CPUS", "300")

	if _, err := NewResolver(nil); err == nil {
		t.Fatal("expected error for cpus=300 from env")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/viperconfig/ -run 'TestResolver' -v`
Expected: FAIL — `NewResolver` / `NewResolverWithConfig` / `Resolver` undefined.

- [ ] **Step 3: Implement `Resolver`, `NewResolver`, `NewResolverWithConfig`, and getters**

In `internal/viperconfig/viperconfig.go`, add these imports:

```go
import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)
```

Add the `Resolver` type and constructors. Insert after the `Config` struct (around line 38):

```go
// Resolver resolves launcher config with precedence flag > env > config > default.
type Resolver struct {
	cfg Config
}

// NewResolver builds a Resolver, loading config files, configuring the
// OPENCODE_SANDBOX_ env prefix, binding config-backed flags on cmd, and
// validating. cmd may be nil to skip flag binding.
func NewResolver(cmd *cobra.Command) (*Resolver, error) {
	v := viper.New()

	if err := mergeDir(v, configpaths.Get().UserConfigDir()); err != nil {
		return nil, err
	}
	if err := mergeDir(v, configpaths.Get().ProjectConfigDir()); err != nil {
		return nil, err
	}

	v.SetEnvPrefix("OPENCODE_SANDBOX")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	for _, key := range configFlagKeys {
		if err := v.BindEnv(key); err != nil {
			return nil, err
		}
	}

	if cmd != nil {
		if err := bindConfigFlags(v, cmd); err != nil {
			return nil, err
		}
	}

	if err := validate(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
		),
	)); err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	return &Resolver{cfg: cfg}, nil
}

// NewResolverWithConfig builds a Resolver from an explicit Config. It is
// used by callers (notably cmd tests) that need a resolver with known values
// without touching config files or env.
func NewResolverWithConfig(cfg Config) *Resolver {
	return &Resolver{cfg: cfg}
}
```

Add the config-backed key list and flag-binding helper. Insert after the const block (around line 48):

```go
// configFlagKeys are the config-backed keys that are also exposed as CLI flags.
// Their env vars use the OPENCODE_SANDBOX_ prefix.
var configFlagKeys = []string{
	"cpus", "memory", "tmp-size", "disk-size",
	"yes", "verbose", "quiet",
}
```

Add `bindConfigFlags` (uses the `setXFlag`-style precedence but via viper):

```go
// bindConfigFlags binds each config-backed flag found on cmd (local or
// inherited) to viper and mirrors its declared default so that an
// unspecified flag with a default does not override env/config.
func bindConfigFlags(v *viper.Viper, cmd *cobra.Command) error {
	for _, key := range configFlagKeys {
		flag := findFlag(cmd, key)
		if flag == nil {
			continue
		}
		if err := v.BindPFlag(key, flag); err != nil {
			return fmt.Errorf("bind flag %q: %w", key, err)
		}
		v.SetDefault(key, flagTypedDefault(key, flag))
	}
	return nil
}

func findFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

func flagTypedDefault(key string, flag *pflag.Flag) any {
	switch key {
	case "cpus":
		n, _ := strconv.ParseUint(flag.DefValue, 10, 8)
		return uint8(n)
	case "yes", "verbose", "quiet":
		return flag.DefValue == "true"
	default:
		return flag.DefValue
	}
}
```

Add the typed getters after the `IdleTimeout` method (end of file):

```go
func (r *Resolver) CPUs() uint8                        { return r.cfg.CPUs }
func (r *Resolver) Memory() string                     { return r.cfg.Memory }
func (r *Resolver) TmpSize() string                    { return r.cfg.TmpSize }
func (r *Resolver) DiskSize() string                   { return r.cfg.DiskSize }
func (r *Resolver) Yes() bool                          { return r.cfg.Yes }
func (r *Resolver) Verbose() bool                      { return r.cfg.Verbose }
func (r *Resolver) Quiet() bool                        { return r.cfg.Quiet }
func (r *Resolver) AutoPruneAge() time.Duration        { return r.cfg.AutoPruneAge }
func (r *Resolver) ManualPruneAge() time.Duration      { return r.cfg.ManualPruneAge }
func (r *Resolver) AutoStopOnActiveSessions() bool     { return r.cfg.AutoStopOnActiveSessions }
func (r *Resolver) AutoStopTimeout() time.Duration     { return r.cfg.AutoStopTimeout }
func (r *Resolver) AutoStopMaxSessionRetries() int     { return r.cfg.AutoStopMaxSessionRetries }
func (r *Resolver) IdleTimeout() time.Duration         { return r.cfg.IdleTimeout() }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/viperconfig/ -run 'TestResolver' -v`
Expected: PASS. Then run the full package: `go test ./internal/viperconfig/` — existing `Load` tests must still pass.

- [ ] **Step 5: Run `go mod tidy` and commit**

```bash
go mod tidy
git add internal/viperconfig/viperconfig.go internal/viperconfig/viperconfig_test.go
git commit -m "feat(viperconfig): add typed Resolver with env and flag binding"
```

---

### Task 2: Rewrite `viperconfig` tests for precedence, env, and `rebuild` exclusion

**Files:**
- Modify: `internal/viperconfig/viperconfig_test.go`

**Interfaces:**
- Consumes: `NewResolver(cmd *cobra.Command) (*Resolver, error)`, `NewResolverWithConfig(Config)`, getters from Task 1.
- Produces: (no new interfaces)

**Notes:**
- The `Config` struct still contains `Rebuild` until Task 3. Tests must stop asserting on `Rebuild` now, so Task 3 can remove the field cleanly.

- [ ] **Step 1: Update tests that reference `Rebuild` and `Load`'s `keys` map**

In `TestLoadMissingFilesReturnsDefaults` (currently asserts `cfg.Rebuild` at line 23) remove the `cfg.Rebuild` reference:

```go
if cfg.CPUs != 0 || cfg.Memory != "" || cfg.TmpSize != "" || cfg.DiskSize != "" || cfg.Yes || cfg.Verbose ||
	cfg.Quiet || cfg.AutoPruneAge != 0 || cfg.ManualPruneAge != 0 {
	t.Errorf("expected zero defaults, got %+v", cfg)
}
```

In `TestLoadYAMLConfig` (currently writes `"rebuild": true` and asserts `cfg.Rebuild` at lines 35/52): remove the `"rebuild": true` entry from the written config and the `!cfg.Rebuild` assertion:

```go
testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{
	"cpus":     4,
	"memory":   "8G",
	"tmp-size": "4G",
	"verbose":  true,
})
```

and:

```go
if !cfg.Verbose {
	t.Errorf("expected verbose true, got %+v", cfg)
}
```

- [ ] **Step 2: Run tests to verify the updated tests pass**

Run: `go test ./internal/viperconfig/`
Expected: PASS (the file-loaded `Load` tests still exercise the existing path).

- [ ] **Step 3: Add precedence and env integration tests**

Add these tests to `internal/viperconfig/viperconfig_test.go`:

```go
// Flag-over-env-over-config precedence via a real cobra command.
func TestResolverFlagOverridesEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2})
	t.Setenv("OPENCODE_SANDBOX_CPUS", "4")

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().Uint8("cpus", 0, "")
	if err := root.ParseFlags([]string{"--cpus", "6"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 6 {
		t.Errorf("CPUs = %d; want 6 (explicit flag overrides env/config)", r.CPUs())
	}
}

// An unspecified flag with a default must NOT override env/config.
func TestResolverUnspecifiedFlagDefaultDoesNotOverride(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"memory": "8G"})

	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "") // default 4G, not changed
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Memory() != "8G" {
		t.Errorf("Memory = %q; want 8G (config beats unspecified flag default)", r.Memory())
	}
}

// With no env/config, an unspecified flag's default is the resolution.
func TestResolverFlagDefaultUsedWhenNothingElse(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("memory", "4G", "")
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Memory() != "4G" {
		t.Errorf("Memory = %q; want 4G (flag default)", r.Memory())
	}
}

// rebuild is not a config-backed key; a config file setting it is ignored.
func TestResolverIgnoresRebuildKey(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"rebuild": true, "cpus": 2})

	r, err := NewResolver(nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.CPUs() != 2 {
		t.Errorf("CPUs = %d; want 2", r.CPUs())
	}
	// There is no Rebuild getter; the field is dropped silently.
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/viperconfig/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/viperconfig/viperconfig_test.go
git commit -m "test(viperconfig): cover resolver precedence, env, and rebuild exclusion"
```

---

### Task 3: Wire the resolver into the CLI and remove the hand-rolled config application

**Files:**
- Modify: `cmd/opencode-sandbox/cli.go`
- Modify: `cmd/opencode-sandbox/commands.go`
- Modify: `cmd/opencode-sandbox/commands_system.go`
- Modify: `internal/viperconfig/viperconfig.go` (remove `Rebuild` field and `Load`)

**Interfaces:**
- Consumes: `viperconfig.NewResolver(cmd)`, `*viperconfig.Resolver` getters.
- Produces:
  - Context carries `*viperconfig.Resolver` under key `launcherConfigKey`.
  - `extractRunOptions` reads config-backed values from the resolver.
  - `applyCLISettings(cmd, ui, r *viperconfig.Resolver)`.
  - `buildPruneCmd` uses `resolver.ManualPruneAge()`.
  - Deletes `applyLauncherConfig`, `setBoolFlag`, `setUint8Flag`, `setStringFlag`, `setDurationFlag`.

**Notes for the implementer:**
- This task touches three source files that must compile together; complete all edits before building.
- `commands_system.go`'s `buildPruneCmd` needs access to the resolver from the command context. Read it from `cmd.Context()` in the `RunE`, mirroring how `extractRunOptions` reads it.

- [ ] **Step 1: Remove the `Rebuild` field and `Load` from `viperconfig`**

In `internal/viperconfig/viperconfig.go`:
- Remove `Rebuild bool` from the `Config` struct.
- Remove the `Load` function (lines ~104-130) and the `keys` map logic. `NewResolver` replaces it. Remove the now-unused `Load` references. `validate`, `mergeDir`, `findConfigFile`, `configType`, `ParseHumanDuration`, `durationDecodeHook` remain.

- [ ] **Step 2: Update `cmd/opencode-sandbox/cli.go`**

Change the context-key comment and `applyCLISettings` to read from a resolver. Replace the `applyLauncherConfig` and `setXFlag` helpers with nothing (delete them). Update imports if `strconv`/`time`/`launcherconfig` become unused.

```go
func applyCLISettings(cmd *cobra.Command, ui termio.UI, r *viperconfig.Resolver) {
	if cmd == nil || r == nil {
		return
	}
	quiet := r.Quiet()
	verbose := r.Verbose()
	yes := r.Yes()
	ui.SetLevel(levelFrom(quiet, verbose))
	ui.SetAssumeYes(yes)
}
```

Delete `applyLauncherConfig`, `setBoolFlag`, `setUint8Flag`, `setStringFlag`, `setDurationFlag`. The `applyCLISettings` callers in `commands.go` will pass the resolver (updated in Step 4).

- [ ] **Step 3: Update `cmd/opencode-sandbox/commands.go` — context key and `PersistentPreRunE`**

Change the context key doc comment and the value type. `launcherConfigKey` now carries `*viperconfig.Resolver`:

```go
// launcherConfigKey is the context key type for storing the built
// viperconfig.Resolver between PersistentPreRunE and command RunE.
type launcherConfigKey struct{}
```

Update `PersistentPreRunE`:

```go
rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
	r, err := launcherconfig.NewResolver(cmd)
	if err != nil {
		return err
	}
	cmd.SetContext(context.WithValue(cmd.Context(), (*launcherConfigKey)(nil), r))
	applyCLISettings(cmd, ui, r)

	isDryRun, _ := cmd.Flags().GetBool(flagDryRun)
	pruning.AutoPrune(cmd.Context(), r.AutoPruneAge(), isDryRun, ui)
	return nil
}
```

- [ ] **Step 4: Update `cmd/opencode-sandbox/commands.go` — `extractRunOptions`**

Read config-backed values from the resolver. Replace the current reads:

```go
func extractRunOptions(cmd *cobra.Command, ui termio.UI) (options.RunOptions, error) {
	opts := options.RunOptions{}
	rawWorktree, _ := cmd.Flags().GetString(flagWorktree)
	worktree, err := session.ResolveWorktreeSpec(rawWorktree)
	if err != nil {
		return options.RunOptions{}, err
	}
	opts.Worktree = worktree
	opts.Rebuild, _ = cmd.Flags().GetBool(flagRebuild)
	opts.DryRun, _ = cmd.Flags().GetBool(flagDryRun)
	opts.DryRunVM, _ = cmd.Flags().GetBool(flagDryRunVM)
	if opts.DryRun {
		opts.DryRunVM = true
		ui.Verbosef("dry-run-vm: auto-enabled (--dry-run)")
	}
	opts.ServeOnly, _ = cmd.Flags().GetBool(flagServeOnly)
	if cmd.Flags().Lookup(flagRoot) != nil {
		opts.Root, _ = cmd.Flags().GetBool(flagRoot)
	}

	r := resolverFromContext(cmd.Context())
	if r != nil {
		opts.CPUs = r.CPUs()
		opts.Memory = r.Memory()
		opts.TmpSize = r.TmpSize()
		opts.DiskSize = r.DiskSize()
		opts.ReapPolicy = options.NewReapPolicy(r.AutoStopOnActiveSessions(), r.AutoStopMaxSessionRetries())
		opts.IdleTimeout = r.IdleTimeout()
	}

	if opts.TmpSize != "" {
		if _, ok := options.ParseMemoryOK(opts.TmpSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --tmp-size %q: expected a size like 4G, 512M, or 2048",
				opts.TmpSize,
			)
		}
	}
	if opts.DiskSize != "" {
		if _, ok := options.ParseMemoryOK(opts.DiskSize); !ok {
			return options.RunOptions{}, fmt.Errorf(
				"invalid --disk-size %q: expected a size like 16G, 512M, or 4096",
				opts.DiskSize,
			)
		}
	}
	return opts, nil
}

// resolverFromContext returns the viperconfig.Resolver stored on the context,
// or nil if absent.
func resolverFromContext(ctx context.Context) *launcherconfig.Resolver {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value((*launcherConfigKey)(nil)).(*launcherconfig.Resolver)
	return r
}
```

Note: the old context read at the top (`if lc, ok := ctx.Value(...).(launcherconfig.Config); ok {...}`) is replaced by the `resolverFromContext` block above.

- [ ] **Step 5: Update `cmd/opencode-sandbox/commands_system.go` — `buildPruneCmd`**

Make `prune` fall back to the resolver's `ManualPruneAge()` when `--age` is unset:

```go
RunE: func(cmd *cobra.Command, _ []string) error {
	ageStr, _ := cmd.Flags().GetString(flagAge)
	var age time.Duration
	if ageStr != "" {
		d, ok := viperconfig.ParseHumanDuration(ageStr)
		if !ok {
			return fmt.Errorf("invalid age %q: use a Go duration or suffix d/w (e.g. 7d, 2w)", ageStr)
		}
		age = d
	}
	if age == 0 {
		if r := resolverFromContext(cmd.Context()); r != nil && r.ManualPruneAge() > 0 {
			age = r.ManualPruneAge()
		} else {
			age = 7 * 24 * time.Hour
		}
	}
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	return pruning.Prune(cmd.Context(), age, dryRun, false, ui)
},
```

- [ ] **Step 6: Build and run the full test suite**

Run: `go build ./... && go test ./cmd/opencode-sandbox/... ./internal/viperconfig/...`
Expected: Tests that still call `applyLauncherConfig` / `Load` / reference `launcherconfig.Config` will fail to compile. Those are fixed in Task 4. First confirm the production code compiles: `go build ./...` must succeed.

- [ ] **Step 7: Commit**

```bash
git add cmd/opencode-sandbox/cli.go cmd/opencode-sandbox/commands.go cmd/opencode-sandbox/commands_system.go internal/viperconfig/viperconfig.go
git commit -m "feat(cli): resolve config through viper Resolver, drop hand-rolled application"
```

---

### Task 4: Update `cmd/opencode-sandbox` tests for the resolver

**Files:**
- Modify: `cmd/opencode-sandbox/cli_test.go`
- Modify: `cmd/opencode-sandbox/cli_run_options_test.go`

**Interfaces:**
- Consumes: `viperconfig.NewResolverWithConfig`, `*viperconfig.Resolver`, `resolverFromContext`.

**Notes:**
- `TestApplyLauncherConfigSetsUnsetFlags`, `TestApplyLauncherConfigRespectsCLIOverrides`, `TestApplyLauncherConfigSetsDiskSize` are deleted/replaced because `applyLauncherConfig` no longer exists.
- `cli_run_options_test.go` helpers inject a `viperconfig.Config` into context; they now inject a `*viperconfig.Resolver` built via `NewResolverWithConfig`.

- [ ] **Step 1: Update `cli_run_options_test.go` context helpers to inject a resolver**

Replace the three helper functions (`buildCommandWithLauncherConfig`, `buildShellCommandWithLauncherConfig`, `buildCommandWithoutLauncherConfig`):

```go
// buildCommandWithLauncherConfig builds a "run" command and injects a
// viperconfig.Resolver built from lc into the context, mimicking what
// PersistentPreRunE does in production.
func buildCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	cmd := buildRunCmd(ui)
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), launcherconfig.NewResolverWithConfig(lc))
	cmd.SetContext(rootCtx)
	return cmd
}

// buildShellCommandWithLauncherConfig builds a "shell" command and injects
// a resolver built from lc, verifying the shell path.
func buildShellCommandWithLauncherConfig(ui termio.UI, lc launcherconfig.Config) *cobra.Command {
	pet := buildShellCmd(ui)
	petCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), launcherconfig.NewResolverWithConfig(lc))
	pet.SetContext(petCtx)
	return pet
}

// buildCommandWithoutLauncherConfig builds a "run" command with no resolver
// in its context, exercising the absent-config path.
func buildCommandWithoutLauncherConfig(ui termio.UI) *cobra.Command {
	cmd := buildRunCmd(ui)
	return cmd
}
```

The test bodies (`TestExtractRunOptions*`) need no change — they still build a `Config` and pass it to the helper; the helper wraps it in a resolver.

- [ ] **Step 2: Remove the deleted `applyLauncherConfig` tests from `cli_test.go`**

Delete `TestApplyLauncherConfigSetsUnsetFlags` (lines 76-121), `TestApplyLauncherConfigRespectsCLIOverrides` (lines 167-218), and `TestApplyLauncherConfigSetsDiskSize` (lines 317-333). Remove the now-unused `launcherconfig` import from `cli_test.go` if it becomes unused.

- [ ] **Step 3: Add CLI-level precedence tests**

Add to `cli_test.go`:

```go
func TestCLIConfigPrecedenceViaResolver(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	testutil.WriteYAML(t, cp.UserConfigDir(), "config.yaml", map[string]any{"cpus": 2, "memory": "8G"})

	ui := &termio.Mock{}
	root := buildRootCmd(ui)
	runCmd, _, _ := root.Find([]string{"run"})
	if err := runCmd.ParseFlags([]string{"--cpus", "6"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	rootCtx := context.WithValue(context.Background(), (*launcherConfigKey)(nil), mustResolver(t, runCmd))
	runCmd.SetContext(rootCtx)

	opts, err := extractRunOptions(runCmd, ui)
	if err != nil {
		t.Fatalf("extractRunOptions: %v", err)
	}
	if opts.CPUs != 6 {
		t.Errorf("CPUs = %d; want 6 (flag overrides config)", opts.CPUs)
	}
	if opts.Memory != "8G" {
		t.Errorf("Memory = %q; want 8G (config, flag unspecified)", opts.Memory)
	}
}

// mustResolver builds a resolver from a real command, failing the test on error.
func mustResolver(t *testing.T, cmd *cobra.Command) *launcherconfig.Resolver {
	t.Helper()
	r, err := launcherconfig.NewResolver(cmd)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}
```

Add the needed imports to `cli_test.go`: `context`, `"github.com/inoio/opencode-sandbox/internal/testutil"`, `launcherconfig` (if removed in Step 2, re-add).

- [ ] **Step 4: Run the `cmd` test suite**

Run: `go test ./cmd/opencode-sandbox/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/opencode-sandbox/cli_test.go cmd/opencode-sandbox/cli_run_options_test.go
git commit -m "test(cli): update for viper Resolver context"
```

---

### Task 5: Update documentation

**Files:**
- Modify: `docs/configuration.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing; documents the new behavior.

**Notes:**
- The existing "Environment Variables" section of `docs/configuration.md` is about sandbox-internal env files. Add a distinct subsection for launcher configuration env vars.

- [ ] **Step 1: Update `docs/configuration.md` — remove `rebuild` from the config table**

In the "Configuration file" table, delete the `rebuild` row:

```markdown
| `rebuild`                       | `--rebuild` / `-r`     | Rebuild runner image before starting                                                                                     |
```

- [ ] **Step 2: Update the precedence section to include env vars**

Replace the "Precedence" list to add env vars and the `OPENCODE_SANDBOX_` prefix:

```markdown
## Precedence

Configuration is resolved in this order (later entries override earlier ones):

1. **Built-in / flag defaults** — compiled-in values and CLI flag defaults
2. **User-level** — `~/.config/opencode-sandbox/`
3. **Project-level** — `.opencode-sandbox/`
4. **Environment variables** — `OPENCODE_SANDBOX_<KEY>`
5. **CLI flags** — always win when explicitly passed
```

- [ ] **Step 3: Add a launcher-config env-var subsection**

After the "Configuration file" section (before "Environment Variables"), add:

```markdown
### Launcher configuration environment variables

Every config-file field above can also be set with an environment variable. Env vars take
precedence over config files but lose to an explicitly passed CLI flag. The prefix is
`OPENCODE_SANDBOX_`; dashes in the field name become underscores.

| Field                           | Environment variable                                              |
|---------------------------------|-------------------------------------------------------------------|
| `yes`                           | `OPENCODE_SANDBOX_YES`                                            |
| `verbose`                       | `OPENCODE_SANDBOX_VERBOSE`                                        |
| `quiet`                         | `OPENCODE_SANDBOX_QUIET`                                          |
| `cpus`                          | `OPENCODE_SANDBOX_CPUS`                                           |
| `memory`                        | `OPENCODE_SANDBOX_MEMORY`                                         |
| `disk-size`                     | `OPENCODE_SANDBOX_DISK_SIZE`                                      |
| `tmp-size`                      | `OPENCODE_SANDBOX_TMP_SIZE`                                       |
| `auto-prune-age`                | `OPENCODE_SANDBOX_AUTO_PRUNE_AGE`                                 |
| `manual-prune-age`              | `OPENCODE_SANDBOX_MANUAL_PRUNE_AGE`                               |
| `auto-stop-on-active-sessions`  | `OPENCODE_SANDBOX_AUTO_STOP_ON_ACTIVE_SESSIONS`                   |
| `auto-stop-timeout`             | `OPENCODE_SANDBOX_AUTO_STOP_TIMEOUT`                              |
| `auto-stop-max-session-retries` | `OPENCODE_SANDBOX_AUTO_STOP_MAX_SESSION_RETRIES`                  |

Action toggles (`--rebuild`, `--dry-run`, `--force`, ...) are CLI-only and cannot be set via
config file or env var.
```

- [ ] **Step 4: Update `README.md`**

Search `README.md` for any config-file field table or precedence list. If it duplicates the
`docs/configuration.md` table, remove the `rebuild` row and add a one-line pointer to the
env-var subsection. If `README.md` only links to `docs/configuration.md`, no change beyond
verifying the link text still matches. Run a quick `grep -n "rebuild\|OPENCODE_SANDBOX\|precedence" README.md` to decide.

- [ ] **Step 5: Run `make check` and commit**

Run: `make check`
Expected: fmt, lint, and all tests pass.

```bash
git add docs/configuration.md README.md
git commit -m "docs: document OPENCODE_SANDBOX_ env vars and env/config precedence"
```

---

## Self-review

**Spec coverage:**
- Single `OPENCODE_SANDBOX_` prefix + key replacement → Task 1 (`SetEnvPrefix`, `SetEnvKeyReplacer`, `BindEnv`).
- Precedence flag > env > config > default → Task 1 (`BindPFlag` + `HasChanged`) and Task 2 (`TestResolverFlagOverridesEnv`, `TestResolverUnspecifiedFlagDefaultDoesNotOverride`).
- An unspecified flag default does not override config → Task 2 (`TestResolverUnspecifiedFlagDefaultDoesNotOverride`) + `SetDefault` mirroring in Task 1.
- `rebuild` removed from schema → Task 2 (`TestResolverIgnoresRebuildKey`), Task 3 (field removal).
- Deletes `applyLauncherConfig` / `setXFlag` / `keys` → Task 3.
- Typed resolver on context → Task 3 (`launcherConfigKey` carries `*Resolver`, `resolverFromContext`).
- Action toggles stay CLI-only → Task 3 (they remain on `cmd.Flags()`).
- Validation applies to env values → Task 1 (`TestResolverEnvInvalidCPUs`).
- Docs → Task 5.

**Placeholder scan:** No TBD/TODO; all code blocks are concrete.

**Type consistency:**
- `resolverFromContext` returns `*viperconfig.Resolver`; used in `extractRunOptions` (Task 3) and `buildPruneCmd` (Task 3).
- Getters `CPUs() uint8`, `Memory() string`, `TmpSize() string`, `DiskSize() string`, `AutoStopOnActiveSessions() bool`, `AutoStopMaxSessionRetries() int`, `IdleTimeout() time.Duration`, `ManualPruneAge() time.Duration` match the `options.RunOptions` fields and `options.NewReapPolicy` signature.
- `NewResolverWithConfig(Config) *Resolver` matches its use in `cli_run_options_test.go` (Task 4).
- `applyCLISettings(cmd, ui, r *viperconfig.Resolver)` matches the call in `PersistentPreRunE` (Task 3).

One cross-task note: `configpaths` and `testutil` imports are already present in the test packages (verified against `viperconfig_test.go`); Task 4's `cli_test.go` additions may require adding `context` and `testutil` imports — Step 3 calls this out.