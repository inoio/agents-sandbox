package testutil

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRunScriptIsolatesAndCaptures pins the contract a harness driving a shell
// script depends on: the child runs with the host git config neutralized (so a
// fixture commit cannot reach a signing prompt), extraEnv is applied, and both
// output streams plus the exit status are captured without the test failing.
func TestRunScriptIsolatesAndCaptures(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	WritePath(t, script, "#!/usr/bin/env bash\necho \"glob=$GIT_CONFIG_GLOBAL\"\necho \"home=$HOME\"\necho err >&2\nexit 7\n")

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

// TestRunScriptPassesArguments asserts the script receives its trailing args,
// the shape a harness uses to drive a tool with a case-specific argument.
func TestRunScriptPassesArguments(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "args.sh")
	WritePath(t, script, "#!/usr/bin/env bash\nprintf '%s|' \"$@\"\n")

	got := RunScript(t, dir, nil, script, "alpha", "beta")

	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	if got.Stdout != "alpha|beta|" {
		t.Errorf("Stdout = %q, want %q", got.Stdout, "alpha|beta|")
	}
}
