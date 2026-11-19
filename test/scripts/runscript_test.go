package scripts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestRunScriptIsolatesAndCaptures pins the contract a harness driving a shell
// script depends on: the child runs with the host git config neutralized (so a
// fixture commit cannot reach a signing prompt), extraEnv is applied, and both
// output streams plus the exit status are captured without the test failing.
func TestRunScriptIsolatesAndCaptures(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	testutil.WritePath(
		t,
		script,
		"#!/usr/bin/env bash\necho \"glob=$GIT_CONFIG_GLOBAL\"\necho \"home=$HOME\"\necho err >&2\nexit 7\n",
	)

	got := RunScript(t, dir, map[string]string{"HOME": dir}, script)

	if got.Status != 7 {
		t.Errorf("Status = %d, want 7", got.Status)
	}
	if got.Stderr != "err\n" {
		t.Errorf("Stderr = %q, want %q", got.Stderr, "err\n")
	}
	if !strings.Contains(got.Stdout, "glob=/dev/null") {
		t.Errorf("Stdout = %q, want it to contain glob=/dev/null", got.Stdout)
	}
	if !strings.Contains(got.Stdout, "home="+dir) {
		t.Errorf("Stdout = %q, want it to contain home=%s", got.Stdout, dir)
	}
}

// TestRunScriptDisablesRaceExitSleep pins that the child disables the race
// runtime's exit delay while keeping the options the environment already sets.
// A fake command re-execs the race-instrumented test binary, whose clean exit
// otherwise sleeps `atexit_sleep_ms` (1000 ms), so the delay would dominate the
// package's -race run.
func TestRunScriptDisablesRaceExitSleep(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gorace.sh")
	testutil.WritePath(t, script, "#!/usr/bin/env bash\nprintf '%s\\n' \"$GORACE\"\n")

	got := RunScript(t, dir, map[string]string{"GORACE": "suppress_equal_stacks=1"}, script)

	want := "suppress_equal_stacks=1 atexit_sleep_ms=0\n"
	if got.Stdout != want {
		t.Errorf("GORACE = %q, want %q", got.Stdout, want)
	}
}

// TestRunScriptKeepsAnExplicitRaceExitSleep asserts RunScript does not clobber a
// delay the environment sets on purpose.
func TestRunScriptKeepsAnExplicitRaceExitSleep(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gorace.sh")
	testutil.WritePath(t, script, "#!/usr/bin/env bash\nprintf '%s\\n' \"$GORACE\"\n")

	got := RunScript(t, dir, map[string]string{"GORACE": "atexit_sleep_ms=500"}, script)

	if got.Stdout != "atexit_sleep_ms=500\n" {
		t.Errorf("GORACE = %q, want %q", got.Stdout, "atexit_sleep_ms=500\n")
	}
}

// TestRunScriptPassesArguments asserts the script receives its trailing args,
// the shape a harness uses to drive a tool with a case-specific argument.
func TestRunScriptPassesArguments(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "args.sh")
	testutil.WritePath(t, script, "#!/usr/bin/env bash\nprintf '%s|' \"$@\"\n")

	got := RunScript(t, dir, nil, script, "alpha", "beta")

	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	if got.Stdout != "alpha|beta|" {
		t.Errorf("Stdout = %q, want %q", got.Stdout, "alpha|beta|")
	}
}
