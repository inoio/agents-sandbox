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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostlock"
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

	// homeDirName is the isolated HOME directory under a run's home base; the
	// child's home files (including a seeded harness config) live beneath it.
	homeDirName = "home"

	// The XDG dir names isolatedEnv roots under a run's home base, plus the
	// child's TMPDIR. They share these names with prepareHome, which creates
	// them before the child starts: a TMPDIR that does not exist fails sandbox
	// creation.
	configDirName = "config"
	stateDirName  = "state"
	cacheDirName  = "cache"
	tmpDirName    = "tmp"

	// runsDirName holds one directory per harness run, so concurrently running
	// harnesses get separate HOME/XDG_CONFIG/XDG_STATE/XDG_CACHE trees. The
	// microsandbox home stays shared under dataDirName so every harness reuses
	// one image cache (see isolatedEnv/harnessHomeBase).
	runsDirName = "runs"
)

// smokeRootReuseEnv, when set, points the suite at an existing directory it
// keeps between runs instead of a fresh temp root. The microsandbox image cache
// lives under the root's data dir, so a reused root skips the multi-GB
// `docker save | msb ImageLoad` that each harness prebuild otherwise repays.
// Local iteration only: unset (the default) creates and removes a fresh root.
const smokeRootReuseEnv = "AGENTS_SANDBOX_SMOKE_ROOT"

// A sandbox's control-socket path is microsandbox's private home (msbHomeUnderRoot)
// plus a fixed suffix microsandbox appends; it must stay below a Unix socket's
// sun_path limit (104 bytes on macOS, the tighter of the two targets) or every
// harness fails before its TUI with "sandbox runtime socket path is too long".
const (
	sandboxSocketPathSuffix = 50
	sunPathLimit            = 104
)

// smokeMemoryGiB is the memory each smoke harness VM boots with — the VM's
// ceiling, passed to the launcher as --memory. A harness only has to reach its
// TUI, and the launcher's default project-VM size (4 GiB) multiplies across the
// parallel harnesses, so the smoke run boots each smaller.
const smokeMemoryGiB = 2

// smokeWorkingSetMiB is the host memory a smoke VM is assumed to occupy when
// the parallel-harness count is sized. A guest allocates only the pages it
// touches (virtio-mem), so the divisor is an expected working set, not
// smokeMemoryGiB's ceiling. It tracks the heaviest harness, not the lightest:
// measured peaks are under 0.5 GiB for pi and over 1 GiB for opencode2, so
// sizing on pi would oversubscribe the host.
const smokeWorkingSetMiB = 1536

// harnessHostReserveMiB is held back from the host's free memory before the
// parallel-harness count is sized: it covers the host OS, the docker daemon,
// and the internal/sandbox/msb test binary, which runs one real VM at the same
// time as this suite.
const harnessHostReserveMiB = 2048

// harnessConcurrencyOverrideEnv forces the parallel-harness count, for a host
// the memory heuristic sizes badly or for debugging. Unset (or empty) uses the
// memory-derived default.
const harnessConcurrencyOverrideEnv = "AGENTS_SANDBOX_MSB_PARALLEL"

// smokeMemorySpec is the launcher --memory value for a smoke VM.
func smokeMemorySpec() string {
	return fmt.Sprintf("%dG", smokeMemoryGiB)
}

// harnessConcurrencyFor returns how many harnesses may run at once given the
// host's free memory in MiB: as many smoke VMs as fit after the host reserve,
// never fewer than one. The msb-tagged suite wraps it with the host reading, so
// the memory policy is testable without the host.
func harnessConcurrencyFor(freeMiB int) int {
	if usable := freeMiB - harnessHostReserveMiB; usable >= smokeWorkingSetMiB {
		return usable / smokeWorkingSetMiB
	}
	return 1
}

// harnessRunArgs is the `run` invocation a harness smoke subtest drives: the
// agent and network under test, and the smoke memory cap.
func harnessRunArgs(name, network string) []string {
	return []string{
		"run", "--agent", name, "--network", network, "--memory", smokeMemorySpec(),
	}
}

// msbHomeUnderRoot is the private microsandbox home a suite root derives.
func msbHomeUnderRoot(root string) string {
	return filepath.Join(root, dataDirName, "agents-sandbox", "msb")
}

// rootFitsSandboxSocket reports whether root's derived sandbox control-socket
// path stays below the sun_path limit.
func rootFitsSandboxSocket(root string) bool {
	return len(msbHomeUnderRoot(root))+sandboxSocketPathSuffix < sunPathLimit
}

// harnessHomeBase is the per-harness directory holding HOME and the per-harness
// XDG dirs, so concurrently running harnesses never share a home.
func harnessHomeBase(root, name string) string {
	return filepath.Join(root, runsDirName, name)
}

// reuseLockSuffix names the lock file that serializes runs sharing a reused
// suite root. The lock sits beside the root, not inside it, so it never lands
// in the microsandbox home under it.
const reuseLockSuffix = ".lock"

// reuseLockPath is the lock file that serializes runs sharing the reused root
// dir.
func reuseLockPath(dir string) string {
	return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix)
}

// acquireReuseLock blocks until this run holds the exclusive lock for the reused
// root dir, waiting out any other run that shares it. Two runs that share a root
// would otherwise race on the sandbox sweep: whichever finishes first removes
// every sandbox in the home, including the other's live VMs. dir is absolute,
// so the lock path is stable across working directories. It returns the release.
func acquireReuseLock(dir string) (func(), error) {
	return hostlock.AcquireExclusiveWaiting(filepath.Dir(dir), filepath.Base(dir)+reuseLockSuffix, func() {
		fmt.Fprintf(os.Stderr, "harness smoke: reuse root %s is in use by another run; waiting for it to finish\n", dir)
	})
}

// resolveSuiteRoot returns the suite's shared root, whether it is a reused
// directory, and the release for the exclusive lock a reused directory is held
// under (nil for a fresh root). A set smokeRootReuseEnv reuses that directory —
// resolved to an absolute path and validated to fit the sandbox socket path
// budget — so the microsandbox image cache under it survives between runs;
// otherwise a fresh temp root is created for the caller to remove. The lock
// serializes runs that share a root, so a finishing run's sandbox sweep cannot
// destroy VMs another run is still using.
func resolveSuiteRoot() (string, bool, func(), error) {
	if dir := os.Getenv(smokeRootReuseEnv); dir != "" {
		reuseRoot, err := filepath.Abs(dir)
		if err != nil {
			return "", false, nil, fmt.Errorf("resolve reuse root %s: %w", dir, err)
		}
		if !rootFitsSandboxSocket(reuseRoot) {
			return "", false, nil, fmt.Errorf(
				"%s=%q derives a %d-byte sandbox control-socket path, at or above the %d-byte sun_path limit",
				smokeRootReuseEnv, reuseRoot,
				len(msbHomeUnderRoot(reuseRoot))+sandboxSocketPathSuffix, sunPathLimit)
		}
		// reuseRoot is an explicit operator opt-in (the reuse root), not request
		// data.
		//nolint:gosec // G703: reuseRoot is operator-provided, not tainted input
		if err = os.MkdirAll(reuseRoot, 0o750); err != nil {
			return "", false, nil, fmt.Errorf("create reuse root %s: %w", reuseRoot, err)
		}
		release, err := acquireReuseLock(reuseRoot)
		if err != nil {
			return "", false, nil, fmt.Errorf("lock reuse root %s: %w", reuseRoot, err)
		}
		return reuseRoot, true, release, nil
	}
	root, err := mkdtemp(smokeRootBase, smokeRootPrefix)
	if err != nil {
		return "", false, nil, err
	}
	return root, false, nil, nil
}

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
// `opencode2`'s, `pi`'s, and `oh-my-pi`'s documented `app_exit`/`app.exit`, and Ctrl-C is the
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
	// alternate screen never announces that its UI is up. Its startup splash
	// prints the product name, which readies the subtest.
	"claude-code": {
		QuitKey:           []byte{ctrlC},
		Network:           publicNetwork,
		KeepsNormalBuffer: true,
		ReadyMarker:       claudeCodeReadyMarker,
	},
	// codex quits on Ctrl-C and may reach its API at startup, so it gets
	// egress like claude-code; its TUI uses the alternate screen.
	"codex": {QuitKey: []byte{ctrlC}, Network: publicNetwork, KeepsNormalBuffer: false},
	// hermes' classic CLI keeps the terminal's normal buffer, so the alternate
	// screen never announces that its UI is up. The subtest seeds a home config
	// that makes a dummy, loopback provider count as configured, so hermes starts
	// the classic CLI REPL instead of stopping at its first-run setup guard (which
	// always exits 1 without a real provider); Ctrl-D then leaves the REPL with
	// exit 0. The seeded endpoint is loopback, so it runs under the default
	// `--network none`; the startup banner is the readiness marker. Its runner
	// image carries a whole Python/Node tool store, so its startup is the slowest
	// of the harnesses.
	"hermes": {
		QuitKey:           []byte{ctrlD},
		KeepsNormalBuffer: true,
		ReadyMarker:       hermesReadyMarker,
		ReadyTimeout:      heavyweightReadyTimeout,
		SeedHomeFiles:     map[string]string{hermesConfigPath: hermesSmokeConfig},
	},
	// oh-my-pi reaches its TUI without credentials, like pi, which it forks.
	// Its renderer keeps the terminal's normal buffer (it borrows the alternate
	// screen only while resizing or for fullscreen overlays), so readiness is a
	// marker rather than the alternate screen: with no model configured it prints
	// its "No default model selected" warning above the editor's prompt bar. The
	// subtest seeds a home config that turns off the first-run setup wizard, a
	// fullscreen overlay that would otherwise come up before the editor. It needs
	// no egress, so it runs under the default `--network none`.
	"oh-my-pi": {
		QuitKey:           []byte{ctrlD},
		KeepsNormalBuffer: true,
		ReadyMarker:       ohMyPiReadyMarker,
		SeedHomeFiles:     map[string]string{ohMyPiConfigPath: ohMyPiSmokeConfig},
	},
	// opencode's editor owns Ctrl-C, so its TUI leaves on Ctrl-D.
	"opencode":  {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
	"opencode2": {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
	"pi":        {QuitKey: []byte{ctrlD}, Network: "", KeepsNormalBuffer: false},
}

// harnessSpec describes how to drive one harness to its TUI and out of it.
//
//exhaustruct:ignore
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
	// ReadyMarker, when set, is an output substring that announces a
	// normal-buffer harness's UI is up — the readiness signal waitSettled
	// replaces for a harness whose CLI prints a stable startup line (hermes'
	// banner). It takes precedence over KeepsNormalBuffer's settle.
	ReadyMarker string
	// ReadyTimeout overrides the readiness wait for a KeepsNormalBuffer
	// harness; zero uses the shared default. It bounds either the marker wait
	// or the settle. It is ignored for an alternate-screen harness, whose
	// readiness is the alternate-screen marker rather than a wall clock.
	ReadyTimeout time.Duration
	// SeedHomeFiles maps a home-relative path to its content, written under the
	// isolated HOME (<root>/home) before the run so the launcher provisions it
	// into the VM. It is how a harness that needs host-side configuration (a
	// provider block, a settled opt-in) starts headful without credentials.
	SeedHomeFiles map[string]string
}

// settledReadyTimeout bounds the readiness wait for a harness whose TUI keeps
// the terminal's normal buffer, where no marker announces that its UI is up. It
// covers the launcher's image load and VM start — about 14s on a cold host when
// the image prebuild has already run — with margin. A harness whose runner image
// loads far slower overrides it (see harnessSpec.ReadyTimeout).
const settledReadyTimeout = 25 * time.Second

// heavyweightReadyTimeout bounds the readiness wait for a KeepsNormalBuffer
// harness whose startup is much slower than the lightweight agents the shared
// budget was measured on: hermes carries a whole Python/Node tool store. It is a
// ceiling, not a sleep — hermes' banner marker ends the wait as soon as the CLI
// is up — so a slow cold start fails the subtest only when it genuinely hangs.
const heavyweightReadyTimeout = 120 * time.Second

// hermesReadyMarker is the startup line hermes' classic CLI prints before it
// accepts input (show_banner: "Hermes Agent v<version> (<date>)"). It is stable
// across releases, unlike the version itself.
const hermesReadyMarker = "Hermes Agent v"

// claudeCodeReadyMarker is the product name claude-code's startup splash prints
// before its prompt is live. Its renderer keeps the normal buffer and emits no
// alternate-screen sequence, so without a marker its subtest would wait out the
// whole settle.
const claudeCodeReadyMarker = "Claude Code"

// ohMyPiReadyMarker is the warning oh-my-pi prints when it starts its editor
// with no model configured, which is exactly the smoke run: the seeded config
// turns off the setup wizard and sets no model, and the child's restricted
// environment carries no API-key variables. It renders just above the editor's
// prompt bar, so it marks the editor being up; like claude-code, oh-my-pi keeps
// the normal buffer and emits no alternate-screen sequence.
const ohMyPiReadyMarker = "No default model selected"

// hermesConfigPath is hermes' config file, home-relative, provisioned into the
// guest from the host's ~/.hermes/config.yaml.
const hermesConfigPath = ".hermes/config.yaml"

// hermesSmokeConfig makes hermes start its interactive REPL without a real
// credential: the model block's custom, loopback base_url (and dummy api_key)
// satisfies hermes' "any provider configured" check, so the classic CLI starts
// instead of the first-run setup guard, which exits 1 with no provider. The
// endpoint is a closed loopback port, so no request leaves the VM; the deeper
// model keys only matter once a message is submitted. The telemetry block
// settles the shared-metrics opt-in so hermes does not show that prompt before
// the REPL (offer_version must match hermes' current OFFER_VERSION).
const hermesSmokeConfig = `model:
  provider: custom
  default: local-model
  base_url: http://127.0.0.1:9/v1
  api_key: dummy
telemetry:
  shared_metrics:
    enabled: false
    send: false
    offer_version: 2
`

// ohMyPiConfigPath is omp's global config file, home-relative, provisioned
// into the guest from the host's ~/.omp/agent/config.yml.
const ohMyPiConfigPath = ".omp/agent/config.yml"

// ohMyPiSmokeConfig turns off omp's first-run setup wizard (provider sign-in,
// model, and theme scenes), so the smoke test lands in the editor.
const ohMyPiSmokeConfig = `startup:
  setupWizard: false
`

// readyTimeout returns the readiness wait for this harness: its per-harness
// override when set, otherwise settledReadyTimeout.
func (s harnessSpec) readyTimeout() time.Duration {
	if s.ReadyTimeout > 0 {
		return s.ReadyTimeout
	}
	return settledReadyTimeout
}

// isolatedEnv builds the child environment from inherited: it keeps only the
// allowlisted variables (defaulting TERM when it is absent, empty, or "dumb"),
// then appends a private HOME/XDG_*/TMPDIR tree rooted at homeBase, so the child
// reads and writes no real agents-sandbox config or state. XDG_DATA_HOME stays
// rooted at root so every harness shares one microsandbox home (the image cache)
// while keeping its own HOME and config/state/cache dirs.
func isolatedEnv(root, homeBase string, inherited []string) []string {
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
		"HOME="+filepath.Join(homeBase, homeDirName),
		"XDG_CONFIG_HOME="+filepath.Join(homeBase, configDirName),
		"XDG_DATA_HOME="+filepath.Join(root, dataDirName),
		"XDG_STATE_HOME="+filepath.Join(homeBase, stateDirName),
		"XDG_CACHE_HOME="+filepath.Join(homeBase, cacheDirName),
		"TMPDIR="+filepath.Join(homeBase, tmpDirName),
	)
}

// prepareHome creates the per-harness home tree isolatedEnv points the child at:
// its HOME, TMPDIR, and the XDG config/state/cache dirs. TMPDIR must exist before
// the child starts — the launcher and the microsandbox runtime create temporary
// files through os.TempDir() while setting up a sandbox, so a TMPDIR that does
// not exist fails the run with `create prefill sandbox: io error: no such file
// or directory`.
func prepareHome(homeBase string) error {
	for _, dir := range []string{homeDirName, tmpDirName, configDirName, stateDirName, cacheDirName} {
		if err := mkdirAll(filepath.Join(homeBase, dir), 0o750); err != nil {
			return fmt.Errorf("create %s under %s: %w", dir, homeBase, err)
		}
	}
	return nil
}

// seedHomeFiles writes each home-relative file under the run's isolated HOME
// (<homeBase>/home/<rel>), creating parent directories, so the launcher
// provisions it into the VM (see harnessSpec.SeedHomeFiles).
//
// It writes through mkdirAll and writeFile, seams a test can replace to assert
// the seed's private modes without depending on the process umask.
func seedHomeFiles(homeBase string, files map[string]string) error {
	for rel, content := range files {
		path := filepath.Join(homeBase, homeDirName, rel)
		if err := mkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create parent for %s: %w", rel, err)
		}
		if err := writeFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// mkdirAll and writeFile are the filesystem seams seedHomeFiles writes through;
// mkdtemp is the seam resolveSuiteRoot creates a fresh root through. A test
// replaces one to assert its failure branch.
var (
	mkdirAll  = os.MkdirAll
	writeFile = os.WriteFile
	mkdtemp   = os.MkdirTemp
)

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
