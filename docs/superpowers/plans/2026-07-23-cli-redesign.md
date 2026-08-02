# CLI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the opencode-msb CLI to align with msb conventions: flat verbs with noun-group aliases, global persistent flags, extracted subcommands for standalone actions, and a reworked README.

**Architecture:** The CLI uses cobra. The root command dispatches to subcommands (`run`, `doctor`, `build`, `list`, `shell`, `config`, `image`, `volume`). Bare invocation implicitly runs `run`. Global persistent flags (`--yes`, `--verbose`, `--quiet`, `--tree`) apply to all commands. The `Run` function in runner.go is refactored to extract a shared `prepareSandbox` function used by both `run` and `shell`.

**Tech Stack:** Go 1.26, cobra v1.10.2, microsandbox SDK, Docker SDK, golangci-lint v2

## Global Constraints

- Go module: `gitlab.inoio.de/inoio/opencode-msb`
- Max line length: 120 chars (golines)
- Import grouping: std, third-party, then `gitlab.inoio.de/inoio/opencode-msb` (goimports local prefix)
- Linter: `golangci-lint run` must pass with zero issues
- Tests: `CGO_ENABLED=1 go test ./...` must pass
- No comments unless code is not self-explanatory (AGENTS.md convention)
- No `init()` functions (golangci-lint gochecknoinits)
- No global variables except `//nolint:gochecknoglobals` with justification
- Run `gofmt -w .` before every commit
- Version flag: `var version = "dev"` (set at link time)

---

### Task 1: Logger Refactor

Add log levels (Normal, Verbose, Quiet) to the logger, including a new `Debug` method and level-aware filtering. The spinner must also respect the level (hidden in quiet mode).

**Files:**
- Modify: `internal/log/log.go`
- Modify: `internal/log/log_test.go`
- Modify: `internal/log/spinner.go`
- Modify: `internal/log/spinner_test.go`

**Interfaces:**
- Produces: `log.Level` type, `log.NewWithLevel(w ui.Writer, color bool, level Level) *Logger`, `(*Logger).Debug(msg string)`, `(*Logger).Level() Level`

- [ ] **Step 1: Write failing tests for Debug method and level filtering**

Replace `internal/log/log_test.go` with:

```go
package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestInfoWritesWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	l.Info("hello")
	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected no ANSI codes when color disabled, got %q", out)
	}
}

func TestWarnWritesWithYellow(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, true)
	l.Warn("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected output to contain 'danger', got %q", out)
	}
	if !strings.Contains(out, "\x1b[33m") {
		t.Errorf("expected yellow ANSI code, got %q", out)
	}
}

func TestErrorWritesWithRed(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, true)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("expected red ANSI code, got %q", out)
	}
}

func TestDebugHiddenAtNormalLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, false)
	l.Debug("secret")
	if buf.String() != "" {
		t.Errorf("expected no output at normal level, got %q", buf.String())
	}
}

func TestDebugShownAtVerboseLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelVerbose)
	l.Debug("secret")
	out := buf.String()
	if !strings.Contains(out, "secret") {
		t.Errorf("expected output to contain 'secret' at verbose level, got %q", out)
	}
}

func TestInfoHiddenAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Info("hello")
	if buf.String() != "" {
		t.Errorf("expected no info output at quiet level, got %q", buf.String())
	}
}

func TestWarnShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Warn("danger")
	out := buf.String()
	if !strings.Contains(out, "danger") {
		t.Errorf("expected warn at quiet level, got %q", out)
	}
}

func TestErrorShownAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	l.Error("boom")
	out := buf.String()
	if !strings.Contains(out, "boom") {
		t.Errorf("expected error at quiet level, got %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./internal/log/ -run 'TestDebug|TestInfo.*Quiet|TestWarn.*Quiet|TestError.*Quiet' -v`
Expected: FAIL — `NewWithLevel` and `Level` don't exist, `Debug` doesn't exist

- [ ] **Step 3: Implement Level type and Debug method in log.go**

Replace `internal/log/log.go` with:

```go
package log

import (
	"fmt"
	"io"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

type Level int

const (
	LevelNormal Level = iota
	LevelQuiet
	LevelVerbose
)

type Logger struct {
	w     ui.Writer
	color bool
	level Level
}

func New(w ui.Writer, color bool) *Logger {
	return &Logger{w: w, color: color, level: LevelNormal}
}

func NewWithLevel(w ui.Writer, color bool, level Level) *Logger {
	return &Logger{w: w, color: color, level: level}
}

func (l *Logger) Level() Level { return l.level }

func (l *Logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *Logger) Info(msg string) {
	if l.level >= LevelQuiet {
		return
	}
	l.write("", msg)
}

func (l *Logger) Warn(msg string) {
	if l.level >= LevelQuiet {
		return
	}
	l.write(ansiYellow, msg)
}

func (l *Logger) Error(msg string) { l.write(ansiRed, msg) }

func (l *Logger) Debug(msg string) {
	if l.level < LevelVerbose {
		return
	}
	l.write("", msg)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/log/ -v`
Expected: PASS

- [ ] **Step 5: Write failing test for spinner respecting quiet level**

Add to end of `internal/log/spinner_test.go`:

```go
func TestSpinnerHiddenAtQuietLevel(t *testing.T) {
	var buf bytes.Buffer
	l := NewWithLevel(&buf, false, LevelQuiet)
	s := NewSpinner(l)
	s.Start("Building image")
	s.Stop()
	if buf.String() != "" {
		t.Errorf("expected no spinner output at quiet level, got %q", buf.String())
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `CGO_ENABLED=1 go test ./internal/log/ -run TestSpinnerHiddenAtQuietLevel -v`
Expected: FAIL — spinner still writes at quiet level

- [ ] **Step 7: Implement spinner level awareness**

In `internal/log/spinner.go`, add `level Level` field to the `Spinner` struct (after `color bool`):

```go
type Spinner struct {
	w      ui.Writer
	color  bool
	level  Level
	msg    string
	start  time.Time
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	active bool
}
```

Update `NewSpinner` to pass the level:

```go
func NewSpinner(l *Logger) *Spinner {
	return &Spinner{w: l.w, color: l.color, level: l.level}
}
```

In `Start`, add a quiet-level early return after the `s.active` check:

```go
func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	if s.active || s.level >= LevelQuiet {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.msg = msg
	s.start = time.Now()
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	if s.color {
		s.done = make(chan struct{})
		go s.animate()
	} else {
		fmt.Fprintf(s.w, "%s... ", s.msg)
	}
}
```

In `finish`, add a quiet-level early return after the `elapsed` line:

```go
func (s *Spinner) finish(result string) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	elapsed := time.Since(s.start)
	s.mu.Unlock()

	if s.level >= LevelQuiet {
		return
	}

	suffix := formatElapsedDone(elapsed)
	var final string
	switch {
	case result == "done":
		final = "done" + suffix
	case strings.HasPrefix(result, "failed: "):
		final = fmt.Sprintf("failed %s: %s", suffix, strings.TrimPrefix(result, "failed: "))
	default:
		final = result + " " + suffix
	}

	if s.color {
		close(s.stopCh)
		<-s.done
		fmt.Fprintf(s.w, "\r\033[K%s %s\n", s.msg, final)
	} else {
		fmt.Fprintf(s.w, "%s\n", final)
	}
}
```

- [ ] **Step 8: Run all log tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/log/ -v`
Expected: PASS

- [ ] **Step 9: Run full test suite and lint**

Run: `CGO_ENABLED=1 go test ./... && golangci-lint run ./...`
Expected: PASS

- [ ] **Step 10: Format and commit**

```bash
gofmt -w .
git add internal/log/
git commit -m "feat(log): add log levels with Debug method and quiet filtering

Add Level type (Normal, Quiet, Verbose) to Logger. Debug messages
only appear at verbose level. Info and Warn are suppressed at quiet
level. Spinner output is hidden at quiet level."
```

---

### Task 2: Volume Fallback Removal

Remove the `--volume-fallback` flag, the `VolumeManager.fallback` field, and all fallback machinery. If msb volume creation fails, return an error immediately.

**Files:**
- Modify: `internal/sandbox/volumes.go`
- Modify: `internal/sandbox/volumes_test.go`
- Modify: `internal/sandbox/runner.go` (VolumeManager construction + EnsureHome call)
- Modify: `cmd/opencode-msb/cli.go` (remove flag definitions temporarily)

**Interfaces:**
- Produces: `NewVolumeManager(logger *log.Logger) *VolumeManager` — no `fallback` or `stateDir` params
- Produces: `(*VolumeManager).EnsureHome(ctx, projectSlug, imageDigest, imageTag string) (string, error)` — no `reset` param

- [ ] **Step 1: Update volumes_test.go — remove fallback tests, update constructor test**

Replace `internal/sandbox/volumes_test.go` with:

```go
package sandbox

import (
	"testing"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
)

func TestHomeVolumeName(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256-def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestHomeVolumeNameSanitizesColon(t *testing.T) {
	got := HomeVolumeName("p-abc123", "sha256:def456")
	expected := "p-abc123-opencode-home-sha256-def456"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestNewVolumeManager(t *testing.T) {
	l := log.New(nil, false)
	vm := NewVolumeManager(l)
	if vm.logger == nil {
		t.Error("expected logger to be set")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./internal/sandbox/ -run 'TestNewVolumeManager' -v`
Expected: FAIL — `NewVolumeManager` still takes 3 args

- [ ] **Step 3: Refactor volumes.go — remove fallback machinery**

Replace `internal/sandbox/volumes.go` with:

```go
package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func sanitizeDigest(digest string) string {
	return strings.ReplaceAll(digest, ":", "-")
}

func HomeVolumeName(projectSlug, imageDigest string) string {
	return projectSlug + "-opencode-home-" + sanitizeDigest(imageDigest)
}

type VolumeManager struct {
	logger *log.Logger
}

func NewVolumeManager(logger *log.Logger) *VolumeManager {
	return &VolumeManager{logger: logger}
}

func (vm *VolumeManager) EnsureHome(
	ctx context.Context,
	projectSlug, imageDigest, imageTag string,
) (string, error) {
	name := HomeVolumeName(projectSlug, imageDigest)

	_, err := msb.GetVolume(ctx, name)
	if err == nil {
		return name, nil
	}

	vol, err := msb.CreateVolume(ctx, name,
		msb.WithVolumeKind(msb.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume %s: %w", name, err)
	}

	if err := vm.prefillVolume(ctx, vol.Name(), imageTag); err != nil {
		return "", fmt.Errorf("prefill volume %s: %w", name, err)
	}

	return name, nil
}

func (vm *VolumeManager) prefillVolume(ctx context.Context, volumeName, imageTag string) error {
	prefillName := fmt.Sprintf("opencode-msb-prefill-%d", time.Now().UnixNano())

	mountConfig := msb.Mount.Named(volumeName, msb.MountOptions{})

	spin := log.NewSpinner(vm.logger)
	spin.Start("Preparing home volume")
	sb, err := msb.CreateSandbox(ctx, prefillName,
		msb.WithImage(imageTag),
		msb.WithMounts(map[string]msb.MountConfig{
			"/mnt/home": mountConfig,
		}),
		msb.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = msb.RemoveSandbox(context.Background(), prefillName)
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}
```

- [ ] **Step 4: Update runner.go — fix VolumeManager construction and EnsureHome call**

In `internal/sandbox/runner.go`, in the `Run` function, replace:

```go
	vm := NewVolumeManager(opts.VolumeFallback, cfg.StateDir, logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef, opts.ResetHome)
```

With:

```go
	vm := NewVolumeManager(logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef)
```

- [ ] **Step 5: Fix cli.go — remove flags referencing removed RunOptions fields**

In `cmd/opencode-msb/cli.go`, in `buildRunCmd`, remove these lines from the `RunE`:

```go
	opts.ImageRebuild, _ = cmd.Flags().GetBool("image-rebuild")
	opts.VolumeFallback, _ = cmd.Flags().GetBool("volume-fallback")
	opts.ResetHome, _ = cmd.Flags().GetBool("reset-home")
	opts.TestRun, _ = cmd.Flags().GetBool("test-run")
```

And remove these flag definitions:

```go
	cmd.Flags().Bool("image-rebuild", false, "Force image rebuild")
	cmd.Flags().Bool("volume-fallback", false, "Use host directories instead of msb volumes")
	cmd.Flags().Bool("reset-home", false, "Recreate the project home volume")
	cmd.Flags().Bool("test-run", false, "Validate setup without running opencode")
```

- [ ] **Step 6: Run full test suite and lint**

Run: `CGO_ENABLED=1 go test ./... && golangci-lint run ./...`
Expected: PASS

- [ ] **Step 7: Format and commit**

```bash
gofmt -w .
git add internal/sandbox/volumes.go internal/sandbox/volumes_test.go internal/sandbox/runner.go cmd/opencode-msb/cli.go
git commit -m "refactor(sandbox): remove volume fallback, fail early on msb errors

Remove VolumeManager.fallback field, ensureFallbackHome, prefillFallback,
and fallbackHomePath. If msb volume creation fails, return an error
immediately instead of silently falling back to host directories."
```

---

### Task 3: RunOptions Refactor + CLI Core Rewrite

Update `RunOptions` struct (remove `ImageRebuild`, `TestRun`; rename `ImageRebuild`→`Rebuild`; add `DryRun`). Extract shared `prepareSandbox` function from `Run`. Add `BuildImage` function. Rewrite cli.go with global persistent flags, implicit-run fix using cobra's command registry, `--tree` support, `--version`/`-V`, and the `run`/`doctor`/`build` commands.

**Files:**
- Modify: `internal/sandbox/runner.go` — RunOptions struct, extract `prepareSandbox`, update `Run`, add `BuildImage`
- Modify: `cmd/opencode-msb/cli.go` — full rewrite of CLI structure
- Create: `cmd/opencode-msb/cli_test.go` — tests for CLI helpers

**Interfaces:**
- Produces: `sandbox.RunOptions{Branch string, Rebuild bool, DryRun bool, CPUs uint8, Memory string, Auto bool, Args []string}`
- Produces: `sandbox.BuildImage(ctx context.Context, force bool, logger *log.Logger) error`
- Produces: `sandbox.Shell(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error` (stub, implemented in Task 5)
- Produces: `buildRootCmd() *cobra.Command` — testable root command construction
- Produces: `isKnownSubcommand(arg string, root *cobra.Command) bool` — uses cobra registry
- Produces: `printTree(w ui.Writer, cmd *cobra.Command, prefix string)` — tree printer for `--tree`

- [ ] **Step 1: Write failing tests for CLI helpers**

Create `cmd/opencode-msb/cli_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestIsKnownSubcommandRecognizesRegisteredCommands(t *testing.T) {
	root := buildRootCmd()
	tests := []struct {
		arg  string
		want bool
	}{
		{"run", true},
		{"doctor", true},
		{"build", true},
		{"help", true},
		{"--help", true},
		{"-h", true},
		{"--tree", true},
		{"--version", true},
		{"-V", true},
		{"unknown-cmd", false},
		{"--branch", false},
		{"-b", false},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			if got := isKnownSubcommand(tt.arg, root); got != tt.want {
				t.Errorf("isKnownSubcommand(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestPrintTreeContainsAllCommands(t *testing.T) {
	root := buildRootCmd()
	var sb strings.Builder
	printTree(&sb, root, "")
	out := sb.String()
	expected := []string{"run", "doctor", "build"}
	for _, cmd := range expected {
		if !strings.Contains(out, cmd) {
			t.Errorf("expected tree to contain %q, got:\n%s", cmd, out)
		}
	}
}

func TestRootHasGlobalFlags(t *testing.T) {
	root := buildRootCmd()
	flags := []string{"yes", "verbose", "quiet", "tree", "version"}
	for _, f := range flags {
		if root.PersistentFlags().Lookup(f) == nil {
			t.Errorf("expected persistent flag --%s on root", f)
		}
	}
}

func TestRunCommandHasExpectedFlags(t *testing.T) {
	root := buildRootCmd()
	runCmd, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	flags := []string{"branch", "cpus", "memory", "rebuild", "dry-run", "no-auto"}
	for _, f := range flags {
		if runCmd.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s on run command", f)
		}
	}
}

func TestRunCommandFlagShortcuts(t *testing.T) {
	root := buildRootCmd()
	runCmd, _ := root.Find([]string{"run"})
	if runCmd == nil {
		t.Fatal("expected run command")
	}
	shortcuts := map[string]string{
		"b": "branch", "c": "cpus", "m": "memory",
		"r": "rebuild", "n": "dry-run", "y": "yes",
		"v": "verbose", "q": "quiet",
	}
	for short, long := range shortcuts {
		f := runCmd.Flags().ShorthandLookup(short)
		if f == nil {
			f = root.PersistentFlags().ShorthandLookup(short)
		}
		if f == nil {
			t.Errorf("expected shorthand -%s for --%s", short, long)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./cmd/opencode-msb/ -v`
Expected: FAIL — `buildRootCmd`, `isKnownSubcommand`, `printTree` don't exist yet

- [ ] **Step 3: Update RunOptions struct and extract prepareSandbox in runner.go**

In `internal/sandbox/runner.go`, replace the `RunOptions` struct:

```go
type RunOptions struct {
	Branch  string
	Rebuild bool
	DryRun  bool
	CPUs    uint8
	Memory  string
	Auto    bool
	Args    []string
}
```

Add `sandboxSession` struct and `prepareSandbox` function before `Run`:

```go
type sandboxSession struct {
	sb        *msb.Sandbox
	name      string
	repoPath  string
	cwd       string
	cwdBranch string
	created   bool
	branch    string
}

func prepareSandbox(
	ctx context.Context,
	opts RunOptions,
	cfg Config,
	logger *log.Logger,
) (*sandboxSession, error) {
	if !CheckAll(ctx, logger) {
		return nil, errors.New("preflight failed")
	}

	projectSlug := git.ProjectSlug(logger)

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}

	repoPath, branch, cwdBranch, created, err := resolveWorkspace(cwd, opts, cfg, projectSlug, logger)
	if err != nil {
		return nil, err
	}

	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	imageRef, imageDigest, err := EnsureImage(ctx, dockerCli, dockerfile, opts.Rebuild, logger)
	if err != nil {
		return nil, fmt.Errorf("image setup failed: %w", err)
	}

	vm := NewVolumeManager(logger)
	homeVol, err := vm.EnsureHome(ctx, projectSlug, imageDigest, imageRef)
	if err != nil {
		return nil, fmt.Errorf("volume setup failed: %w", err)
	}

	configFiles, err := loadConfigFiles(cfg.UserConfigDir)
	if err != nil {
		return nil, err
	}
	secrets := BuildSecrets(logger)
	name := sandboxName(projectSlug, git.BranchSlug(branch))

	if err = ensureNoConflictingSession(ctx, name, projectSlug, branch, logger); err != nil {
		return nil, err
	}

	sb, err := createSandbox(ctx, name, imageRef, repoPath, homeVol, secrets, opts, logger)
	if err != nil {
		return nil, err
	}

	fs := sb.FS()
	if err := provisionSandbox(ctx, fs, configFiles, repoPath, logger); err != nil {
		return nil, err
	}

	return &sandboxSession{
		sb:        sb,
		name:      name,
		repoPath:  repoPath,
		cwd:       cwd,
		cwdBranch: cwdBranch,
		created:   created,
		branch:    branch,
	}, nil
}

func (s *sandboxSession) cleanup() {
	stopCtx, cancel := context.WithTimeout(context.Background(), sandboxStopTimeout)
	defer cancel()
	_ = s.sb.Stop(stopCtx)
	_ = s.sb.Close()
	_ = msb.RemoveSandbox(context.Background(), s.name)
}
```

Replace the `Run` function:

```go
func Run(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	var exitCode int
	var attachErr error
	if opts.DryRun {
		logger.Debug("dry run: setup validated, skipping opencode execution")
	} else {
		opencodeArgs := buildOpencodeArgs(opts.Args, opts.Auto)
		setup := `exec opencode ` + strings.Join(opencodeArgs, " ")
		exitCode, attachErr = session.sb.Attach(ctx, "/bin/bash", "-c", setup)
	}

	var cleanupErr error
	if session.created {
		cleanupErr = cleanupManagedRepo(session.repoPath, session.cwd, session.cwdBranch, opts, logger)
	}

	return finalizeRun(attachErr, cleanupErr, exitCode)
}
```

Add `BuildImage` function after `Run`:

```go
func BuildImage(ctx context.Context, force bool, logger *log.Logger) error {
	if !CheckDocker(logger) {
		return errors.New("docker not available")
	}
	dockerfile := resolveDockerfile()
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("cannot connect to Docker daemon (is dockerd running?): %w", err)
	}
	defer dockerCli.Close()

	_, _, err = EnsureImage(ctx, dockerCli, dockerfile, force, logger)
	return err
}
```

- [ ] **Step 4: Rewrite cli.go with new structure**

Replace `cmd/opencode-msb/cli.go` with:

```go
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"gitlab.inoio.de/inoio/opencode-msb/internal/log"
	"gitlab.inoio.de/inoio/opencode-msb/internal/prompt"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
)

var version = "dev"

func Execute() error {
	root := buildRootCmd()

	args := os.Args[1:]

	for _, a := range args {
		switch a {
		case "--tree":
			printTree(os.Stdout, root, "")
			return nil
		case "--version", "-V":
			fmt.Fprintf(os.Stdout, "opencode-msb version %s\n", version)
			return nil
		}
	}

	if len(args) == 0 || !isKnownSubcommand(args[0], root) {
		args = append([]string{"run"}, args...)
	}
	root.SetArgs(args)
	return root.Execute()
}

func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "opencode-msb",
		Short: "Run opencode inside an ephemeral microsandbox VM",
	}

	root.PersistentFlags().BoolP("yes", "y", false, "Assume yes to all prompts")
	root.PersistentFlags().BoolP("verbose", "v", false, "Show debug-level output")
	root.PersistentFlags().BoolP("quiet", "q", false, "Suppress non-error output")
	root.PersistentFlags().Bool("tree", false, "Print the full command tree and exit")
	root.PersistentFlags().BoolP("version", "V", false, "Print version and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if yes, _ := cmd.Flags().GetBool("yes"); yes {
			prompt.AssumeYes = true //nolint:reassign // CLI flag override, set once at startup
		}
		return nil
	}

	root.AddCommand(buildRunCmd())
	root.AddCommand(buildDoctorCmd())
	root.AddCommand(buildBuildCmd())

	return root
}

func isKnownSubcommand(arg string, root *cobra.Command) bool {
	switch arg {
	case "help", "--help", "-h", "--tree", "--version", "-V":
		return true
	default:
	}

	for _, cmd := range root.Commands() {
		if cmd.Name() == arg {
			return true
		}
		for _, alias := range cmd.Aliases {
			if alias == arg {
				return true
			}
		}
	}
	return false
}

func printTree(w ui.Writer, cmd *cobra.Command, prefix string) {
	subs := cmd.Commands()
	for i, sub := range subs {
		isLast := i == len(subs)-1
		var connector, childPrefix string
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		} else {
			connector = "├── "
			childPrefix = prefix + "│   "
		}
		fmt.Fprintf(w, "%s%s%s", prefix, connector, sub.Name())
		if len(sub.Aliases) > 0 {
			fmt.Fprintf(w, " (aliases: %s)", strings.Join(sub.Aliases, ", "))
		}
		fmt.Fprintln(w)
		printTree(w, sub, childPrefix)
	}
}

func buildDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check prerequisites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := newLogger(cmd)
			if !sandbox.CheckAll(cmd.Context(), logger) {
				return errors.New("preflight failed")
			}
			fmt.Fprintln(os.Stderr, "doctor: all checks passed")
			return nil
		},
	}
}

func buildBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build or rebuild the runner image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("rebuild")
			logger := newLogger(cmd)
			return sandbox.BuildImage(cmd.Context(), force, logger)
		},
	}
	cmd.Flags().BoolP("rebuild", "r", false, "Force a clean rebuild")
	return cmd
}

func buildRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] [ARGS...]",
		Short: "Run opencode in a microsandbox VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := sandbox.RunOptions{Args: args, Auto: true}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.DryRun, _ = cmd.Flags().GetBool("dry-run")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")
			if noAuto, _ := cmd.Flags().GetBool("no-auto"); noAuto {
				opts.Auto = false
			}

			cfg := newConfig()
			logger := newLogger(cmd)

			err := sandbox.Run(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().StringP("branch", "b", "", "Run in an isolated git clone for the given branch")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().BoolP("dry-run", "n", false, "Validate setup without running opencode")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit (default: 4G)")
	cmd.Flags().Bool("no-auto", false, "Do not pass --auto to opencode")

	return cmd
}

func newConfig() sandbox.Config {
	home, _ := os.UserHomeDir()
	return sandbox.Config{
		StateDir:      filepath.Join(home, ".local", "state", "opencode-msb"),
		UserConfigDir: filepath.Join(home, ".config", "opencode-msb", "opencode"),
	}
}

func newLogger(cmd *cobra.Command) *log.Logger {
	verbose, _ := cmd.Flags().GetBool("verbose")
	quiet, _ := cmd.Flags().GetBool("quiet")

	level := log.LevelNormal
	if quiet {
		level = log.LevelQuiet
	} else if verbose {
		level = log.LevelVerbose
	}

	return log.NewWithLevel(os.Stderr, term.IsTerminal(int(os.Stderr.Fd())), level)
}
```

- [ ] **Step 5: Run CLI tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./cmd/opencode-msb/ -v`
Expected: PASS

- [ ] **Step 6: Run full test suite and lint**

Run: `CGO_ENABLED=1 go test ./... && golangci-lint run ./...`
Expected: PASS

- [ ] **Step 7: Format and commit**

```bash
gofmt -w .
git add internal/sandbox/runner.go cmd/opencode-msb/cli.go cmd/opencode-msb/cli_test.go
git commit -m "refactor(cli): rewrite CLI with global flags, --tree, --version, new RunOptions

Update RunOptions: remove ImageRebuild/VolumeFallback/ResetHome/TestRun,
rename ImageRebuild to Rebuild, add DryRun. Extract prepareSandbox from
Run for reuse by shell command. Add BuildImage for standalone image
build. Rewrite cli.go with global persistent flags (--yes/-y,
--verbose/-v, --quiet/-q, --tree, --version/-V), implicit-run using
cobra registry, --tree printer, and build command with --rebuild/-r."
```

---

### Task 4: List + Shell + Config + Image/Volume List Commands

Add the remaining subcommands: `list` (with `sandbox list`/`ls` aliases), `shell` (with `sandbox shell` alias), `config show`, `image list`/`image ls`, `volume list`/`volume ls`. Add noun-group parent commands (`sandbox`, `image`, `volume`, `config`).

**Files:**
- Modify: `cmd/opencode-msb/cli.go` — add new commands, noun-group parents
- Modify: `cmd/opencode-msb/cli_test.go` — add tests for new commands
- Modify: `internal/sandbox/runner.go` — add `Shell` function, `ListSandboxes`, `ListImages`, `ListVolumes` functions
- Create: `internal/sandbox/query.go` — listing functions for sandboxes, images, volumes
- Create: `internal/sandbox/query_test.go` — tests for listing filter logic
- Modify: `internal/config/config.go` — add `DescribeConfig` function for `config show`

**Interfaces:**
- Produces: `sandbox.Shell(ctx, opts, cfg, logger) error`
- Produces: `sandbox.ListSandboxes(ctx) ([]SandboxInfo, error)`
- Produces: `sandbox.ListImages(ctx) ([]ImageInfo, error)`
- Produces: `sandbox.ListVolumes(ctx) ([]VolumeInfo, error)`
- Produces: `config.DescribeConfig(userDir, projectDir string, providerCfg map[string]any) ([]ConfigFileDesc, error)`

- [ ] **Step 1: Write failing tests for listing filter logic**

Create `internal/sandbox/query_test.go`:

```go
package sandbox

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestFilterSandboxesByPrefix(t *testing.T) {
	handles := []sandboxHandle{
		{name: "opencode-msb-proj-main"},
		{name: "opencode-msb-other-feat"},
		{name: "someone-elses-sandbox"},
		{name: "random"},
	}
	got := filterSandboxes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(got))
	}
	if got[0] != "opencode-msb-proj-main" {
		t.Errorf("expected first match, got %q", got[0])
	}
	if got[1] != "opencode-msb-other-feat" {
		t.Errorf("expected second match, got %q", got[1])
	}
}

func TestFilterVolumesByPrefix(t *testing.T) {
	handles := []volumeHandle{
		{name: "proj-opencode-home-sha256-abc"},
		{name: "other-opencode-home-sha256-def"},
		{name: "random-volume"},
	}
	got := filterVolumes(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(got))
	}
}

func TestFilterImagesByPrefix(t *testing.T) {
	handles := []imageHandle{
		{reference: "opencode-msb/runner:sha256-abc"},
		{reference: "opencode-msb/runner:base"},
		{reference: "python:3.12"},
	}
	got := filterImages(handles)
	if len(got) != 2 {
		t.Fatalf("expected 2 images, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=1 go test ./internal/sandbox/ -run 'TestFilter' -v`
Expected: FAIL — types and functions don't exist

- [ ] **Step 3: Implement listing functions in query.go**

Create `internal/sandbox/query.go`:

```go
package sandbox

import (
	"context"
	"fmt"
	"strings"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

type SandboxInfo struct {
	Name   string
	Status string
}

type VolumeInfo struct {
	Name string
	Path string
	Kind string
}

type ImageInfo struct {
	Reference string
	Digest    string
}

type sandboxHandle struct {
	name   string
	status msb.SandboxStatus
}

type volumeHandle struct {
	name string
	path string
	kind msb.VolumeKind
}

type imageHandle struct {
	reference   string
	manifestDigest string
}

func filterSandboxes(handles []sandboxHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.name, "opencode-msb-") {
			result = append(result, h.name)
		}
	}
	return result
}

func filterVolumes(handles []volumeHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.Contains(h.name, "-opencode-home-") {
			result = append(result, h.name)
		}
	}
	return result
}

func filterImages(handles []imageHandle) []string {
	var result []string
	for _, h := range handles {
		if strings.HasPrefix(h.reference, "opencode-msb/runner") {
			result = append(result, h.reference)
		}
	}
	return result
}

func ListSandboxes(ctx context.Context) ([]SandboxInfo, error) {
	handles, err := msb.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}
	var result []SandboxInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.HasPrefix(name, "opencode-msb-") {
			continue
		}
		result = append(result, SandboxInfo{
			Name:   name,
			Status: string(h.Status()),
		})
	}
	return result, nil
}

func ListVolumes(ctx context.Context) ([]VolumeInfo, error) {
	handles, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var result []VolumeInfo
	for _, h := range handles {
		name := h.Name()
		if !strings.Contains(name, "-opencode-home-") {
			continue
		}
		result = append(result, VolumeInfo{
			Name: name,
			Path: h.Path(),
			Kind: string(h.Kind()),
		})
	}
	return result, nil
}

func ListImages(ctx context.Context) ([]ImageInfo, error) {
	handles, err := msb.Image.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	var result []ImageInfo
	for _, h := range handles {
		ref := h.Reference()
		if !strings.HasPrefix(ref, "opencode-msb/runner") {
			continue
		}
		result = append(result, ImageInfo{
			Reference: ref,
			Digest:    h.ManifestDigest(),
		})
	}
	return result, nil
}
```

- [ ] **Step 4: Run filter tests to verify they pass**

Run: `CGO_ENABLED=1 go test ./internal/sandbox/ -run 'TestFilter' -v`
Expected: PASS

- [ ] **Step 5: Add Shell function to runner.go**

In `internal/sandbox/runner.go`, add after the `Run` function:

```go
func Shell(ctx context.Context, opts RunOptions, cfg Config, logger *log.Logger) error {
	session, err := prepareSandbox(ctx, opts, cfg, logger)
	if err != nil {
		return err
	}
	defer session.cleanup()

	exitCode, attachErr := session.sb.Attach(ctx, "/bin/bash")
	return finalizeRun(attachErr, nil, exitCode)
}
```

- [ ] **Step 6: Add config source tracking for config show**

In `internal/config/config.go`, add these types and function:

```go
type ConfigFileDesc struct {
	Name    string
	Sources []string
}

func DescribeConfig(userDir, projectDir string, providerConfig map[string]any) ([]ConfigFileDesc, error) {
	var result []ConfigFileDesc

	jsonFiles := scanJSONFiles(userDir, projectDir)
	for name := range jsonFiles {
		var sources []string
		if userDir != "" {
			if entries, err := os.ReadDir(userDir); err == nil {
				for _, e := range entries {
					if e.Name() == name {
						sources = append(sources, filepath.Join(userDir, name))
					}
				}
			}
		}
		if projectDir != "" {
			if entries, err := os.ReadDir(projectDir); err == nil {
				for _, e := range entries {
					if e.Name() == name {
						sources = append(sources, filepath.Join(projectDir, name))
					}
				}
			}
		}
		if name == "opencode.jsonc" || name == "opencode.json" {
			sources = append(sources, "embedded provider config")
		}
		result = append(result, ConfigFileDesc{Name: name, Sources: sources})
	}

	otherFiles := scanOtherFiles(userDir, projectDir)
	for name := range otherFiles {
		var sources []string
		if userDir != "" {
			if entries, err := os.ReadDir(userDir); err == nil {
				for _, e := range entries {
					if e.Name() == name {
						sources = append(sources, filepath.Join(userDir, name))
					}
				}
			}
		}
		if projectDir != "" {
			if entries, err := os.ReadDir(projectDir); err == nil {
				for _, e := range entries {
					if e.Name() == name {
						sources = append(sources, filepath.Join(projectDir, name))
					}
				}
			}
		}
		result = append(result, ConfigFileDesc{Name: name, Sources: sources})
	}

	return result, nil
}
```

Add a helper to print the merged content alongside the description. In the `config show` command's RunE, call both `DescribeConfig` and `BuildMergedConfig` to show sources and merged content.

- [ ] **Step 7: Add new commands to cli.go**

In `cmd/opencode-msb/cli.go`, add to `buildRootCmd` after the existing `AddCommand` calls:

```go
	root.AddCommand(buildListCmd())
	root.AddCommand(buildShellCmd())
	root.AddCommand(buildConfigCmd())
	root.AddCommand(buildImageCmd())
	root.AddCommand(buildVolumeCmd())
	root.AddCommand(buildSandboxCmd())
```

Add the command builder functions:

```go
func buildListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes for this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sandboxes, err := sandbox.ListSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			if len(sandboxes) == 0 {
				fmt.Fprintln(os.Stderr, "No sandboxes found.")
				return nil
			}
			for _, s := range sandboxes {
				fmt.Fprintf(os.Stdout, "%-40s %s\n", s.Name, s.Status)
			}
			return nil
		},
	}
	return cmd
}

func buildShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [flags]",
		Short: "Start sandbox and open a shell (debug)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := sandbox.RunOptions{Auto: false}
			opts.Branch, _ = cmd.Flags().GetString("branch")
			opts.Rebuild, _ = cmd.Flags().GetBool("rebuild")
			opts.CPUs, _ = cmd.Flags().GetUint8("cpus")
			opts.Memory, _ = cmd.Flags().GetString("memory")

			cfg := newConfig()
			logger := newLogger(cmd)

			err := sandbox.Shell(cmd.Context(), opts, cfg, logger)
			var exitErr *sandbox.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.Code)
			}
			return err
		},
	}

	cmd.Flags().StringP("branch", "b", "", "Run in an isolated git clone for the given branch")
	cmd.Flags().BoolP("rebuild", "r", false, "Rebuild the runner image before starting")
	cmd.Flags().Uint8P("cpus", "c", 0, "Number of CPUs (default: all)")
	cmd.Flags().StringP("memory", "m", "4G", "Memory limit (default: 4G)")

	return cmd
}

func buildConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect opencode configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print merged opencode config with source paths",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := newConfig()
			projectConfigDir := ""
			if _, statErr := os.Stat(".opencode-msb/opencode"); statErr == nil {
				projectConfigDir = ".opencode-msb/opencode"
			}
			providerCfg, err := config.LoadProviderConfig(config.EmbeddedProviderConfig)
			if err != nil {
				return fmt.Errorf("load provider config: %w", err)
			}

			descs, err := config.DescribeConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}
			files, err := config.BuildMergedConfig(cfg.UserConfigDir, projectConfigDir, providerCfg)
			if err != nil {
				return err
			}

			for _, desc := range descs {
				fmt.Fprintf(os.Stdout, "=== %s ===\n", desc.Name)
				for _, src := range desc.Sources {
					fmt.Fprintf(os.Stdout, "  source: %s\n", src)
				}
				if data, ok := files[desc.Name]; ok {
					fmt.Fprintln(os.Stdout, "  merged content:")
					for _, line := range strings.Split(string(data), "\n") {
						fmt.Fprintf(os.Stdout, "    %s\n", line)
					}
				}
				fmt.Fprintln(os.Stdout)
			}
			return nil
		},
	})
	return cmd
}

func buildImageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage runner images",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List cached runner images",
		RunE: func(cmd *cobra.Command, _ []string) error {
			images, err := sandbox.ListImages(cmd.Context())
			if err != nil {
				return err
			}
			if len(images) == 0 {
				fmt.Fprintln(os.Stderr, "No images found.")
				return nil
			}
			for _, img := range images {
				fmt.Fprintf(os.Stdout, "%-50s %s\n", img.Reference, img.Digest)
			}
			return nil
		},
	})
	cmd.AddCommand(buildBuildCmd())
	return cmd
}

func buildVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage volumes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List managed volumes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			volumes, err := sandbox.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			if len(volumes) == 0 {
				fmt.Fprintln(os.Stderr, "No volumes found.")
				return nil
			}
			for _, vol := range volumes {
				fmt.Fprintf(os.Stdout, "%-50s %s\n", vol.Name, vol.Path)
			}
			return nil
		},
	})
	return cmd
}

func buildSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage sandboxes",
	}
	cmd.AddCommand(buildListCmd())
	cmd.AddCommand(buildShellCmd())
	return cmd
}
```

Add `"gitlab.inoio.de/inoio/opencode-msb/internal/config"` to the imports of `cli.go`.

Note: `buildListCmd` and `buildShellCmd` are shared between the flat and `sandbox` parent. The `cobra.Command` struct is not reusable (it holds runtime state), so calling `buildListCmd()` twice creates two separate instances that share the same logic. This is the intended pattern.

- [ ] **Step 8: Update CLI tests for new commands**

In `cmd/opencode-msb/cli_test.go`, update `TestIsKnownSubcommandRecognizesRegisteredCommands` to add:

```go
		{"list", true},
		{"ls", true},
		{"shell", true},
		{"config", true},
		{"image", true},
		{"volume", true},
		{"sandbox", true},
```

Update `TestPrintTreeContainsAllCommands` `expected` slice to:

```go
	expected := []string{"run", "doctor", "build", "list", "shell", "config", "image", "volume"}
```

Add a new test for the `image build` noun form:

```go
func TestImageBuildNounFormExists(t *testing.T) {
	root := buildRootCmd()
	imageCmd, _ := root.Find([]string{"image"})
	if imageCmd == nil {
		t.Fatal("expected image command")
	}
	buildCmd, _ := imageCmd.Find([]string{"build"})
	if buildCmd == nil {
		t.Fatal("expected image build subcommand")
	}
}
```

- [ ] **Step 9: Run all tests and lint**

Run: `CGO_ENABLED=1 go test ./... && golangci-lint run ./...`
Expected: PASS

- [ ] **Step 10: Format and commit**

```bash
gofmt -w .
git add cmd/opencode-msb/cli.go cmd/opencode-msb/cli_test.go internal/sandbox/runner.go internal/sandbox/query.go internal/sandbox/query_test.go internal/config/config.go
git commit -m "feat(cli): add list, shell, config, image, volume commands

Add list (sandbox listing), shell (debug shell in sandbox), config show
(merged config with source paths), image list, volume list commands.
Add noun-group parents: sandbox, image, volume, config. Shell uses
shared prepareSandbox from Task 3. Config show uses new DescribeConfig
in config package."
```

---

### Task 5: README + BACKLOG Rework

Rewrite the README CLI documentation section to match the new command tree and flag structure. Update BACKLOG.md to mark the CLI refactor as done.

**Files:**
- Modify: `README.md` — full CLI documentation rework
- Modify: `BACKLOG.md` — mark CLI refactor item as done

**Interfaces:**
- No code interfaces — documentation only

- [ ] **Step 1: Rewrite README CLI documentation sections**

In `README.md`, replace the "## Usage", "## Branch sessions", and "## Flags" sections (lines 21-61) with:

```markdown
## Usage

```bash
opencode-msb                    # run opencode in a microsandbox VM
opencode-msb -b my-feature      # run in an isolated git clone
opencode-msb doctor             # check prerequisites
opencode-msb build -r           # rebuild the runner image
opencode-msb list               # list running sandboxes
```

## Commands

| Command | Aliases | Purpose |
|---|---|---|
| `run` (default) | `sandbox run` | Run opencode in the sandbox VM |
| `doctor` | — | Check host prerequisites (docker, kvm, git, msb) |
| `build` | `image build` | Build or rebuild the runner image |
| `list` | `ls`, `sandbox list` | List sandboxes for this host |
| `shell` | `sandbox shell` | Start sandbox and open a shell (debug) |
| `config show` | — | Print merged opencode config (debug) |
| `image list` | `image ls` | List cached runner images |
| `volume list` | `volume ls` | List managed volumes |

Bare `opencode-msb` (or flags-only invocation) implicitly runs `run`.

## Branch sessions

By default `opencode-msb` runs in the current directory. To start an isolated
session for a different branch, use `-b`/`--branch <branch>`:

```bash
opencode-msb -b my-feature
```

Rules:

- If the current checkout is already on `<branch>`, the current directory is used.
- Otherwise the launcher creates or reuses an independent git clone under
  `~/.local/state/opencode-msb/isolated-workspaces/<project>/<branch>`.
- If `<branch>` does not exist, you are prompted whether to create it. Use
  `--yes`/`-y` to create it from `HEAD` without prompting.
- When the launcher created the managed clone, it asks after the session whether to
  keep it, remove it, or merge it back into the original branch. With `--yes`,
  the default is to remove the managed clone and keep the branch.

## Flags

### Global

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--yes` | `-y` | `false` | Assume yes to all prompts |
| `--verbose` | `-v` | `false` | Show debug-level output |
| `--quiet` | `-q` | `false` | Suppress non-error output |
| `--tree` | — | `false` | Print the full command tree and exit |
| `--version` | `-V` | `false` | Print version and exit |

### Run / Shell

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--branch` | `-b` | `""` | Isolated git clone for the given branch |
| `--cpus` | `-c` | `0` (all) | vCPUs for the sandbox |
| `--memory` | `-m` | `4G` | Memory limit (e.g. `4G`, `512M`) |
| `--rebuild` | `-r` | `false` | Rebuild the runner image before starting |

### Run only

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--dry-run` | `-n` | `false` | Validate setup without running opencode |
| `--no-auto` | — | `false` | Do not pass `--auto` to opencode |

### Build

| Flag | Short | Default | Purpose |
|---|---|---|---|
| `--rebuild` | `-r` | `false` | Force a clean rebuild |
```

- [ ] **Step 2: Update BACKLOG.md**

In `BACKLOG.md`, change line 10 from:

```
[ ] refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?
```

To:

```
[*] refactor cli, subcommands for image rebuilding, ...? maybe remove some flags like --reset-home?
```

- [ ] **Step 3: Verify build still passes**

Run: `CGO_ENABLED=1 go build ./cmd/opencode-msb && gofmt -l .`
Expected: Build succeeds, no files need formatting

- [ ] **Step 4: Commit**

```bash
git add README.md BACKLOG.md
git commit -m "docs: rework CLI documentation in README, mark backlog item done

Rewrite Usage, Commands, Branch sessions, and Flags sections to match
the new command tree. Add commands table with aliases. Restructure flags
into Global, Run/Shell, Run-only, and Build subsections with shortcuts."
```
