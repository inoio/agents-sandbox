package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// checkStubSource is the fixture `make` scripts/check.sh invokes: it records the
// target's start/end order markers when STUB_ORDER is set (sleeping so an
// overlapping pool is observable), prints a stdout and a stderr marker, and
// exits non-zero when the control file $STUB_DIR/fail-<target> exists. Anything
// before the target name is a flag; the last argument is the target.
const checkStubSource = `#!/usr/bin/env bash
set -euo pipefail
target=""
for arg in "$@"; do target="$arg"; done
if [[ "${STUB_ORDER:-0}" == 1 ]]; then
  printf 'start %s\n' "$target" >>"$STUB_DIR/order"
  sleep 0.3
fi
printf 'stdout %s\n' "$target"
printf 'stderr %s\n' "$target" >&2
status=0
if [[ -f "$STUB_DIR/fail-$target" ]]; then status=1; fi
if [[ "${STUB_ORDER:-0}" == 1 ]]; then
  printf 'end %s\n' "$target" >>"$STUB_DIR/order"
fi
exit "$status"
`

// checkHarness drives scripts/check.sh with a stub `make` on PATH: the stub
// records each target's output and, when asked, its start/end ordering.
type checkHarness struct {
	t         *testing.T
	dir       string
	stub      string
	orderPath string
}

// newCheckHarness writes the stub make into a fresh temp dir.
func newCheckHarness(t *testing.T) *checkHarness {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "make")
	if err := os.WriteFile(stub, []byte(checkStubSource), 0o755); err != nil {
		t.Fatalf("write stub make: %v", err)
	}
	return &checkHarness{t: t, dir: dir, stub: stub, orderPath: filepath.Join(dir, "order")}
}

// checkRun controls one check.sh invocation. heavy is the space-separated
// CHECK_HEAVY_TARGETS; jobs, serial, verbose, progress and order overlay the
// runner's environment, each defaulting to the bash harness's default.
type checkRun struct {
	heavy    string
	jobs     string
	serial   string
	verbose  string
	progress string
	order    string
}

// checkRun drives check.sh with the targets and environment, returning its
// captured result.
func (h *checkHarness) run(run checkRun, targets ...string) ScriptResult {
	h.t.Helper()
	env := map[string]string{
		"MAKE":                h.stub,
		"CHECK_HEAVY_TARGETS": run.heavy,
		"STUB_DIR":            h.dir,
		"JOBS":                checkDefaultString(run.jobs, "2"),
		"SERIAL":              checkDefaultString(run.serial, "0"),
		"VERBOSE":             checkDefaultString(run.verbose, "0"),
		"CHECK_PROGRESS":      run.progress,
		"STUB_ORDER":          checkDefaultString(run.order, "0"),
	}
	return RunScript(h.t, h.dir, env, repositoryScript(h.t, "check.sh"), targets...)
}

// checkDefaultString returns value, or fallback when value is empty.
func checkDefaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// failTarget drops the stub make's failure control file for a target, so the
// next run's target exits non-zero.
func (h *checkHarness) failTarget(target string) {
	h.t.Helper()
	h.clearOrder()
	if err := os.WriteFile(filepath.Join(h.dir, "fail-"+target), nil, 0o600); err != nil {
		h.t.Fatalf("touch fail-%s: %v", target, err)
	}
}

// clearFailures removes every failure control file.
func (h *checkHarness) clearFailures() {
	h.t.Helper()
	matches, err := filepath.Glob(filepath.Join(h.dir, "fail-*"))
	if err != nil {
		h.t.Fatalf("glob fail files: %v", err)
	}
	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			h.t.Fatalf("remove %s: %v", path, err)
		}
	}
}

// clearOrder truncates the stub's start/end marker log.
func (h *checkHarness) clearOrder() {
	h.t.Helper()
	if err := os.WriteFile(h.orderPath, nil, 0o600); err != nil {
		h.t.Fatalf("clear order log: %v", err)
	}
}

// startsBeforeFirstEnd counts `start` markers seen before the first `end` in the
// order log: a width of 1 means the targets ran one at a time.
func (h *checkHarness) startsBeforeFirstEnd() int {
	starts := 0
	for line := range strings.SplitSeq(readText(h.t, h.orderPath), "\n") {
		switch {
		case strings.HasPrefix(line, "end "):
			return starts
		case strings.HasPrefix(line, "start "):
			starts++
		}
	}
	return starts
}

// checkLineOf returns the 1-based line number of the first line containing
// needle, or 0 when absent.
func checkLineOf(haystack, needle string) int {
	for i, line := range strings.Split(haystack, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// TestCheck ports every case of the bash harness scripts/test-check.sh,
// asserting the same exit codes, summary lines, failure-log ordering, and
// JOBS/SERIAL/VERBOSE scheduling behavior. The harness never runs git, so the
// ambient-config poison run is not meaningful: it exercises nothing a poisoned
// git config could reach.
func TestCheck(t *testing.T) {
	t.Run("all pass", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha"}, "alpha", "beta")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "PASS alpha")
		assertContains(t, "stdout", result.Stdout, "PASS beta")
		assertNotContains(t, "stdout", result.Stdout, "stdout alpha")
		assertNotContains(t, "stdout", result.Stdout, "--- FAIL")
	})

	t.Run("failing light target", func(t *testing.T) {
		h := newCheckHarness(t)
		h.failTarget("beta")
		result := h.run(checkRun{heavy: "alpha"}, "alpha", "beta")

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "FAIL beta")
		assertContains(t, "stdout", result.Stdout, "--- FAIL beta ---")
		assertContains(t, "stdout", result.Stdout, "stderr beta")
		betaBanner := checkLineOf(result.Stdout, "--- FAIL beta ---")
		alphaPass := checkLineOf(result.Stdout, "PASS alpha")
		if betaBanner == 0 || alphaPass == 0 || alphaPass >= betaBanner {
			t.Errorf(
				"failure log follows the PASS lines: pass=%d banner=%d\nstdout:\n%s",
				alphaPass,
				betaBanner,
				result.Stdout,
			)
		}
	})

	t.Run("failing heavy target", func(t *testing.T) {
		h := newCheckHarness(t)
		h.failTarget("alpha")
		result := h.run(checkRun{heavy: "alpha"}, "alpha", "beta")

		assertStatus(t, result, 1)
		assertContains(t, "stdout", result.Stdout, "FAIL alpha")
		assertContains(t, "stdout", result.Stdout, "--- FAIL alpha ---")
	})

	t.Run("unknown heavy target", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "gamma"}, "alpha", "beta")

		assertStatus(t, result, 2)
		assertContains(t, "stderr", result.Stderr, "gamma")
	})

	t.Run("help", func(t *testing.T) {
		h := newCheckHarness(t)
		result := RunScript(t, h.dir, nil, repositoryScript(t, "check.sh"), "--help")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "Usage")
	})

	t.Run("light targets overlap", func(t *testing.T) {
		h := newCheckHarness(t)
		h.clearFailures()
		h.clearOrder()
		result := h.run(checkRun{heavy: "", jobs: "2", serial: "0", verbose: "0", order: "1"}, "alpha", "beta")

		assertStatus(t, result, 0)
		if got := h.startsBeforeFirstEnd(); got != 2 {
			t.Errorf("light targets overlap: starts before first end = %d, want 2", got)
		}
	})

	t.Run("heavy targets run one at a time", func(t *testing.T) {
		h := newCheckHarness(t)
		h.clearFailures()
		h.clearOrder()
		result := h.run(
			checkRun{heavy: "alpha beta", jobs: "2", serial: "0", verbose: "0", order: "1"},
			"alpha",
			"beta",
		)

		assertStatus(t, result, 0)
		if got := h.startsBeforeFirstEnd(); got != 1 {
			t.Errorf("heavy targets run one at a time: starts before first end = %d, want 1", got)
		}
	})

	t.Run("SERIAL runs one at a time", func(t *testing.T) {
		h := newCheckHarness(t)
		h.clearFailures()
		h.clearOrder()
		result := h.run(checkRun{heavy: "", jobs: "2", serial: "1", verbose: "0", order: "1"}, "alpha", "beta")

		assertStatus(t, result, 0)
		if got := h.startsBeforeFirstEnd(); got != 1 {
			t.Errorf("SERIAL runs one at a time: starts before first end = %d, want 1", got)
		}
	})

	t.Run("VERBOSE streams without status lines", func(t *testing.T) {
		h := newCheckHarness(t)
		h.clearFailures()
		h.clearOrder()
		result := h.run(checkRun{heavy: "", jobs: "2", serial: "0", verbose: "1", order: "1"}, "alpha")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "stdout alpha")
		assertNotContains(t, "stdout", result.Stdout, "PASS ")
	})

	t.Run("per-target line reports cpu time and lane", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha"}, "alpha", "beta")

		assertStatus(t, result, 0)
		heavy := regexp.MustCompile(`(?m)^PASS alpha \d+s \(cpu \d+s\) \[heavy\]$`)
		light := regexp.MustCompile(`(?m)^PASS beta \d+s \(cpu \d+s\) \[light\]$`)
		if !heavy.MatchString(result.Stdout) || !light.MatchString(result.Stdout) {
			t.Errorf("PASS lines lack a cpu time or lane:\n%s", result.Stdout)
		}
	})

	t.Run("total time line reports the target count", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha"}, "alpha", "beta")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "check: 2 targets in ")
	})

	t.Run("total time line is singular for one target", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha"}, "alpha")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "check: 1 target in ")
	})

	t.Run("progress is off without a terminal", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha", jobs: "2", order: "1"}, "alpha", "beta")

		assertStatus(t, result, 0)
		assertNotContains(t, "stdout", result.Stdout, "\x1b[")
		assertNotContains(t, "stdout", result.Stdout, "heavy alpha")
		assertNotContains(t, "stdout", result.Stdout, "light beta")
	})

	t.Run("progress separates heavy and light targets", func(t *testing.T) {
		h := newCheckHarness(t)
		result := h.run(checkRun{heavy: "alpha", jobs: "2", order: "1", progress: "1"}, "alpha", "beta")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "\x1b[")
		assertContains(t, "stdout", result.Stdout, "heavy alpha")
		assertContains(t, "stdout", result.Stdout, "light beta")
		assertContains(t, "stdout", result.Stdout, "check: 2 targets in ")
	})

	t.Run("check-selected filters the heavy set", func(t *testing.T) {
		h := newCheckHarness(t)
		got := h.runSelected(checkRunSelectedCase{
			targets: "docs-linkcheck shellcheck",
			mode:    "branch",
			base:    "origin/main",
			heavy:   "validate-fmt lint test compile-all",
		})
		want := "heavy=[] mode=[branch] base=[origin/main] args=[docs-linkcheck shellcheck changelog-presence]"
		if got != want {
			t.Errorf("check-selected wiring = %q, want %q", got, want)
		}
	})

	t.Run("check-selected keeps a selected heavy target", func(t *testing.T) {
		h := newCheckHarness(t)
		got := h.runSelected(checkRunSelectedCase{
			targets: "validate-fmt docs-linkcheck",
			mode:    "local",
			base:    "",
			heavy:   "validate-fmt lint test compile-all",
		})
		want := "heavy=[validate-fmt] mode=[local] base=[] args=[validate-fmt docs-linkcheck changelog-presence]"
		if got != want {
			t.Errorf("check-selected keeps a selected heavy target = %q, want %q", got, want)
		}
	})
}

// checkRunSelectedCase is one make check-selected invocation's inputs.
type checkRunSelectedCase struct {
	targets string
	mode    string
	base    string
	heavy   string
}

// runSelected runs `make check-selected` with a stub CHECK_RUNNER, so the
// Makefile's heavy-set filtering and MODE/BASE forwarding are exercised without
// running a real target, and returns the stub runner's recorded line.
func (h *checkHarness) runSelected(c checkRunSelectedCase) string {
	h.t.Helper()
	record := filepath.Join(h.dir, "selected")
	if err := os.WriteFile(record, nil, 0o600); err != nil {
		h.t.Fatalf("clear selected record: %v", err)
	}
	runner := h.writeSelectedRunner(record)

	args := []string{
		"-s", "-C", repositoryRoot(h.t), "check-selected",
		"TARGETS=" + c.targets,
		"MODE=" + c.mode,
		"CHECK_RUNNER=" + runner,
		"CHECK_HEAVY_TARGETS=" + c.heavy,
	}
	if c.base != "" {
		args = append(args, "BASE="+c.base)
	}
	if out, err := checkRunMake(h.t, args...); err != nil {
		h.t.Fatalf("make check-selected: %v\n%s", err, out)
	}
	return strings.TrimRight(readText(h.t, record), "\n")
}

// writeSelectedRunner writes the stub CHECK_RUNNER that records the environment
// make's check-selected target composed, returning its path.
func (h *checkHarness) writeSelectedRunner(record string) string {
	h.t.Helper()
	source := "#!/usr/bin/env bash\n" +
		"set -euo pipefail\n" +
		"printf 'heavy=[%s] mode=[%s] base=[%s] args=[%s]\\n' " +
		"\"${CHECK_HEAVY_TARGETS:-}\" \"${MODE:-}\" \"${BASE:-}\" \"$*\" >\"" + record + "\"\n" +
		"exit 0\n"
	runner := filepath.Join(h.dir, "selected-runner")
	if err := os.WriteFile(runner, []byte(source), 0o755); err != nil {
		h.t.Fatalf("write selected runner: %v", err)
	}
	return runner
}

// runMake runs make with args, clearing MAKEFLAGS the way the runner does so a
// parent make cannot leak a jobserver into the read, and returns its combined
// output.
func checkRunMake(tb testing.TB, args ...string) ([]byte, error) {
	tb.Helper()
	// The helper forwards the parent environment to make, minus MAKEFLAGS.
	environ := os.Environ() //testenv:allow make needs the caller's PATH and toolchain selection
	env := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "MAKEFLAGS=") {
			env = append(env, entry)
		}
	}
	env = append(env, "MAKEFLAGS=")
	command := exec.Command("make", args...)
	command.Env = env
	return command.CombinedOutput()
}
