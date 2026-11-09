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
// storage driver and can run and layered-build an image with it (#489), and
// that it runs rootless so a container cannot reach VM root (#487). It is a
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

	assertRootlessEngine(t, ses)

	if err := ses.write([]byte("exit\n")); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	if code, err := ses.waitExit(quitTimeout); err != nil {
		t.Fatalf("shell did not exit: %v\n%s", err, output)
	} else if code != 0 {
		t.Fatalf("shell exited %d, want 0\n%s", code, output)
	}
}

// assertRootlessEngine asserts the in-VM engine runs rootless and that a
// container cannot reach VM root through it (#487): a host bind mount cannot
// read the root-only /etc/shadow, a privileged host-pid container cannot enter
// PID 1's namespaces, and a privileged container runs in a non-initial user
// namespace, where the kernel never grants module loading (CAP_SYS_MODULE only
// counts in the initial user namespace). The probe image is assembled from the VM's own cat
// and nsenter plus their libraries, because the smoke VM has no registry
// access; a positive control proves the image runs, so a broken probe cannot
// pass as a blocked escape.
func assertRootlessEngine(t *testing.T, ses *ptySession) {
	t.Helper()

	writeAndAwaitMarker(
		t,
		ses,
		"echo \"__SECURITY=__$(docker info --format '{{.SecurityOptions}}')__END__\"; printf '__SECURITY%s__\\n' _DONE\n",
		"__SECURITY_DONE__",
	)
	if !strings.Contains(markerText(string(ses.output())), "name=rootless") {
		t.Fatalf("in-VM dockerd is not rootless; output:\n%s", stripANSI(ses.output()))
	}

	script := strings.Join([]string{
		"mkdir -p /tmp/escape",
		`for f in /usr/bin/cat /usr/bin/nsenter $(ldd /usr/bin/cat /usr/bin/nsenter | awk '/\//{print $(NF-1)}'); do cp --parents "$f" /tmp/escape; done`,
		`printf 'FROM scratch\nCOPY . /\n' > /tmp/escape/Dockerfile`,
		"docker build -q -t escape-probe /tmp/escape >/dev/null",
		`docker run --rm escape-probe cat /etc/hostname >/dev/null; echo "__CONTROL_RC=__$?__"`,
		`docker run --rm -v /:/host escape-probe cat /host/etc/shadow >/dev/null; echo "__BIND_RC=__$?__"`,
		`docker run --rm --privileged --pid=host escape-probe nsenter -t 1 -m -u -i -n -p cat /etc/shadow >/dev/null; echo "__NSENTER_RC=__$?__"`,
		`echo "__UIDMAP=__$(docker run --rm --privileged escape-probe cat /proc/self/uid_map | awk '{printf "%s:%s:%s;", $1, $2, $3}')__END__"`,
		"printf '__ESCAPE%s__\\n' _DONE",
	}, "; ")
	writeAndAwaitMarker(t, ses, script+"\n", "__ESCAPE_DONE__")
	output := markerText(string(ses.output()))
	if !strings.Contains(output, "__CONTROL_RC=__0__") {
		t.Fatalf("the escape probe image does not run; output:\n%s", stripANSI(ses.output()))
	}
	if !strings.Contains(output, "__UIDMAP=__0:1000:1;") {
		t.Errorf("a privileged container must map its root to dev in a non-initial user namespace; output:\n%s",
			stripANSI(ses.output()))
	}
	for _, escape := range []string{"__BIND_RC=__", "__NSENTER_RC=__"} {
		if strings.Contains(output, escape+"0__") || !strings.Contains(output, escape) {
			t.Errorf("a container reached VM root (%s); output:\n%s", escape, stripANSI(ses.output()))
		}
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
