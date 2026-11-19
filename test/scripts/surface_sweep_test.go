package scripts

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestSurfaceSweep ports every case of the bash harness scripts/test-surface-sweep.sh,
// asserting the same exit codes, stdout/stderr substrings, asb/docker argv, and
// on-disk artifacts. The harness fakes `asb` (ASB_BIN) and `docker` (PATH) as
// re-exec test-binary handlers (see surfaceSweepAsbHandler/surfaceSweepDockerHandler,
// registered in main_test.go); the bash stubs' per-case rewrites become environment
// knobs the handler reads. The root is staged directly under /tmp because surface-sweep
// derives a sandbox control socket path from it that must fit sun_path: t.TempDir()'s
// path is too long. XDG_DATA_HOME/HOME are pointed under the test root, so nothing the
// script derives can reach the developer's real state.
func TestSurfaceSweep(t *testing.T) {
	t.Run("init refuses a root whose socket path is too long", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		longRoot := filepath.Join(t.TempDir(), strings.Repeat("x", 120))

		result := h.runIn(t.TempDir(), map[string]string{"ASB_SURFACE_ROOT": longRoot}, "init")

		surfaceSweepAssertNonzero(t, "init", result)
		assertContains(t, "stderr", result.Stderr, "socket path")
		if _, err := os.Stat(longRoot); !os.IsNotExist(err) {
			t.Errorf("too-long root was created (stat err = %v)", err)
		}
	})

	t.Run("init creates the root tree and protects it 0700", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)

		result := h.run(nil, "init")

		assertStatus(t, result, 0)
		if got := strings.TrimRight(result.Stdout, "\n"); got != h.root {
			t.Errorf("init stdout = %q, want %q", got, h.root)
		}
		for _, name := range []string{"data", "runs"} {
			if info, err := os.Stat(filepath.Join(h.root, name)); err != nil || !info.IsDir() {
				t.Errorf("init did not create %s (stat err = %v)", name, err)
			}
		}
		info, err := os.Stat(h.root)
		if err != nil {
			t.Fatalf("stat root: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("root mode = %o, want 700", got)
		}
	})

	t.Run("init resolves a relative root absolute", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		// A PID-sized name mirrors the deleted bash harness's asb-rel-$$: the
		// MkdirTemp suffix (9-10 digits) pushes the derived socket path over the
		// sun_path budget on macOS, where /tmp resolves to /private/tmp.
		name := "asb-rel-" + strconv.Itoa(os.Getpid())
		physical, err := filepath.EvalSymlinks(surfaceSweepRootBase)
		if err != nil {
			t.Fatalf("resolve %s: %v", surfaceSweepRootBase, err)
		}
		expected := filepath.Join(physical, name)
		t.Cleanup(func() {
			_ = os.RemoveAll(expected)
			_ = os.RemoveAll(expected + ".lock")
		})

		result := h.runIn(surfaceSweepRootBase, map[string]string{"ASB_SURFACE_ROOT": name}, "init")

		assertStatus(t, result, 0)
		if got := strings.TrimRight(result.Stdout, "\n"); got != expected {
			t.Errorf("relative root resolved to %q, want %q", got, expected)
		}
	})

	t.Run("env and sweep refuse a too-long root", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		longRoot := filepath.Join(t.TempDir(), strings.Repeat("x", 120))

		envResult := h.runIn(t.TempDir(), map[string]string{"ASB_SURFACE_ROOT": longRoot}, "env", "foo")
		sweepResult := h.runIn(t.TempDir(), map[string]string{"ASB_SURFACE_ROOT": longRoot}, "sweep")

		surfaceSweepAssertNonzero(t, "env", envResult)
		surfaceSweepAssertNonzero(t, "sweep", sweepResult)
		assertContains(t, "env stderr", envResult.Stderr, "socket path")
		assertContains(t, "sweep stderr", sweepResult.Stderr, "socket path")
	})

	t.Run("env creates the per-run tree and exports the isolated vars", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)

		result := h.run(nil, "env", "foo")

		assertStatus(t, result, 0)
		for _, name := range []string{"home", "config", "state", "cache", "tmp", "project"} {
			path := filepath.Join(h.root, "runs", "foo", name)
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Errorf("env did not create runs/foo/%s (stat err = %v)", name, err)
			}
		}
		for _, name := range []string{
			"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TMPDIR",
		} {
			assertContains(t, "env stdout", result.Stdout, "export "+name+"=")
		}
		assertContains(t, "env stdout", result.Stdout, `export XDG_DATA_HOME="`+filepath.Join(h.root, "data")+`"`)
	})

	t.Run("env is idempotent and requires a name", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)

		first := h.run(nil, "env", "foo")
		before := surfaceSweepTree(t, filepath.Join(h.root, "runs", "foo"))
		again := h.run(nil, "env", "foo")
		after := surfaceSweepTree(t, filepath.Join(h.root, "runs", "foo"))

		assertStatus(t, first, 0)
		assertStatus(t, again, 0)
		if !slices.Equal(before, after) {
			t.Errorf("env changed runs/foo: %v -> %v", before, after)
		}
		if first.Stdout != again.Stdout {
			t.Errorf("env prints different vars on re-run:\n%q\n%q", first.Stdout, again.Stdout)
		}

		missing := h.run(nil, "env")
		surfaceSweepAssertNonzero(t, "env without a name", missing)
	})

	t.Run("env drops the ambient config-override namespaces", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		leak := map[string]string{
			"AGENTS_SANDBOX_NETWORK_PROFILE": "none",
			"OPENCODE_SANDBOX_CPUS":          "99",
			"MSB_HOME":                       "/tmp/real",
		}

		result := h.run(leak, "env", "leaky")

		assertContains(t, "env stdout", result.Stdout, "AGENTS_SANDBOX_NETWORK_PROFILE")
		assertContains(t, "env stdout", result.Stdout, "OPENCODE_SANDBOX_CPUS")
		assertContains(t, "env stdout", result.Stdout, "MSB_HOME")

		evalScript := filepath.Join(t.TempDir(), "eval-env.sh")
		if err := os.WriteFile(evalScript, []byte(surfaceSweepEvalBody), 0o700); err != nil {
			t.Fatalf("write eval script: %v", err)
		}
		evalResult := RunScript(t, h.root, leak, evalScript, h.root, h.scriptPath)

		assertStatus(t, evalResult, 0)
		if got := strings.TrimRight(evalResult.Stdout, "\n"); got != "unset|unset" {
			t.Errorf("eval applied drops the ambient overrides = %q, want %q", got, "unset|unset")
		}
	})

	t.Run("sweep without the coordinator lock is refused", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		h.run(nil, "init")

		result := h.run(nil, "sweep")

		surfaceSweepAssertNonzero(t, "unlocked sweep", result)
		assertContains(t, "stderr", result.Stderr, h.root+".lock")
		if log := readText(t, h.asbLog); log != "" {
			t.Errorf("unlocked sweep still called asb:\n%s", log)
		}
	})

	t.Run("sweep removes sandboxes and volumes with --yes", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		h.run(nil, "init")
		h.run(nil, "lock")

		result := h.run(map[string]string{
			"AGENTS_SANDBOX_QUIET":        "true",
			"SURFACE_SWEEP_ASB_SANDBOXES": "sandbox-a\nsandbox-b",
			"SURFACE_SWEEP_ASB_VOLUMES":   "vol-a\nvol-b",
		}, "sweep")

		assertStatus(t, result, 0)
		log := readText(t, h.asbLog)
		for _, needle := range []string{
			"sandbox remove --force --yes sandbox-a",
			"sandbox remove --force --yes sandbox-b",
			"volume remove --force --yes vol-a",
			"volume remove --force --yes vol-b",
			"volume list -q",
			filepath.Join(h.root, "data"),
			filepath.Join(h.root, "runs", "coordinator", "home"),
		} {
			assertContains(t, "asb log", log, needle)
		}
	})

	t.Run("empty sweep prints nothing and exits 0", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		h.run(nil, "init")
		h.run(nil, "lock")

		result := h.run(nil, "sweep")

		assertStatus(t, result, 0)
		if result.Stdout != "" {
			t.Errorf("empty sweep stdout = %q, want empty", result.Stdout)
		}
	})

	t.Run("sweep fails when a list command fails", func(t *testing.T) {
		t.Run("sandbox list", func(t *testing.T) {
			h := newSurfaceSweepHarness(t)
			h.run(nil, "init")
			h.run(nil, "lock")

			result := h.run(map[string]string{"SURFACE_SWEEP_ASB_FAIL_SANDBOX_LIST": "1"}, "sweep")

			surfaceSweepAssertNonzero(t, "sweep", result)
			assertContains(t, "stderr", result.Stderr, "sandbox list failed")
		})

		t.Run("volume list", func(t *testing.T) {
			h := newSurfaceSweepHarness(t)
			h.run(nil, "init")
			h.run(nil, "lock")

			result := h.run(map[string]string{"SURFACE_SWEEP_ASB_FAIL_VOLUME_LIST": "1"}, "sweep")

			surfaceSweepAssertNonzero(t, "sweep", result)
			assertContains(t, "stderr", result.Stderr, "volume list failed")
		})
	})

	t.Run("a failed removal is reported while later removals still run", func(t *testing.T) {
		t.Run("sandbox removal", func(t *testing.T) {
			h := newSurfaceSweepHarness(t)
			h.run(nil, "init")
			h.run(nil, "lock")

			result := h.run(map[string]string{
				"SURFACE_SWEEP_ASB_SANDBOXES":           "sandbox-a\nsandbox-b",
				"SURFACE_SWEEP_ASB_VOLUMES":             "vol-a",
				"SURFACE_SWEEP_ASB_FAIL_SANDBOX_REMOVE": "sandbox-a",
			}, "sweep")

			surfaceSweepAssertNonzero(t, "sweep", result)
			assertContains(t, "stderr", result.Stderr, "failed to remove sandbox sandbox-a")
			log := readText(t, h.asbLog)
			assertContains(t, "asb log", log, "sandbox remove --force --yes sandbox-b")
			assertContains(t, "asb log", log, "volume remove --force --yes vol-a")
		})

		t.Run("volume removal", func(t *testing.T) {
			h := newSurfaceSweepHarness(t)
			h.run(nil, "init")
			h.run(nil, "lock")

			result := h.run(map[string]string{
				"SURFACE_SWEEP_ASB_SANDBOXES":          "sandbox-a",
				"SURFACE_SWEEP_ASB_VOLUMES":            "vol-a\nvol-b",
				"SURFACE_SWEEP_ASB_FAIL_VOLUME_REMOVE": "vol-a",
			}, "sweep")

			surfaceSweepAssertNonzero(t, "sweep", result)
			assertContains(t, "stderr", result.Stderr, "failed to remove volume vol-a")
			assertContains(t, "asb log", readText(t, h.asbLog), "volume remove --force --yes vol-b")
		})
	})

	t.Run("init records the runner-image baseline", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)

		h.run(map[string]string{
			"SURFACE_SWEEP_DOCKER_IMAGES": "agents-sandbox/runner-kept-aaaaaaaaaaaaaa:opencode-latest",
		}, "init")

		baseline := strings.TrimRight(readText(t, filepath.Join(h.root, ".runner-images")), "\n")
		want := "agents-sandbox/runner-kept-aaaaaaaaaaaaaa:opencode-latest"
		if baseline != want {
			t.Errorf("baseline = %q, want %q", baseline, want)
		}
	})

	t.Run("sweep removes only the tags added since the baseline", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		const kept = "agents-sandbox/runner-kept-aaaaaaaaaaaaaa:opencode-latest"
		const added = "agents-sandbox/runner-new-bbbbbbbbbbbbbb:opencode-latest"
		h.run(map[string]string{"SURFACE_SWEEP_DOCKER_IMAGES": kept}, "init")
		h.run(nil, "lock")

		result := h.run(map[string]string{
			"SURFACE_SWEEP_DOCKER_IMAGES": kept + "\n" + added,
		}, "sweep")

		assertStatus(t, result, 0)
		log := readText(t, h.dockerLog)
		assertContains(t, "docker log", log, "rmi --no-prune "+added)
		assertNotContains(t, "docker log", log, "rmi --no-prune "+kept)
	})

	t.Run("init warns when docker is unreachable and leaves no baseline", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)

		result := h.run(map[string]string{"SURFACE_SWEEP_DOCKER_FAIL_ALL": "1"}, "init")

		assertStatus(t, result, 0)
		assertContains(t, "stderr", result.Stderr, "not reachable")
		if _, err := os.Stat(filepath.Join(h.root, ".runner-images")); !os.IsNotExist(err) {
			t.Errorf("baseline written without docker (stat err = %v)", err)
		}
	})

	t.Run("sweep without a baseline skips docker cleanup", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		h.run(nil, "init")
		h.run(nil, "lock")
		if err := os.Remove(filepath.Join(h.root, ".runner-images")); err != nil {
			t.Fatalf("remove baseline: %v", err)
		}

		result := h.run(nil, "sweep")

		assertContains(t, "stderr", result.Stderr, "skipping docker cleanup")
	})

	t.Run("a failed rmi is reported and fails the sweep", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		const kept = "agents-sandbox/runner-kept-aaaaaaaaaaaaaa:opencode-latest"
		const added = "agents-sandbox/runner-new-bbbbbbbbbbbbbb:opencode-latest"
		h.run(map[string]string{"SURFACE_SWEEP_DOCKER_IMAGES": kept}, "init")
		h.run(nil, "lock")

		result := h.run(map[string]string{
			"SURFACE_SWEEP_DOCKER_IMAGES":   added,
			"SURFACE_SWEEP_DOCKER_FAIL_RMI": "1",
		}, "sweep")

		surfaceSweepAssertNonzero(t, "sweep", result)
		assertContains(t, "stderr", result.Stderr, "failed to remove runner image")
	})

	t.Run("the lock is a portable exclusive mkdir", func(t *testing.T) {
		h := newSurfaceSweepHarness(t)
		lockDir := h.root + ".lock"

		assertStatus(t, h.run(nil, "lock"), 0)
		if info, err := os.Stat(lockDir); err != nil || !info.IsDir() {
			t.Fatalf("lock did not create %s (stat err = %v)", lockDir, err)
		}

		surfaceSweepAssertNonzero(t, "second lock", h.run(nil, "lock"))

		assertStatus(t, h.run(nil, "unlock"), 0)
		if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
			t.Errorf("unlock left %s behind (stat err = %v)", lockDir, err)
		}

		surfaceSweepAssertNonzero(t, "unlock without a held lock", h.run(nil, "unlock"))
	})
}

// surfaceSweepRootBase is the short directory the harness stages its root under.
// The root must fit microsandbox's control-socket budget
// (<root>/data/agents-sandbox/msb plus a fixed suffix under sun_path's 104), and
// t.TempDir()'s path is too long for it.
const surfaceSweepRootBase = "/tmp"

// The environment knobs the fake asb/docker handlers read in the re-executed test
// process. The bash harness rewrote its stub scripts per case; the Go handler is
// registered once in TestMain and reads its behavior from these instead.
const (
	surfaceSweepAsbLogEnv          = "SURFACE_SWEEP_ASB_LOG"
	surfaceSweepAsbSandboxesEnv    = "SURFACE_SWEEP_ASB_SANDBOXES"
	surfaceSweepAsbVolumesEnv      = "SURFACE_SWEEP_ASB_VOLUMES"
	surfaceSweepAsbFailSandboxList = "SURFACE_SWEEP_ASB_FAIL_SANDBOX_LIST"
	surfaceSweepAsbFailVolumeList  = "SURFACE_SWEEP_ASB_FAIL_VOLUME_LIST"
	surfaceSweepAsbFailSandboxRm   = "SURFACE_SWEEP_ASB_FAIL_SANDBOX_REMOVE"
	surfaceSweepAsbFailVolumeRm    = "SURFACE_SWEEP_ASB_FAIL_VOLUME_REMOVE"

	surfaceSweepDockerLogEnv     = "SURFACE_SWEEP_DOCKER_LOG"
	surfaceSweepDockerImagesEnv  = "SURFACE_SWEEP_DOCKER_IMAGES"
	surfaceSweepDockerFailAllEnv = "SURFACE_SWEEP_DOCKER_FAIL_ALL"
	surfaceSweepDockerFailRmiEnv = "SURFACE_SWEEP_DOCKER_FAIL_RMI"
)

// surfaceSweepEvalBody sources surface-sweep's env output and prints whether the
// ambient override namespaces survived, so the harness asserts the unset line
// actually drops them rather than merely naming them.
const surfaceSweepEvalBody = `eval "$(ASB_SURFACE_ROOT="$1" bash "$2" env leaky)"
printf '%s|%s\n' "${AGENTS_SANDBOX_NETWORK_PROFILE-unset}" "${MSB_HOME-unset}"
`

// surfaceSweepAsbHandler serves the fake launcher `asb`. It logs each invocation
// with the HOME/XDG_DATA_HOME the script sourced (so a case can prove the sweep
// runs against the isolated data home, not the real one), then emulates
// `sandbox list -q`, `volume list -q`, and the removals from the knobs above.
func surfaceSweepAsbHandler(args []string) (string, string, int) {
	if logPath := envOption(surfaceSweepAsbLogEnv); logPath != "" {
		if err := appendLine(logPath, "home="+envOption("HOME")+" xdg_data="+envOption("XDG_DATA_HOME")); err != nil {
			return "", "fake asb: record environment: " + err.Error() + "\n", 1
		}
		if err := appendLine(logPath, strings.Join(args, " ")); err != nil {
			return "", "fake asb: record argv: " + err.Error() + "\n", 1
		}
	}
	switch strings.Join(args, " ") {
	case "sandbox list -q":
		if envOption(surfaceSweepAsbFailSandboxList) != "" {
			return "", "", 1
		}
		return surfaceSweepOutputLines(envOption(surfaceSweepAsbSandboxesEnv)), "", 0
	case "volume list -q":
		if envOption(surfaceSweepAsbFailVolumeList) != "" {
			return "", "", 1
		}
		return surfaceSweepOutputLines(envOption(surfaceSweepAsbVolumesEnv)), "", 0
	}
	if name, ok := surfaceSweepRemoval(args, "sandbox"); ok && name == envOption(surfaceSweepAsbFailSandboxRm) {
		return "", "", 1
	}
	if name, ok := surfaceSweepRemoval(args, "volume"); ok && name == envOption(surfaceSweepAsbFailVolumeRm) {
		return "", "", 1
	}
	return "", "", 0
}

// surfaceSweepDockerHandler serves the fake `docker` on PATH. It logs its argv
// and emulates `docker images` (the knob's tags) and `docker rmi`.
func surfaceSweepDockerHandler(args []string) (string, string, int) {
	if envOption(surfaceSweepDockerFailAllEnv) != "" {
		return "", "", 1
	}
	if logPath := envOption(surfaceSweepDockerLogEnv); logPath != "" {
		if err := appendLine(logPath, strings.Join(args, " ")); err != nil {
			return "", "fake docker: record argv: " + err.Error() + "\n", 1
		}
	}
	switch {
	case len(args) > 0 && args[0] == "images":
		return surfaceSweepOutputLines(envOption(surfaceSweepDockerImagesEnv)), "", 0
	case len(args) > 0 && args[0] == "rmi":
		if envOption(surfaceSweepDockerFailRmiEnv) != "" {
			return "", "", 1
		}
	}
	return "", "", 0
}

// surfaceSweepRemoval reports the name a `<kind> remove --force --yes <name>`
// call targets.
func surfaceSweepRemoval(args []string, kind string) (string, bool) {
	if len(args) == 5 && args[0] == kind && args[1] == "remove" && args[2] == "--force" && args[3] == "--yes" {
		return args[4], true
	}
	return "", false
}

// surfaceSweepOutputLines renders a newline-separated knob as command output: one
// line each, blank lines dropped (mirroring the bash stub's `grep -v '^$'`), with
// a trailing newline.
func surfaceSweepOutputLines(value string) string {
	var lines []string
	for line := range strings.SplitSeq(value, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// surfaceSweepHarness drives scripts/surface-sweep.sh against a throwaway root
// and the fake asb/docker binaries.
type surfaceSweepHarness struct {
	t            *testing.T
	root         string
	scriptPath   string
	asbBinDir    string
	dockerBinDir string
	asbLog       string
	dockerLog    string
}

// newSurfaceSweepHarness stages a short root under /tmp and a fresh fake bin and
// log set. The root is short enough for the socket budget; t.TempDir() is not.
func newSurfaceSweepHarness(t *testing.T) *surfaceSweepHarness {
	t.Helper()
	// t.TempDir()'s path is too long for the root: surface-sweep derives a
	// sandbox control socket path under it that must fit sun_path.
	//nolint:usetesting // a short root is required for the socket budget
	root, err := os.MkdirTemp(surfaceSweepRootBase, "asb-sweep-")
	if err != nil {
		t.Fatalf("create short root: %v", err)
	}
	h := &surfaceSweepHarness{
		t:            t,
		root:         root,
		scriptPath:   repositoryScript(t, "surface-sweep.sh"),
		asbBinDir:    BinDir(t, "asb"),
		dockerBinDir: BinDir(t, "docker"),
		asbLog:       filepath.Join(t.TempDir(), "asb.log"),
		dockerLog:    filepath.Join(t.TempDir(), "docker.log"),
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
		_ = os.RemoveAll(root + ".lock")
	})
	return h
}

// run invokes the script with the harness root as its working directory.
func (h *surfaceSweepHarness) run(extra map[string]string, args ...string) ScriptResult {
	h.t.Helper()
	return h.runIn(h.root, extra, args...)
}

// runIn invokes the script in dir with the harness base environment. ASB_BIN and
// PATH reach the fake asb/docker; XDG_DATA_HOME/HOME sit under the root, away
// from the developer's real state.
func (h *surfaceSweepHarness) runIn(dir string, extra map[string]string, args ...string) ScriptResult {
	h.t.Helper()
	env := map[string]string{
		"ASB_SURFACE_ROOT": h.root,
		"ASB_BIN":          filepath.Join(h.asbBinDir, "asb"),
		"HOME":             filepath.Join(h.root, "home"),
		"XDG_DATA_HOME":    filepath.Join(h.root, "data"),
		"PATH": h.asbBinDir + string(os.PathListSeparator) + h.dockerBinDir +
			string(os.PathListSeparator) + envOption("PATH"),
		surfaceSweepAsbLogEnv:    h.asbLog,
		surfaceSweepDockerLogEnv: h.dockerLog,
	}
	maps.Copy(env, extra)
	return RunScript(h.t, dir, env, h.scriptPath, args...)
}

// surfaceSweepTree returns the sorted paths under dir, relative to it, so a case
// can prove a re-run created nothing.
func surfaceSweepTree(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	slices.Sort(paths)
	return paths
}

// surfaceSweepAssertNonzero fails when the script unexpectedly exited 0.
func surfaceSweepAssertNonzero(t *testing.T, label string, result ScriptResult) {
	t.Helper()
	if result.Status == 0 {
		t.Errorf("%s exited 0, want non-zero\nstdout:\n%s\nstderr:\n%s", label, result.Stdout, result.Stderr)
	}
}
