//go:build msb

package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDockerStorageDriverIsFuseOverlayfs drives the launcher's `shell` command
// in a docker-enabled VM and asserts the in-VM dockerd runs the fuse-overlayfs
// storage driver and can run and layered-build an image with it (#489).
//
// It is host-only, the same tier as TestHarnessSmoke (`make test-msb`): it needs
// KVM, a docker daemon, and the network to pull the base image. It builds its own
// root and launcher rather than sharing TestHarnessSmoke's, whose root is removed
// when that test ends.
//
// The disk saving that motivates fuse-overlayfs (its copy-on-write layers versus
// vfs's full copy per layer) is not asserted here: measuring it needs a second
// dockerd on the same backing and is inherently noisy. See the figures in
// `.operator-shared/guides/docker-storage-driver-in-vm.md`.
func TestDockerStorageDriverIsFuseOverlayfs(t *testing.T) {
	preflight(t)
	root, binary := prepareIsolated(t)
	preflightDocker(t, root)
	defer cleanupSandboxes(t, root, binary)

	dir := projectDir(root, "docker-storage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	buildDockerImage(t, root, binary, dir)

	ses := mustStartUnderPTY(t, dir, isolatedEnv(root, os.Environ()),
		binary, "shell", "--agent", "opencode", "--docker", "--network", networkNone)
	defer ses.close()

	// Turn off the tty's input echo first: with it on, the sentinels below would
	// match the echoed command line the moment it is written, before the shell
	// has run anything.
	if err := ses.write([]byte("stty -echo\n")); err != nil {
		t.Fatalf("write stty: %v", err)
	}

	writeAndAwaitMarker(t, ses,
		"echo \"__DRIVER=__$(docker info --format '{{.Driver}}')__END__\"; echo __DRIVER_DONE__\n",
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
		"echo __BUILD_DONE__",
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

// fuseDriverMarker is the driver name dockerd must report in a VM whose runner
// image carries fuse-overlayfs and a usable /dev/fuse.
const fuseDriverMarker = "fuse-overlayfs"

// prepareIsolated creates a fresh suite root and builds the launcher into it,
// mirroring prepare/countingSandbox but scoped to one test. It exists so the
// docker-storage test does not share TestHarnessSmoke's root, which that test's
// cleanup removes while this one may still be running.
func prepareIsolated(t *testing.T) (root, binary string) {
	t.Helper()

	root, err := os.MkdirTemp(smokeRootBase, smokeRootPrefix)
	if err != nil {
		t.Fatalf("create temp root: %v", err)
	}
	t.Cleanup(func() { removeTempRoot(t, root) })
	// A short, fixed root keeps microsandbox's derived control-socket path under
	// the sun_path limit; see createRootAndBuildBinary.
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	binary = filepath.Join(root, "agents-sandbox")
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, launcherPackage)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	return root, binary
}

// buildDockerImage builds the docker-enabled runner image for the test project
// directory, so the shell's `run` loads it instead of building it.
func buildDockerImage(t *testing.T, root, binary, dir string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	build := exec.CommandContext(ctx, binary, "build", "--agent", "opencode", "--docker")
	build.Dir = dir
	build.Env = isolatedEnv(root, os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build docker runner image: %v\n%s", err, out)
	}
}

// writeAndAwaitMarker writes command to the session and blocks until marker
// appears in its output, failing the test if the shell exits first or the
// marker never arrives.
func writeAndAwaitMarker(t *testing.T, ses *ptySession, command, marker string) {
	t.Helper()

	if err := ses.write([]byte(command)); err != nil {
		t.Fatalf("write %q: %v", command, err)
	}
	if err := ses.waitForMarker(marker, tuiReadyTimeout); err != nil {
		t.Fatalf("await %q: %v\n%s", marker, err, stripANSI(ses.output()))
	}
}
