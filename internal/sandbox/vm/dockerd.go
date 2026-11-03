package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	dockerdBinaryCheckCmd = "command -v dockerd >/dev/null 2>&1"
	dockerdReadyCmd       = "docker info"

	// dockerdBinary is the dockerd executable, launched as a runtime-owned
	// detached job so the launcher can inspect and kill the process by handle.
	dockerdBinary = "dockerd"
	// dockerdSocketFlag/dockerdSocket pin dockerd's unix socket, the one clients
	// inside the VM and the launcher's readiness probe use.
	dockerdSocketFlag = "-H"
	dockerdSocket     = "unix:///var/run/docker.sock"

	// dockerdDaemonConfigPath is dockerd's default configuration file. The
	// launcher reads it (see guestStorageDriver) so it never forces
	// storage-driver both in the file and on the command line, which dockerd
	// rejects (inoio/agents-sandbox#287).
	dockerdDaemonConfigPath = "/etc/docker/daemon.json"
	vfsStorageDriver        = "vfs"

	// fuseOverlayfsStorageDriver is the copy-on-write driver the launcher
	// prefers when the guest provides it. The kernel rejects overlay2 here (an
	// overlay upper on the guest overlay, or on virtiofs), but fuse-overlayfs
	// merges in userspace and exposes its own FUSE mount, so neither
	// restriction applies. The tarball's dockerd compiles the driver in.
	fuseOverlayfsStorageDriver = "fuse-overlayfs"

	// fuseOverlayfsProbeCmd reports whether the runner image provides the
	// fuse-overlayfs binary and a usable /dev/fuse, the two things a rootful
	// fuse-overlayfs dockerd needs. It runs as root, the user dockerd runs as,
	// so a root-only device is accepted.
	fuseOverlayfsProbeCmd = "command -v fuse-overlayfs >/dev/null 2>&1 && " +
		"[ -c /dev/fuse ] && [ -r /dev/fuse ] && [ -w /dev/fuse ]"

	// dockerdCleanupCmd removes containerd/pid/socket residue left by a killed
	// dockerd job. Killing the job tears down dockerd but not necessarily its
	// containerd child, so the residue is cleared explicitly before a restart.
	dockerdCleanupCmd = "pkill containerd >/dev/null 2>&1 || true; " +
		"find /run /var/run -iname 'docker*.pid' -delete >/dev/null 2>&1 || true; " +
		"find /run /var/run \\( -iname '*containerd*.sock' -o -iname '*containerd*.pid' \\) -delete >/dev/null 2>&1 || true"
)

// The readiness window must exceed dockerd's 15s containerd start timeout, so a
// restart has time to come up before the poll gives up. Tests shorten the vars.
const (
	dockerdReadyTimeoutDefault = 30 * time.Second
	dockerdPollIntervalDefault = time.Second
)

var (
	dockerdReadyTimeout = dockerdReadyTimeoutDefault
	dockerdPollInterval = dockerdPollIntervalDefault
)

// dockerdArgs builds dockerd's argument list for this VM and returns the
// storage driver it selected. The microsandbox filesystem cannot back overlay2,
// so the launcher asks for a driver the guest supports: fuse-overlayfs when its
// binary and /dev/fuse are present, vfs otherwise. The driver is requested with
// --storage-driver unless the image's daemon.json already sets it, in which
// case the flag is dropped because dockerd refuses a directive set both as a
// flag and in the configuration file (#287). A daemon.json requesting any
// driver other than those two is a hard error rather than a silent override or
// a flag/file conflict.
func dockerdArgs(ctx context.Context, sb msb.Sandbox) ([]string, string, error) {
	driver, err := guestStorageDriver(ctx, sb)
	if err != nil {
		return nil, "", err
	}
	requested := driver == ""
	if requested {
		driver = defaultGuestStorageDriver(ctx, sb)
	}
	switch driver {
	case fuseOverlayfsStorageDriver, vfsStorageDriver:
	default:
		return nil, "", fmt.Errorf(
			"%s sets storage-driver=%q, but the microsandbox filesystem supports only %q or %q; "+
				"remove the storage-driver entry or set it to one of them",
			dockerdDaemonConfigPath, driver, fuseOverlayfsStorageDriver, vfsStorageDriver,
		)
	}
	args := []string{dockerdSocketFlag, dockerdSocket}
	if requested {
		args = append(args, "--storage-driver="+driver)
	}
	return args, driver, nil
}

// defaultGuestStorageDriver returns the driver to request when the image's
// daemon.json sets none: fuse-overlayfs, whose copy-on-write layers cut both
// build time and data-root disk use, when the guest provides the binary and a
// usable /dev/fuse, and vfs otherwise.
func defaultGuestStorageDriver(ctx context.Context, sb msb.Sandbox) string {
	if guestSupportsFuseOverlayfs(ctx, sb) {
		return fuseOverlayfsStorageDriver
	}
	return vfsStorageDriver
}

// guestSupportsFuseOverlayfs probes the guest for the fuse-overlayfs binary and
// a usable /dev/fuse. A probe failure is treated as unsupported so a transport
// hiccup falls back to vfs rather than failing the VM start.
func guestSupportsFuseOverlayfs(ctx context.Context, sb msb.Sandbox) bool {
	out, err := sb.Shell(ctx, fuseOverlayfsProbeCmd, msbSdk.WithExecUser(RootUser))
	if err != nil {
		return false
	}
	return out.Success()
}

// guestStorageDriver returns the storage-driver requested by the image's
// daemon.json. It returns "" when the file is absent, sets no storage-driver,
// or is not parseable as JSON, in which case dockerd reports the parse error
// itself.
func guestStorageDriver(ctx context.Context, sb msb.Sandbox) (string, error) {
	exists, err := sb.FS().Exists(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dockerdDaemonConfigPath, err)
	}
	if !exists {
		return "", nil
	}
	raw, err := sb.FS().Read(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dockerdDaemonConfigPath, err)
	}
	var config map[string]json.RawMessage
	// json.Unmarshal pre-validates the whole document before decoding into the
	// map, and a map of RawMessage cannot raise a type error, so an unparseable
	// document always leaves config empty; the guard is therefore equivalent to
	// falling through to the missing-key check below, which returns "" for the
	// same reason. dockerd reports the parse error itself.
	// gomutants:disable-next-line BRANCH_IF reason="an unparsable document leaves config empty here (syntax is pre-validated, RawMessage cannot type-error), and the fall-through's missing-key check returns the same \"\", nil"
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", nil //nolint:nilerr // malformed daemon.json is left for dockerd to reject
	}
	rawDriver, ok := config["storage-driver"]
	if !ok {
		return "", nil
	}
	var driver string
	if err := json.Unmarshal(rawDriver, &driver); err != nil {
		return "", fmt.Errorf("%s has a non-string storage-driver value", dockerdDaemonConfigPath)
	}
	return driver, nil
}

// startDockerdIfPresent starts dockerd inside the VM as a runtime-owned
// detached job if the docker image is in use, or does nothing otherwise. It is
// safe to call on every VM bootstrap because it checks for the dockerd binary
// and handles the case where dockerd is already running.
func startDockerdIfPresent(ctx context.Context, sb msb.Sandbox, ui termio.UI) error {
	out, checkErr := sb.Shell(ctx, dockerdBinaryCheckCmd, msbSdk.WithExecUser(RootUser))
	if checkErr != nil {
		return fmt.Errorf("while checking dockerd binary: %w", checkErr)
	}
	if !out.Success() {
		ui.Verbosef("/usr/bin/dockerd not present, skipping Docker startup")
		return nil
	}

	if healthy, _ := probeHealthy(ctx, sb, dockerdReadyCmd); healthy {
		ui.Verbose("using already running dockerd")
		return nil
	}

	args, driver, err := dockerdArgs(ctx, sb)
	if err != nil {
		return err
	}

	ui.Verbosef("starting dockerd with %s storage driver", driver)
	// Kill a stale dockerd job before starting a fresh one so a previous launch
	// never keeps the socket bound, then clear the containerd/pid/socket
	// residue the killed process leaves behind. Both are best-effort: a failure
	// is reported and the readiness probe below decides.
	helper := dockerdHelper()
	killJobs(ctx, sb, helper, ui)
	if _, cleanupErr := sb.Shell(ctx, dockerdCleanupCmd, msbSdk.WithExecUser(RootUser)); cleanupErr != nil {
		ui.Verbosef("dockerd residue cleanup failed (continuing): %v", cleanupErr)
	}

	binary, commandArgs := helper.markedCommand(args)
	job, startErr := sb.ExecDetached(ctx, binary, commandArgs, msbSdk.WithExecUser(RootUser))
	if startErr != nil {
		return fmt.Errorf("start dockerd: %w", startErr)
	}
	defer func() {
		if closeErr := job.Close(); closeErr != nil {
			ui.Verbosef("closing dockerd job handle failed: %v", closeErr)
		}
	}()

	probe := func(probeCtx context.Context) (bool, string) {
		return probeHealthy(probeCtx, sb, dockerdReadyCmd)
	}
	onUnhealthy := func(detail string) {
		ui.Verbosef("dockerd not ready: %s", detail)
	}
	if readyErr := awaitJobReady(
		ctx,
		job,
		"dockerd",
		probe,
		onUnhealthy,
		dockerdReadyTimeout,
		dockerdPollInterval,
	); readyErr != nil {
		return readyErr
	}
	ui.Verbosef("dockerd is ready")
	return nil
}
