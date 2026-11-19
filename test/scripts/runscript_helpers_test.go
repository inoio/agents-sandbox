package scripts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// ScriptResult is the captured outcome of a shell script run through RunScript.
type ScriptResult struct {
	Stdout string
	Stderr string
	Status int
}

// RunScript runs `bash <script> <args...>` in dir and returns its captured
// output and exit status. The child environment roots at
// GitIsolationEnvironment(os.Environ()) with extraEnv overlaid, so a script that
// drives git cannot read or prompt through the developer's configuration even
// if the script itself forgets to isolate. It also disables the race runtime's
// exit delay for the child (see withRaceExitSleepDisabled). A failure to start
// bash at all is fatal; a non-zero exit status is returned in ScriptResult, not
// raised.
func RunScript(tb testing.TB, dir string, extraEnv map[string]string, script string, args ...string) ScriptResult {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	// Read the script so cmd/go records it as a test-cache input: the child bash
	// opens it, which the test process cannot see, so without this a changed
	// script would be replayed from a stale cached test result.
	if _, err := os.ReadFile(script); err != nil {
		tb.Fatalf("read script %s: %v", script, err)
	}
	cmd := exec.CommandContext(context.Background(), "bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	// RunScript roots the child in the caller's environment, then strips every
	// ambient git setting, so reading os.Environ here is the point of the helper.
	cmd.Env = withRaceExitSleepDisabled(
		overlayEnvironment(testutil.GitIsolationEnvironment(os.Environ()), extraEnv),
	) //testenv:allow the runner strips the ambient git settings from the caller's environment
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := ScriptResult{Stdout: stdout.String(), Stderr: stderr.String(), Status: 0}
	// gomutants:disable-next-line BRANCH_IF reason="a start failure must stay fatal"
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		result.Status = exitErr.ExitCode()
		return result
	}
	if err != nil {
		tb.Fatalf("run %s: %v", script, err)
	}
	return result
}

// withRaceExitSleepDisabled ensures environ's GORACE disables the race runtime's
// exit delay (`atexit_sleep_ms`, 1000 ms by default). A fake command is a
// re-exec of the race-instrumented test binary, and every clean exit of one
// sleeps that long, so without this the migrated harnesses pay a second per fake
// invocation — minutes across a package under `-race`. Only the exit delay is
// dropped; race detection itself stays on. Other GORACE options are preserved,
// and an explicit atexit_sleep_ms is left alone.
func withRaceExitSleepDisabled(environ []string) []string {
	const (
		gorace      = "GORACE"
		atexitSleep = "atexit_sleep_ms"
	)
	for i, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name != gorace {
			continue
		}
		if strings.Contains(value, atexitSleep) {
			return environ
		}
		environ[i] = entry + " " + atexitSleep + "=0"
		return environ
	}
	return append(environ, gorace+"="+atexitSleep+"=0")
}

// overlayEnvironment returns base with overlay applied: an entry whose key
// appears in overlay is dropped first, so the child sees exactly one entry per
// key rather than base's value shadowed by a duplicate.
func overlayEnvironment(base []string, overlay map[string]string) []string {
	out := make([]string, 0, len(base)+len(overlay))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if _, overridden := overlay[name]; overridden {
			continue
		}
		out = append(out, entry)
	}
	for name, value := range overlay {
		out = append(out, name+"="+value)
	}
	return out
}
