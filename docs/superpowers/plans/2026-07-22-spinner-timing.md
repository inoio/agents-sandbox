# Spinner Timing & `--timing` Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show per-step runtime on the spinner (`done(3.6s)`) and as a live elapsed counter while running, then remove the now-redundant `--timing` flag.

**Architecture:** The `Spinner` records a start time in `Start`, uses it to render elapsed whole seconds during animation, and appends a one-decimal-place duration to the result line on stop. The separate `log.Timing`/`NewTiming` subsystem and the `--timing` CLI flag are removed because the spinner makes the timing visible by default.

**Tech Stack:** Go 1.22+, standard library (`time`, `fmt`, `strings`).

## Global Constraints

- Target platforms: Linux (KVM) and macOS (Apple Silicon).
- No network rules for the MVP; default egress is allowed.
- Keep changes minimal; do not introduce new abstractions unless clearly needed.
- Follow existing Go style in the repo (`go fmt`, `go vet`, `golangci-lint`).

---

### Task 1: Add elapsed-time display to `Spinner`

**Files:**
- Modify: `internal/log/spinner.go`
- Modify: `internal/log/spinner_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Spinner` renders live elapsed seconds and appends timing to the done/failed line.

- [ ] **Step 1: Add failing formatting tests**

Add two pure tests for the unexported formatting helpers before they exist.

```go
func TestFormatElapsedLive(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "(0s)"},
		{1500 * time.Millisecond, "(1s)"},
		{59_900 * time.Millisecond, "(59s)"},
	}
	for _, c := range cases {
		got := formatElapsedLive(c.in)
		if got != c.want {
			t.Errorf("formatElapsedLive(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatElapsedDone(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "(0.0s)"},
		{3_640 * time.Millisecond, "(3.6s)"},
		{1_250 * time.Millisecond, "(1.3s)"},
	}
	for _, c := range cases {
		got := formatElapsedDone(c.in)
		if got != c.want {
			t.Errorf("formatElapsedDone(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Update existing spinner tests to allow timed suffixes**

Replace the exact-match assertions in `spinner_test.go` with substring checks.

```go
func TestSpinnerNonTerminalStop(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.Stop()

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "done(") {
		t.Errorf("expected timed done suffix, got %q", output)
	}
}

func TestSpinnerNonTerminalError(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(New(&buf, false))
	s.Start("Building image")
	s.StopError(fmt.Errorf("build failed"))

	output := buf.String()
	if !strings.Contains(output, "Building image... ") {
		t.Errorf("expected start message, got %q", output)
	}
	if !strings.Contains(output, "failed (") || !strings.Contains(output, ": build failed") {
		t.Errorf("expected timed failed suffix, got %q", output)
	}
}
```

Run: `go test ./internal/log -run TestSpinner -v`
Expected: compilation error (`formatElapsedLive` undefined) and possibly test failures on old exact assertions.

- [ ] **Step 3: Implement timing in `Spinner`**

Modify `internal/log/spinner.go`:

```go
package log

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var spinnerChars = []string{"�", "⠙", "⠹", "⠸", "�", "⠴", "⠦", "⠧", "�", "⠏"}

type Spinner struct {
	w      io.Writer
	color  bool
	msg    string
	start  time.Time
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	active bool
}

func NewSpinner(l *Logger) *Spinner {
	return &Spinner{w: l.w, color: l.color}
}

func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	if s.active {
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

func formatElapsedLive(elapsed time.Duration) string {
	return fmt.Sprintf("(%ds)", int(elapsed.Seconds()))
}

func formatElapsedDone(elapsed time.Duration) string {
	return fmt.Sprintf("(%.1fs)", elapsed.Seconds())
}

func (s *Spinner) animate() {
	defer close(s.done)
	i := 0
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		elapsed := time.Since(s.start)
		fmt.Fprintf(s.w, "\r\033[K%s %s %s", s.msg, spinnerChars[i%len(spinnerChars)], formatElapsedLive(elapsed))
		i++
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Spinner) finish(result string) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	elapsed := time.Since(s.start)
	s.mu.Unlock()

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

func (s *Spinner) Stop() {
	s.finish("done")
}

func (s *Spinner) StopError(err error) {
	s.finish(fmt.Sprintf("failed: %v", err))
}
```

- [ ] **Step 4: Run spinner tests**

Run: `go test ./internal/log -run TestSpinner -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/log/spinner.go internal/log/spinner_test.go
git commit -m "feat(log): show elapsed time on spinner done and live counter"
```

---

### Task 2: Remove the `log.Timing` subsystem

**Files:**
- Delete: `internal/log/timing.go`
- Delete: `internal/log/timing_test.go`
- Modify: `internal/log/log.go`
- Modify: `internal/log/log_test.go`

**Interfaces:**
- Consumes: decision that `--timing` output is replaced by spinner timing.
- Produces: no `Logger.Timing` method; no `NewTiming` helper.

- [ ] **Step 1: Delete timing source files**

```bash
rm internal/log/timing.go internal/log/timing_test.go
```

- [ ] **Step 2: Remove `Logger.Timing` and unused `time` import**

Edit `internal/log/log.go` to remove the `time` import and the `Timing` method. The file should end as:

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

type Logger struct {
	w     io.Writer
	color bool
}

func New(w io.Writer, color bool) *Logger {
	return &Logger{w: w, color: color}
}

func (l *Logger) write(color, msg string) {
	if l.color {
		fmt.Fprintf(l.w, "%s%s%s\n", color, msg, ansiReset)
	} else {
		fmt.Fprintln(l.w, msg)
	}
}

func (l *Logger) Info(msg string)  { l.write("", msg) }
func (l *Logger) Warn(msg string)  { l.write(ansiYellow, msg) }
func (l *Logger) Error(msg string) { l.write(ansiRed, msg) }
```

- [ ] **Step 3: Remove timing test**

Edit `internal/log/log_test.go` and delete `TestTimingFormatsDuration`. Keep the other three tests.

- [ ] **Step 4: Run log package tests**

Run: `go test ./internal/log -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/log/log.go internal/log/log_test.go
git commit -m "refactor(log): remove Logger.Timing and NewTiming helpers"
```

---

### Task 3: Remove the `--timing` CLI flag and runner wiring

**Files:**
- Modify: `cmd/opencode-msb/cli.go`
- Modify: `internal/sandbox/runner.go`

**Interfaces:**
- Consumes: `RunOptions` no longer has a `Timing` field.
- Produces: CLI has no `--timing` flag; `Run` no longer calls `log.NewTiming` or `tick`.

- [ ] **Step 1: Remove the flag from the CLI**

Edit `cmd/opencode-msb/cli.go`:
- Delete the line `opts.Timing, _ = cmd.Flags().GetBool("timing")`.
- Delete the line `cmd.Flags().Bool("timing", false, "Print per-phase launcher timing to stderr")`.

- [ ] **Step 2: Remove `Timing` from `RunOptions` and the runner timing calls**

Edit `internal/sandbox/runner.go`:
- Delete `Timing bool` from the `RunOptions` struct.
- Delete the block at the top of `Run`:

```go
	tick, summary := log.NewTiming(logger, opts.Timing)
	defer summary()
```

- Delete every `tick("...")` call in `Run`:
  - `tick("preflight")`
  - `tick("project/branch resolution")`
  - `tick("worktree resolution")`
  - `tick("image hash/check/build")`
  - `tick("volume ensure")`
  - `tick("config/secrets")`
  - `tick("config setup")`
  - `tick("opencode session")`

- [ ] **Step 3: Build and test**

Run: `go build ./...`
Expected: success.

Run: `go test ./...`
Expected: PASS (some tests may still be skipped for external dependencies; the log and CLI tests should pass).

- [ ] **Step 4: Commit**

```bash
git add cmd/opencode-msb/cli.go internal/sandbox/runner.go
git commit -m "feat(cli): remove --timing flag; spinner now shows step timing"
```

---

### Task 4: Update README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: `--timing` flag removed.
- Produces: README flags table no longer mentions `--timing`.

- [ ] **Step 1: Remove the `--timing` row from the flags table**

Delete this line from `README.md`:

```markdown
| `--timing` | `false` | print per-phase launcher timing to stderr |
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: remove --timing from README flags table"
```

---

### Task 5: Final verification

**Files:** all touched files.

- [ ] **Step 1: Run the standard checks**

```bash
go test ./...
go vet ./...
gofmt -l .
```

Expected:
- `go test` PASS.
- `go vet` clean.
- `gofmt -l .` returns no files (reformat with `gofmt -w .` if needed).

- [ ] **Step 2: Run linter if available**

```bash
golangci-lint run
```

Expected: no new issues.

- [ ] **Step 3: Final commit (only if fixes were needed)**

```bash
git add -A
git commit -m "style: gofmt and lint fixes"
```

---

## Self-Review

**Spec coverage:** The user asked for (1) runtime after the done string with tenth-second precision, (2) live elapsed seconds on the spinner, and (3) removal of `--timing`. Task 1 covers (1) and (2); Tasks 2–4 cover (3).

**Placeholder scan:** No placeholders; all steps include exact file paths and code.

**Type consistency:** `RunOptions.Timing` is removed consistently from `cli.go` and `runner.go`; no remaining references to `NewTiming` or `Logger.Timing` are expected.
