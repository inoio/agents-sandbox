//go:build msb

package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// runDockerStorageDriverSmoke drives the launcher's `shell` command in a
// docker-enabled VM and asserts the in-VM dockerd runs the fuse-overlayfs
// storage driver and can run and layered-build an image with it (#489). It is a
// sibling subtest of the harness smoke, sharing the suite's root, home, and
// cleanup (see TestHarnessSmoke).
//
// The disk saving that motivates fuse-overlayfs (its copy-on-write layers versus
// vfs's full copy per layer) is not asserted here: measuring it needs a second
// dockerd on the same backing and is inherently noisy. The figures measured for
// #489 are recorded on the issue.
func runDockerStorageDriverSmoke(t *testing.T, root, binary string) {
	t.Helper()

	dir := projectDir(root, dockerStorageProject)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	ses := mustStartUnderPTY(t, dir, isolatedEnv(root, root, os.Environ()),
		binary, "shell", "--agent", "opencode", "--docker", "--network", networkNone)
	defer ses.close()

	// The shell prints no startup marker, and its attach takes the terminal only
	// after the VM and its daemon are up. A single write can land before the
	// attach switches the terminal's mode and be lost, so the readiness command
	// is retried until it echoes back, the way the quit key is — instead of
	// waiting out a fixed settle. Every marker is assembled at runtime (printf)
	// so the pty's echoed command line never contains it: with the tty echoing
	// input, a literal marker would match the echo before the shell had run
	// anything.
	writeUntilMarker(t, ses, "printf '__SHELL%s__\\n' _READY\n", "__SHELL_READY__")
	writeAndAwaitMarker(t, ses,
		"echo \"__DRIVER=__$(docker info --format '{{.Driver}}')__END__\"; printf '__DRIVER%s__\\n' _DONE\n",
		"__DRIVER_DONE__")
	if !strings.Contains(markerText(string(ses.output())), "__DRIVER=__"+fuseDriverMarker+"__END__") {
		t.Fatalf("in-VM dockerd did not use %s; output:\n%s", fuseDriverMarker, stripANSI(ses.output()))
	}

	// A layered build and run that need no registry: scratch plus two COPY
	// layers, then run the tarball's statically linked dockerd for its version.
	script := strings.Join([]string{
		"mkdir -p /tmp/ctx",
		`printf 'FROM scratch\nCOPY dockerd /dockerd\nCOPY extra /extra\n' > /tmp/ctx/Dockerfile`,
		`cp "$(command -v dockerd)" /tmp/ctx/dockerd`,
		"touch /tmp/ctx/extra",
		"docker build -t driver-probe /tmp/ctx",
		"docker run --rm driver-probe /dockerd --version",
		"printf '__BUILD%s__\\n' _DONE",
	}, "; ")
	writeAndAwaitMarker(t, ses, script+"\n", "__BUILD_DONE__")
	output := stripANSI(ses.output())
	if !strings.Contains(output, "Docker version") {
		t.Fatalf("layered docker build/run did not report a version; output:\n%s", output)
	}
	if strings.Contains(output, "ERROR:") {
		t.Fatalf("layered docker build/run reported an error; output:\n%s", output)
	}

	if err := ses.write([]byte("exit\n")); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	if code, err := ses.waitExit(quitTimeout); err != nil {
		t.Fatalf("shell did not exit: %v\n%s", err, output)
	} else if code != 0 {
		t.Fatalf("shell exited %d, want 0\n%s", code, output)
	}
}

// dockerStorageProject is the throwaway project directory the docker-storage
// subtest runs from, fixing the slug its runner image is keyed by.
const dockerStorageProject = "docker-storage"

// fuseDriverMarker is the driver name dockerd must report in a VM whose runner
// image carries fuse-overlayfs and a usable /dev/fuse.
const fuseDriverMarker = "fuse-overlayfs"

var (
	prebuildDockerOnce sync.Once
	errPrebuildDocker  error
)

// prebuildDockerStorageImage builds the docker-enabled opencode runner image in
// the docker-storage project's directory once, so the docker-storage subtest
// only pays its VM start rather than a cold image build. It runs in the suite's
// serial prebuild phase, after the docker preflight.
func prebuildDockerStorageImage(t *testing.T, root string) {
	t.Helper()

	prebuildDockerOnce.Do(func() {
		dir := projectDir(root, dockerStorageProject)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			errPrebuildDocker = fmt.Errorf("create docker-storage project dir: %w", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
		defer cancel()
		build := exec.CommandContext(ctx, smokeBinary, "build", "--agent", "opencode", "--docker")
		build.Dir = dir
		build.Env = isolatedEnv(root, root, os.Environ())
		if out, err := build.CombinedOutput(); err != nil {
			errPrebuildDocker = fmt.Errorf("prebuild docker runner image: %w\n%s", err, out)
		}
	})
	if errPrebuildDocker != nil {
		t.Fatalf("prebuild docker runner image: %v", errPrebuildDocker)
	}
}

// writeUntilMarker writes command to the session on an interval until marker
// appears, failing if the session exits or a ceiling elapses first. It is the
// readiness signal for a session with no startup marker — the shell — where one
// write can be lost to the attach's terminal-mode switch, so it is retried the
// way the quit key is. command is assembled at runtime so its echo never
// contains marker.
func writeUntilMarker(t *testing.T, ses *ptySession, command, marker string) {
	t.Helper()

	deadline := time.Now().Add(tuiReadyTimeout)
	needle := markerText(marker)
	for {
		if strings.Contains(markerText(string(ses.output())), needle) {
			return
		}
		if ses.hasExited() {
			t.Fatalf("session exited before %q appeared:\n%s", marker, stripANSI(ses.output()))
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%q never appeared within %s:\n%s", marker, tuiReadyTimeout, stripANSI(ses.output()))
		}
		if err := ses.write([]byte(command)); err != nil {
			t.Fatalf("write %q: %v", command, err)
		}
		time.Sleep(quitPressInterval)
	}
}

// writeAndAwaitMarker writes command to the session once and blocks until marker
// appears in its output, failing the test if the shell exits first or the marker
// never arrives.
func writeAndAwaitMarker(t *testing.T, ses *ptySession, command, marker string) {
	t.Helper()

	if err := ses.write([]byte(command)); err != nil {
		t.Fatalf("write %q: %v", command, err)
	}
	if err := ses.waitForMarker(marker, tuiReadyTimeout); err != nil {
		t.Fatalf("await %q: %v\n%s", marker, err, stripANSI(ses.output()))
	}
}
