//deadcode:ignore reason="test support: referenced only by _test.go files"
package testutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
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
// if the script itself forgets to isolate. A failure to start bash at all is
// fatal; a non-zero exit status is returned in ScriptResult, not raised.
func RunScript(tb testing.TB, dir string, extraEnv map[string]string, script string, args ...string) ScriptResult {
	tb.Helper() // gomutants:disable STATEMENT_REMOVE reason="t.Helper only adjusts reported stack frames"
	//nolint:gosec // the script path is a trusted test fixture, never user input
	cmd := exec.CommandContext(context.Background(), "bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = overlayEnvironment(GitIsolationEnvironment(os.Environ()), extraEnv)
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
