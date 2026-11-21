package scripts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/hostfigures/hostfigurestest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestEnvelope ports every case of the bash harness scripts/test-envelope_test.sh,
// driving scripts/hermetic-envelope.sh with throwaway fixture modules and asserting
// the same exit codes and stdout/stderr substrings. The envelope never touches
// this repository: every case that copies a source passes an explicit --source
// under a temp root (the print and help cases deliberately read the default).
func TestEnvelope(t *testing.T) {
	t.Parallel()
	t.Run("scrubbed run", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))
		work := filepath.Join(root, "work")

		// GOFLAGS is cleared so the exact `GOFLAGS=-count=1` assertion below does
		// not depend on the caller's environment; HOST_ONLY_VAR models an ambient
		// variable the scrub must drop.
		result := runEnvelope(t, root, map[string]string{
			"GOFLAGS":       "",
			"HOST_ONLY_VAR": "leak",
		}, "--source", fixture, "--work-dir", work, "--", "make", "test")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "PWD="+filepath.Join(work, "module"))
		assertContains(t, "stdout", result.Stdout, "HOME="+filepath.Join(work, "home"))
		assertContains(t, "stdout", result.Stdout, "TMPDIR="+filepath.Join(work, "tmp"))
		assertNotContains(t, "stdout", result.Stdout, "leak")

		// The source tree keeps exactly the fixture files: nothing the run wrote
		// (its marker, the copy, HOME/TMPDIR scratch) reached into it.
		assertEnvelopeFixtureIntact(t, fixture)

		// No test-result cache can stand in for a real run.
		assertContains(t, "stdout", result.Stdout, "GOFLAGS=-count=1")
	})

	t.Run("forced -count=1 is last", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		// An inherited -count must lose to the forced one, so the flag has to be
		// the last token of GOFLAGS on the printed env line.
		result := runEnvelope(t, root, map[string]string{"GOFLAGS": "-count=3"},
			"--source", fixture, "--print")

		assertStatus(t, result, 0)
		goflagsLine := envelopeEnvLine(result.Stdout, "GOFLAGS=")
		assertContains(t, "GOFLAGS", goflagsLine, "-count=1")
		if !strings.HasSuffix(goflagsLine, "-count=1") {
			t.Errorf("forced -count=1 is not last: %q", goflagsLine)
		}
	})

	t.Run("pins GOTOOLCHAIN=local", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		// An inherited GOTOOLCHAIN must lose to the pinned one, so a toolchain
		// directive cannot trigger a download inside the network namespace.
		result := runEnvelope(t, root, map[string]string{"GOTOOLCHAIN": "auto"},
			"--source", fixture, "--print")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "env GOTOOLCHAIN=local")
	})

	t.Run("print shows the plan and runs nothing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		result := runEnvelope(t, root, nil, "--source", fixture, "--print")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "command: make test")
		assertNotContains(t, "stdout", result.Stdout, "MARKER")

		// The cleanup trap is installed before the print branch, so a preview
		// does not leak its temp work dir.
		workDir := envelopeWorkDir(result.Stdout)
		if workDir == "" {
			t.Fatal("print output has no work_dir line")
		}
		if _, err := os.Stat(workDir); !os.IsNotExist(err) {
			t.Errorf("print left its work dir %s (stat err: %v)", workDir, err)
		}
	})

	t.Run("print shows the default source", func(t *testing.T) {
		t.Parallel()
		result := runEnvelope(t, t.TempDir(), nil, "--print")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "source_dir="+envelopeRepositoryRoot(t))
	})

	t.Run("help", func(t *testing.T) {
		t.Parallel()
		result := runEnvelope(t, t.TempDir(), nil, "--help")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "Usage:")
		// The usage extraction must not drop the default-command line.
		assertContains(t, "stdout", result.Stdout, "With no `-- COMMAND...`, the command is `make test`.")
	})

	t.Run("keep preserves the work dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))
		keepWork := filepath.Join(root, "keep-work")

		result := runEnvelope(t, root, nil,
			"--source", fixture, "--work-dir", keepWork, "--keep", "--", "make", "test")

		assertStatus(t, result, 0)
		if info, err := os.Stat(keepWork); err != nil || !info.IsDir() {
			t.Errorf("--keep did not preserve the work dir %s (stat: %v)", keepWork, err)
		}
	})

	t.Run("spaced source path", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "with space"))
		spacedWork := filepath.Join(root, "space work")

		result := runEnvelope(t, root, nil,
			"--source", fixture, "--work-dir", spacedWork, "--", "make", "test")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "PWD="+filepath.Join(spacedWork, "module"))
	})

	t.Run("existing work dir refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))
		existing := filepath.Join(root, "existing")
		if err := os.MkdirAll(existing, 0o750); err != nil {
			t.Fatalf("create existing work dir: %v", err)
		}
		testutil.WriteFile(t, existing, "keepme", "")

		result := runEnvelope(t, root, nil,
			"--source", fixture, "--work-dir", existing, "--", "make", "test")

		if result.Status == 0 {
			t.Errorf("existing work dir accepted, want a non-zero exit\nstdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		}
		if _, err := os.Stat(filepath.Join(existing, "keepme")); err != nil {
			t.Errorf("existing work dir not left intact: %v", err)
		}
	})

	t.Run("work dir inside source refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		result := runEnvelope(t, root, nil,
			"--source", fixture, "--work-dir", filepath.Join(fixture, "inside"), "--", "make", "test")

		if result.Status == 0 {
			t.Errorf("work dir inside source accepted, want a non-zero exit\nstdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		}
	})

	t.Run("symlinked work dir inside source refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))
		target := filepath.Join(fixture, "inside-link-target")
		if err := os.MkdirAll(target, 0o750); err != nil {
			t.Fatalf("create link target: %v", err)
		}
		sneaky := filepath.Join(root, "sneaky-link")
		if err := os.Symlink(target, sneaky); err != nil {
			t.Fatalf("create symlink: %v", err)
		}

		result := runEnvelope(t, root, nil,
			"--source", fixture, "--work-dir", sneaky, "--", "make", "test")

		if result.Status == 0 {
			t.Errorf("symlinked work dir inside source accepted, want a non-zero exit\nstdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		}
	})

	t.Run("TMPDIR work dir inside source refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))
		insideTmp := filepath.Join(fixture, "host-tmp")
		if err := os.MkdirAll(insideTmp, 0o750); err != nil {
			t.Fatalf("create inside TMPDIR: %v", err)
		}

		// No --work-dir: the default comes from TMPDIR, which points inside the
		// source, so the run must refuse before copying anything.
		result := runEnvelope(t, root, map[string]string{"TMPDIR": insideTmp},
			"--source", fixture, "--", "make", "test")

		if result.Status == 0 {
			t.Errorf("TMPDIR work dir inside source accepted, want a non-zero exit\nstdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		}
		// The stray temp dir the tool created before it resolved the path is
		// cleaned up, so the TMPDIR itself is left empty.
		entries, err := os.ReadDir(insideTmp)
		if err != nil {
			t.Fatalf("read inside TMPDIR: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("rejected temp dir not cleaned up: %v", envelopeEntryNames(entries))
		}
	})

	t.Run("netns rejected off Linux", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		// A uname shim forces the branch so it runs on a capable Linux host too,
		// rather than self-skipping exactly where the strict run is meant to work.
		shim := filepath.Join(root, "shim")
		if err := os.MkdirAll(shim, 0o750); err != nil {
			t.Fatalf("create shim dir: %v", err)
		}
		shimUname := filepath.Join(shim, "uname")
		if err := os.WriteFile(shimUname, []byte("#!/bin/sh\necho Darwin\n"), 0o600); err != nil {
			t.Fatalf("write uname shim: %v", err)
		}
		if err := os.Chmod(shimUname, 0o755); err != nil {
			t.Fatalf("make uname shim executable: %v", err)
		}

		result := runEnvelope(t, root, map[string]string{
			"PATH": shim + string(os.PathListSeparator) + envelopePath(t),
		}, "--source", fixture, "--netns", "--print")

		if result.Status == 0 {
			t.Errorf("netns accepted off Linux, want a non-zero exit\nstdout:\n%s\nstderr:\n%s",
				result.Stdout, result.Stderr)
		}
		assertContains(t, "stderr", result.Stderr, "Darwin")
	})

	t.Run("netns wraps the command", func(t *testing.T) {
		t.Parallel()
		if !envelopeNetnsCapable(t) {
			t.Skip("no netns on this host")
		}
		root := t.TempDir()
		fixture := envelopeFixture(t, filepath.Join(root, "base"))

		result := runEnvelope(t, root, nil, "--source", fixture, "--netns", "--print")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "netns: yes")
		assertContains(t, "stdout", result.Stdout, "unshare --net")
		assertContains(t, "stdout", result.Stdout, "ip link set lo up && exec")
	})

	t.Run("netns blocks egress", func(t *testing.T) {
		t.Parallel()
		egressIP := envelopeResolveEgressIP(t)
		if !envelopeNetnsCapable(t) || !envelopeUnshareNetCapable(t) || !envelopeHasTool("timeout") ||
			egressIP == "" || !envelopeHostReaches(t, egressIP) {
			t.Skip("no netns-capable unshare/ip or host egress")
		}
		root := t.TempDir()
		// Probe a numeric address, never a name: name resolution can reach a
		// resolver over a pathname Unix socket, which a network namespace does
		// not isolate. The fixture echoes a start marker so a setup failure is
		// distinguished from a denied connect.
		egressFixture := envelopeFixture(t, filepath.Join(root, "egress"))
		writeEnvelopeCheck(t, egressFixture,
			"echo \"REACHED_NET_CHECK\"\ntimeout 10 bash -c 'exec 3<>/dev/tcp/"+egressIP+"/443'\n")

		result := runEnvelope(t, root, nil,
			"--source", egressFixture, "--work-dir", filepath.Join(root, "netns-work"),
			"--netns", "--", "make", "test")

		assertContains(t, "stdout", result.Stdout, "REACHED_NET_CHECK")
		if result.Status == 0 {
			t.Errorf("netns did not block egress (unexpected success)\nstdout:\n%s", result.Stdout)
		}
	})

	t.Run("loopback is up inside the namespace", func(t *testing.T) {
		t.Parallel()
		if !envelopeNetnsCapable(t) || !envelopeUnshareNetCapable(t) {
			t.Skip("no netns-capable unshare/ip")
		}
		root := t.TempDir()
		loopbackFixture := envelopeFixture(t, filepath.Join(root, "loopback"))
		writeEnvelopeCheck(t, loopbackFixture,
			"ip -o link show lo | grep 'UP' >/dev/null && echo \"LOOPBACK_UP\"\n")

		result := runEnvelope(t, root, nil,
			"--source", loopbackFixture, "--work-dir", filepath.Join(root, "loopback-work"),
			"--netns", "--", "make", "test")

		assertStatus(t, result, 0)
		assertContains(t, "stdout", result.Stdout, "LOOPBACK_UP")
	})
}

// TestEnvelopeSentinelEnv pins that the sentinel env var reaches the child only
// when --sentinel is passed: the sentinel pass pins the host-figure seams, and a
// scrubbed run must not inherit the pin.
func TestEnvelopeSentinelEnv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fixture := envelopeFixture(t, filepath.Join(root, "base"))

	plain := runEnvelope(t, root, nil, "--source", fixture, "--print")
	assertStatus(t, plain, 0)
	if strings.Contains(plain.Stdout, hostfigurestest.SentinelEnv) {
		t.Errorf("plain print names the sentinel env:\n%s", plain.Stdout)
	}

	pinned := runEnvelope(t, root, nil, "--source", fixture, "--sentinel", "--print")
	assertStatus(t, pinned, 0)
	assertContains(t, "stdout", pinned.Stdout, "env "+hostfigurestest.SentinelEnv+"=1")
}

// envelopeFixture writes the bash harness's new_fixture module: a Makefile whose
// test target runs check.sh, and a check.sh that reports the scrubbed
// environment and writes a marker into its working directory. It returns the
// fixture directory.
func envelopeFixture(tb testing.TB, dir string) string {
	tb.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		tb.Fatalf("create fixture %s: %v", dir, err)
	}
	testutil.WriteFile(tb, dir, "Makefile", "test:\n\t@sh check.sh\n")
	writeEnvelopeCheck(tb, dir, strings.Join([]string{
		`test -n "${HOME:-}" && echo "HOME=$HOME"`,
		`echo "PWD=$PWD"`,
		`echo "TMPDIR=${TMPDIR:-}"`,
		`echo "GOFLAGS=${GOFLAGS:-}"`,
		`echo "HOST_ONLY_VAR=${HOST_ONLY_VAR:-}"`,
		`: > marker`,
		`echo "MARKER"`,
		"",
	}, "\n"))
	return dir
}

// writeEnvelopeCheck replaces the fixture's check.sh with content.
func writeEnvelopeCheck(tb testing.TB, dir, content string) {
	tb.Helper()
	testutil.WriteFile(tb, dir, "check.sh", content)
}

// runEnvelope runs scripts/hermetic-envelope.sh from dir with extraEnv overlaid on
// the harness environment.
func runEnvelope(t *testing.T, dir string, extraEnv map[string]string, args ...string) ScriptResult {
	t.Helper()
	return RunScript(t, dir, extraEnv, repositoryScript(t, "hermetic-envelope.sh"), args...)
}

// assertEnvelopeFixtureIntact fails when the fixture directory holds anything
// beyond the two files envelopeFixture wrote: a run must not reach into it.
func assertEnvelopeFixtureIntact(t *testing.T, fixture string) {
	t.Helper()
	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	for _, entry := range entries {
		if entry.Name() != "Makefile" && entry.Name() != "check.sh" {
			t.Errorf("source tree touched: %s left in %s", entry.Name(), fixture)
		}
	}
}

// envelopeEnvLine returns the printed `env KEY=...` value for keyPrefix, or "".
func envelopeEnvLine(printed, keyPrefix string) string {
	for line := range strings.SplitSeq(printed, "\n") {
		if rest, ok := strings.CutPrefix(line, "env "+keyPrefix); ok {
			return keyPrefix + rest
		}
	}
	return ""
}

// envelopeWorkDir returns the printed work_dir value, or "".
func envelopeWorkDir(printed string) string {
	for line := range strings.SplitSeq(printed, "\n") {
		if rest, ok := strings.CutPrefix(line, "work_dir="); ok {
			return rest
		}
	}
	return ""
}

// envelopeEntryNames returns the names of dir entries.
func envelopeEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// envelopeRepositoryRoot returns the repository root as the tool resolves it:
// physically, so a symlinked checkout still matches the tool's `pwd -P`.
func envelopeRepositoryRoot(tb testing.TB) string {
	tb.Helper()
	root, err := filepath.EvalSymlinks(repositoryRoot(tb))
	if err != nil {
		tb.Fatalf("resolve repository root: %v", err)
	}
	return root
}

// envelopePath returns the harness's PATH, which the shim case prepends to.
func envelopePath(tb testing.TB) string {
	tb.Helper()
	return envOption("PATH")
}

// envelopeNetnsCapable mirrors the bash harness's netns_capable: a Linux host
// with unshare and ip on PATH.
func envelopeNetnsCapable(tb testing.TB) bool {
	tb.Helper()
	return runtime.GOOS == "linux" && envelopeHasTool("unshare") && envelopeHasTool("ip")
}

// envelopeUnshareNetCapable mirrors the bash guard `unshare --net
// --map-root-user true`: it succeeds only where unprivileged network namespaces
// are permitted.
func envelopeUnshareNetCapable(tb testing.TB) bool {
	tb.Helper()
	if !envelopeHasTool("unshare") {
		return false
	}
	command := exec.Command("unshare", "--net", "--map-root-user", "true")
	return command.Run() == nil
}

// envelopeHasTool reports whether name is on PATH.
func envelopeHasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// envelopeResolveEgressIP returns the first IPv4 address example.com resolves
// to, bounded by the same 10-second budget the bash harness's
// `timeout 10 getent ahostsv4` imposed, or "".
func envelopeResolveEgressIP(tb testing.TB) string {
	tb.Helper()
	if !envelopeHasTool("getent") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "getent", "ahostsv4", "example.com")
	var stdout strings.Builder
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return ""
	}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// envelopeHostReaches reports whether the host itself can open a TCP connect to
// ip on port 443, so a host with no working network does not pass trivially.
func envelopeHostReaches(tb testing.TB, ip string) bool {
	tb.Helper()
	if !envelopeHasTool("timeout") {
		return false
	}
	command := exec.Command("timeout", "5", "bash", "-c", "exec 3<>/dev/tcp/"+ip+"/443")
	return command.Run() == nil
}
