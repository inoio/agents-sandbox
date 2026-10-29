package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
)

func TestHarnessSpecsCoverEveryRegisteredAgent(t *testing.T) {
	names := agent.Names()
	if len(names) == 0 {
		t.Fatal("no agents registered")
	}
	for _, name := range names {
		if _, ok := harnessSpecs[name]; !ok {
			t.Errorf("registered harness %q has no smoke-test spec; add its quit key", name)
		}
	}
	for name := range harnessSpecs {
		if _, ok := agent.Lookup(name); !ok {
			t.Errorf("smoke-test spec %q matches no registered harness", name)
		}
	}
}

// TestHarnessSpecsQuitKeyNonEmpty pins that every harness names the key the
// smoke test taps until its process goes.
func TestHarnessSpecsQuitKeyNonEmpty(t *testing.T) {
	for name, spec := range harnessSpecs {
		if len(spec.QuitKey) == 0 {
			t.Errorf("harness %q has no key for the smoke test to press", name)
		}
	}
}

// TestReadyTimeoutPrefersThePerHarnessOverride pins the readiness-budget
// selection: a spec that names an override uses it, and one that leaves it
// unset falls back to the shared default.
func TestReadyTimeoutPrefersThePerHarnessOverride(t *testing.T) {
	const fallback = 25 * time.Second

	if got := (harnessSpec{ReadyTimeout: 90 * time.Second}).readyTimeout(fallback); got != 90*time.Second {
		t.Errorf("readyTimeout() = %s, want the override 90s", got)
	}
	if got := (harnessSpec{}).readyTimeout(fallback); got != fallback {
		t.Errorf("readyTimeout() = %s, want the fallback %s", got, fallback)
	}
}

// TestHermesUsesTheHeavyweightReadyBudget pins that hermes, whose runner image
// carries a full Python/Node tool store, extends the settled wait instead of
// failing the subtest while the launcher is still loading the image.
func TestHermesUsesTheHeavyweightReadyBudget(t *testing.T) {
	spec, ok := harnessSpecs["hermes"]
	if !ok {
		t.Fatal("hermes has no smoke-test spec")
	}
	if spec.ReadyTimeout != heavyweightSettledReadyTimeout {
		t.Errorf("hermes ReadyTimeout = %s, want %s", spec.ReadyTimeout, heavyweightSettledReadyTimeout)
	}
	if !spec.KeepsNormalBuffer {
		t.Error("hermes KeepsNormalBuffer = false; the heavy budget is the normal-buffer readiness wait")
	}
}

// TestSawDaemonTimeout pins that the shared marker helper fires on the
// launcher's real daemon-timeout phrase and stays quiet on unrelated output.
func TestSawDaemonTimeout(t *testing.T) {
	launcherPhrase := "opencode2 daemon did not become healthy within 1m0s: 401 unauthorized"
	if !strings.Contains(launcherPhrase, "daemon did not become healthy") {
		t.Fatalf("test phrase %q does not carry the launcher's daemon-timeout wording", launcherPhrase)
	}
	if !sawDaemonTimeout([]byte("launcher output\n" + launcherPhrase)) {
		t.Error("sawDaemonTimeout = false, want true for the launcher's daemon-timeout phrase")
	}
	if sawDaemonTimeout([]byte("launcher output\nno daemon timeout here")) {
		t.Error("sawDaemonTimeout = true, want false for unrelated output")
	}
}

func TestIsolatedEnvAllowlist(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin:/bin",
		"TERM=dumb",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"HTTP_PROXY=http://proxy.example:3128",
		"HOME=/real/home",
		"XDG_DATA_HOME=/real/data",
		"AGENTS_SANDBOX_NETWORK_PROFILE=public",
		"OPENCODE_SANDBOX_NOTIFY=on",
		"MSB_HOME=/real/msb",
		"SECRET_TOKEN=leak",
	}
	env := isolatedEnv("/root", inherited)
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	want := map[string]string{
		"PATH":            "/usr/bin:/bin",
		"TERM":            "xterm-256color",
		"LANG":            "en_US.UTF-8",
		"LC_ALL":          "C.UTF-8",
		"HTTP_PROXY":      "http://proxy.example:3128",
		"HOME":            "/root/home",
		"XDG_CONFIG_HOME": "/root/config",
		"XDG_DATA_HOME":   "/root/data",
		"XDG_STATE_HOME":  "/root/state",
		"XDG_CACHE_HOME":  "/root/cache",
		"TMPDIR":          "/root/tmp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range []string{
		"AGENTS_SANDBOX_NETWORK_PROFILE",
		"OPENCODE_SANDBOX_NOTIFY",
		"MSB_HOME",
		"SECRET_TOKEN",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be inherited (got %q)", k, got[k])
		}
	}
}

// TestSmokeRootStaysShortEnoughForSandboxSockets pins why the suite anchors its
// shared temp root at a short, fixed base instead of the host's TMPDIR:
// microsandbox derives its sandbox control-socket path from the private home
// under XDG_DATA_HOME (<root>/data/agents-sandbox/msb), and a long TMPDIR —
// macOS's default is ~49 bytes — pushes the derived path past a Unix socket's
// sun_path limit, failing every harness before its TUI with "sandbox runtime
// socket path is too long".
func TestSmokeRootStaysShortEnoughForSandboxSockets(t *testing.T) {
	// os.MkdirTemp appends nine random characters after the pattern.
	const mkdirTempSuffix = "123456789"
	// The private microsandbox home agents-sandbox derives from XDG_DATA_HOME:
	// <data>/agents-sandbox/msb (configpaths.UserDataDir plus its "msb" child).
	home := filepath.Join(smokeRootBase, smokeRootPrefix+mkdirTempSuffix, dataDirName, "agents-sandbox", "msb")
	// microsandbox builds the socket path by appending a fixed suffix to the
	// home (50 bytes as of v0.7.8) and rejects one that does not fit a Unix
	// socket's sun_path, 104 bytes on macOS, the tighter of the two targets.
	const (
		socketPathSuffix = 50
		sunPathLimit     = 104
	)
	if derived := len(home) + socketPathSuffix; derived >= sunPathLimit {
		t.Errorf(
			"derived sandbox socket path for home %q is %d bytes, at or above the %d-byte sun_path limit; keep smokeRootBase and smokeRootPrefix short",
			home,
			derived,
			sunPathLimit,
		)
	}
}

// TestIsolatedEnvKeepsDockerEndpoint pins that the docker endpoint variables
// survive: the child needs the same daemon the preflight reaches, and they
// carry no agents-sandbox config or state.
func TestIsolatedEnvKeepsDockerEndpoint(t *testing.T) {
	inherited := []string{
		"DOCKER_HOST=unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT=colima",
		"DOCKER_CONFIG=/real/.docker",
	}
	got := map[string]string{}
	for _, kv := range isolatedEnv("/root", inherited) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, want := range map[string]string{
		"DOCKER_HOST":    "unix:///run/user/1000/docker.sock",
		"DOCKER_CONTEXT": "colima",
		"DOCKER_CONFIG":  "/real/.docker",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// TestIsolatedEnvDefaultsTerm pins the TERM fallback: an absent, empty, or
// "dumb" inherited value is replaced with a modern terminfo entry, and any
// other value passes through.
func TestIsolatedEnvDefaultsTerm(t *testing.T) {
	for _, test := range []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{name: "absent", set: false, value: "", want: defaultTerm},
		{name: "empty", set: true, value: "", want: defaultTerm},
		{name: "dumb", set: true, value: termDumb, want: defaultTerm},
		{name: "present", set: true, value: "screen-256color", want: "screen-256color"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inherited := []string{"PATH=/usr/bin:/bin"}
			if test.set {
				inherited = append(inherited, envTerm+"="+test.value)
			}
			if got := envValue(isolatedEnv("/root", inherited), envTerm); got != test.want {
				t.Errorf("TERM = %q, want %q", got, test.want)
			}
		})
	}
}

// envValue returns the value of name in env, or "" when it is absent.
func envValue(env []string, name string) string {
	for _, entry := range env {
		if k, v, _ := strings.Cut(entry, "="); k == name {
			return v
		}
	}
	return ""
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI([]byte("\x1b[31mred\x1b[0m")); got != "red" {
		t.Errorf("stripANSI = %q, want %q", got, "red")
	}
	// A sequence truncated by the end of the buffer is dropped too.
	if got := stripANSI([]byte("red\x1b[3")); got != "red" {
		t.Errorf("stripANSI = %q, want %q for a truncated sequence", got, "red")
	}
}

func TestTUIStarted(t *testing.T) {
	if !tuiStarted([]byte("launcher output\n\x1b[?1049htui")) {
		t.Error("tuiStarted = false, want true for alternate-screen enter sequence")
	}
	if tuiStarted([]byte("launcher output\nno tui yet")) {
		t.Error("tuiStarted = true, want false without the sequence")
	}
}
