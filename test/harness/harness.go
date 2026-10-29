// Package harness holds end-to-end smoke tests that start every registered
// coding-agent harness in a real microsandbox VM and assert it reaches its
// full-screen TUI. The VM-driven test lives behind the `msb` build tag; the
// untagged files here are inert and shared by it: the harness table, the child
// environment allowlist, and the output helpers.
//
//deadcode:ignore reason="test support: referenced only by _test.go files"
package harness

import (
	"bytes"
	"path/filepath"
	"strings"
)

// daemonTimeoutMarker is what the launcher prints when its daemon health probe
// never succeeds. A harness whose output contains it never reached the TUI.
const daemonTimeoutMarker = "did not become healthy"

// altScreenEnter is the terminal control sequence a harness emits as its
// full-screen TUI takes over the alternate screen buffer.
const altScreenEnter = "\x1b[?1049h"

// escapeByte starts an ANSI escape sequence.
const escapeByte = 0x1b

const (
	// envTerm is the terminal type variable a full-screen TUI reads.
	envTerm = "TERM"
	// defaultTerm is the terminfo entry a harness runs under when the test
	// process has no usable one.
	defaultTerm = "xterm-256color"
	// termDumb is the terminfo entry that suppresses terminal control
	// sequences; a harness cannot render its TUI under it.
	termDumb = "dumb"
)

const (
	// smokeRootBase and smokeRootPrefix place the suite's shared temp root at a
	// short, fixed path instead of under the host's TMPDIR.
	//
	// microsandbox derives its sandbox control-socket path from the private home
	// under XDG_DATA_HOME (<root>/data/agents-sandbox/msb) and rejects a derived
	// path once it approaches a Unix socket's sun_path limit (104 bytes on
	// macOS, the tighter of the two supported platforms). A long TMPDIR —
	// macOS's default is already ~49 bytes — would push the derived path over
	// the limit and fail every harness before its TUI with "sandbox runtime
	// socket path is too long", so the root stays short.
	smokeRootBase   = "/tmp"
	smokeRootPrefix = "asb-"

	// dataDirName is the XDG data directory isolatedEnv puts under the root; the
	// private microsandbox home is nested inside it.
	dataDirName = "data"
)

// isolatedEnvKeep lists the inherited variables the child may keep: the
// terminal and locale settings a TUI needs, the proxy settings any egress
// uses, and the docker endpoint variables the runner-image build needs. The
// docker endpoint variables carry a host endpoint, not agents-sandbox config or
// state, so keeping them does not weaken the isolation; dropping HOME, XDG_*,
// AGENTS_SANDBOX_*, OPENCODE_SANDBOX_*, and MSB_* is the point of it.
var isolatedEnvKeep = []string{
	"PATH", "TERM", "LANG", "LC_ALL", "LC_CTYPE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
}

// publicNetwork is the `--network` profile a harness that needs egress to
// render runs under.
const publicNetwork = "public"

// Terminal keys that leave a harness's full-screen TUI: Ctrl-D is `opencode`'s,
// `opencode2`'s, and `pi`'s documented `app_exit`/`app.exit`, and Ctrl-C is the
// interrupt `claude-code` announces as its exit ("Press Ctrl-C again to exit")
// and `codex` quits on.
const (
	ctrlC = 0x03
	ctrlD = 0x04
)

// harnessSpecs maps every registered harness to how the smoke test drives it.
// It must stay in sync with agent.Names(); the smoke test asserts the two key
// sets are equal.
var harnessSpecs = map[string]harnessSpec{
	// claude-code checks in with its API before it draws anything, so it needs
	// egress, and its renderer keeps the terminal's normal buffer, so the
	// alternate screen never announces that its UI is up.
	"claude-code": {QuitKey: []byte{ctrlC}, Network: publicNetwork, KeepsNormalBuffer: true},
	// codex quits on Ctrl-C and may reach its API at startup, so it gets
	// egress like claude-code; its TUI uses the alternate screen.
	"codex": {QuitKey: []byte{ctrlC}, Network: publicNetwork, KeepsNormalBuffer: false},
	// hermes greets with a banner and its own model/provider state before the
	// prompt, so it needs egress like claude-code; its classic CLI keeps the
	// terminal's normal buffer, so the alternate screen never announces that its
	// UI is up; Ctrl-D leaves its prompt.
	"hermes": {QuitKey: []byte{ctrlD}, Network: publicNetwork, KeepsNormalBuffer: true},
	// opencode's editor owns Ctrl-C, so its TUI leaves on Ctrl-D.
	"opencode":  {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
	"opencode2": {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
	"pi":        {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
}

// harnessSpec describes how to drive one harness to its TUI and out of it.
type harnessSpec struct {
	// QuitKey leaves the harness's full-screen TUI. The smoke test taps it
	// until the process goes, because a harness can paint its TUI seconds
	// before it accepts input and can need the key more than once.
	QuitKey []byte
	// Network overrides the default `--network none` when a harness needs
	// egress to render.
	Network string
	// KeepsNormalBuffer declares a harness whose TUI renders into the
	// terminal's normal buffer instead of the alternate screen, so the
	// alternate-screen marker cannot announce that its UI is up.
	KeepsNormalBuffer bool
}

// isolatedEnv builds the child environment from inherited: it keeps only the
// allowlisted variables (defaulting TERM when it is absent, empty, or "dumb"),
// then appends a private HOME/XDG_*/TMPDIR tree rooted at root, so the child
// reads and writes no real agents-sandbox config or state.
func isolatedEnv(root string, inherited []string) []string {
	inheritedByName := make(map[string]string, len(inherited))
	for _, entry := range inherited {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			inheritedByName[name] = value
		}
	}

	env := make([]string, 0, len(isolatedEnvKeep)+7)
	for _, name := range isolatedEnvKeep {
		if name == envTerm {
			continue
		}
		if value, ok := inheritedByName[name]; ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, envTerm+"="+usableTerm(inheritedByName[envTerm]))
	return append(env,
		"HOME="+filepath.Join(root, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, dataDirName),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"TMPDIR="+filepath.Join(root, "tmp"),
	)
}

// usableTerm returns the TERM to run the child under: the inherited value
// unless it is absent, empty, or "dumb", in which case the harness is given a
// modern terminfo entry so its full-screen TUI can render.
func usableTerm(value string) string {
	if value == "" || value == termDumb {
		return defaultTerm
	}
	return value
}

// stripANSI removes CSI escape sequences (ESC '[' ... final byte) from b so
// captured pty output can be searched as plain text.
func stripANSI(b []byte) string {
	stripped := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == escapeByte && i+1 < len(b) && b[i+1] == '[' {
			i = ansiSequenceEnd(b, i)
			continue
		}
		stripped = append(stripped, b[i])
	}
	return string(stripped)
}

// ansiSequenceEnd returns the index of the final byte of the CSI sequence
// starting at b[start] (ESC '['), or len(b)-1 when the sequence is truncated.
func ansiSequenceEnd(b []byte, start int) int {
	for i := start + 2; i < len(b); i++ {
		if isANSIFinalByte(b[i]) {
			return i
		}
	}
	return len(b) - 1
}

// isANSIFinalByte reports whether c terminates a CSI escape sequence; final
// bytes occupy the range '@' through '~' (0x40 to 0x7e).
func isANSIFinalByte(c byte) bool {
	return c >= 0x40 && c <= 0x7e
}

// tuiStarted reports whether the accumulated output shows a harness taking
// over the alternate screen, the signal that its full-screen TUI came up.
func tuiStarted(output []byte) bool {
	return bytes.Contains(output, []byte(altScreenEnter))
}

// sawDaemonTimeout reports whether the captured output contains the launcher's
// daemon-timeout marker; a harness whose output does never reached the TUI.
func sawDaemonTimeout(output []byte) bool {
	return strings.Contains(string(output), daemonTimeoutMarker)
}
