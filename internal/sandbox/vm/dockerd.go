package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	dockerdBinaryCheckCmd = "command -v dockerd >/dev/null 2>&1"
	dockerdReadyCmd       = "docker info"

	// dockerdBinary is the engine executable whose presence enables Docker in
	// the VM.
	dockerdBinary = "dockerd"
	// dockerdRootlessBinary is the rootless entrypoint the engine is launched
	// through as a runtime-owned detached job: it starts rootlesskit, which runs
	// dockerd as dev inside dev's own user namespace.
	dockerdRootlessBinary   = "dockerd-rootless.sh"
	dockerdRootlessCheckCmd = "command -v " + dockerdRootlessBinary + " >/dev/null 2>&1"

	// dockerdRuntimeDir is the engine's XDG_RUNTIME_DIR: it holds the socket,
	// rootlesskit's state, and pid files. It is a fixed dev-owned path because
	// the guest has no logind to provide /run/user/<uid>, and dev's uid is the
	// base image's choice.
	dockerdRuntimeDir = "/run/agents-sandbox/docker"
	// dockerdDataRoot keeps the engine data on the VM root disk, with the same
	// lifecycle as the rootful /var/lib/docker it replaces, instead of the
	// rootless default under the home volume.
	dockerdDataRoot = "/var/lib/agents-sandbox/docker"
	// dockerdDefaultSocket is where clients look when DOCKER_HOST is unset; it
	// links to the rootless socket so they need no configuration.
	dockerdDefaultSocket = "/var/run/docker.sock"

	// dockerdRootlessPrepareCmd sets up, as root, what the rootless engine
	// cannot set up for itself. The guest recreates /dev/net/tun and /dev/fuse
	// as 0600 on every boot, so it opens them to dev as a regular distro does;
	// opening either grants nothing without the capability in a user namespace
	// dev owns. It creates the dev-only runtime directory and the data root,
	// and links the default socket path to the rootless socket.
	dockerdRootlessPrepareCmd = "set -e; " +
		"chmod 0666 /dev/net/tun; " +
		"if [ -c /dev/fuse ]; then chmod 0666 /dev/fuse; fi; " +
		"mkdir -p " + dockerdRuntimeDir + " " + dockerdDataRoot + "; " +
		"chown " + DefaultSandboxUser + ":" + DefaultSandboxUser + " " + dockerdRuntimeDir + " " + dockerdDataRoot + "; " +
		"chmod 0700 " + dockerdRuntimeDir + "; " +
		"ln -sfn " + dockerdRuntimeDir + "/docker.sock " + dockerdDefaultSocket

	// dockerdDaemonConfigPath is the image's dockerd configuration file. The
	// rootless engine would read ~/.config/docker/daemon.json instead, so the
	// launcher names this file explicitly when it exists, and never forces a
	// directive both in the file and on the command line, which dockerd
	// rejects.
	dockerdDaemonConfigPath = "/etc/docker/daemon.json"
	vfsStorageDriver        = "vfs"

	// fuseOverlayfsStorageDriver is the copy-on-write driver the launcher
	// prefers when the guest provides it. The kernel rejects overlay2 here (an
	// overlay upper on the guest overlay, or on virtiofs), but fuse-overlayfs
	// merges in userspace and exposes its own FUSE mount, so neither
	// restriction applies. The tarball's dockerd compiles the driver in.
	fuseOverlayfsStorageDriver = "fuse-overlayfs"

	// fuseOverlayfsProbeCmd reports whether the runner image provides the
	// fuse-overlayfs binary and a /dev/fuse the engine can use. It runs as dev,
	// the user the rootless engine runs as, after dockerdRootlessPrepareCmd
	// opened the device.
	fuseOverlayfsProbeCmd = "command -v fuse-overlayfs >/dev/null 2>&1 && " +
		"[ -c /dev/fuse ] && [ -r /dev/fuse ] && [ -w /dev/fuse ]"

	// dockerdCleanupCmd removes the residue a killed engine job can leave.
	// Killing the job tears down rootlesskit and dockerd, but helper processes
	// and the runtime directory's sockets, pid files, and state may survive, so
	// they are cleared explicitly before a restart.
	dockerdCleanupCmd = "pkill -KILL -x containerd >/dev/null 2>&1 || true; " +
		"pkill -KILL -x slirp4netns >/dev/null 2>&1 || true; " +
		"find " + dockerdRuntimeDir + " -mindepth 1 -delete >/dev/null 2>&1 || true"
)

// The readiness window must exceed dockerd's 15s containerd start timeout, so a
// restart has time to come up before the poll gives up.
const (
	dockerdReadyTimeoutDefault = 30 * time.Second
	dockerdPollIntervalDefault = time.Second
)

var (
	dockerdReadyTimeout = dockerdReadyTimeoutDefault
	dockerdPollInterval = dockerdPollIntervalDefault
)

// dockerdArgs builds the rootless engine's argument list for this VM and
// returns the storage driver it selected. The microsandbox filesystem cannot
// back overlay2, so the launcher asks for a driver the guest supports:
// fuse-overlayfs when its binary and /dev/fuse are usable, vfs otherwise. The
// image's daemon.json, when present, is passed as the engine's configuration,
// and a launcher flag (--storage-driver, --data-root) is dropped when the file
// already sets that key, because dockerd refuses a directive set both as a flag
// and in the configuration file. A daemon.json requesting any driver other than
// those two is a hard error rather than a silent override or a flag/file
// conflict.
func dockerdArgs(ctx context.Context, sb msb.Sandbox) ([]string, string, error) {
	config, err := readGuestDaemonConfig(ctx, sb)
	if err != nil {
		return nil, "", err
	}
	var args []string
	if config.present {
		args = append(args, "--config-file", dockerdDaemonConfigPath)
	}
	if config.dataRoot == "" {
		args = append(args, "--data-root="+dockerdDataRoot)
	}
	driver := config.storageDriver
	if driver == "" {
		driver = defaultGuestStorageDriver(ctx, sb)
		args = append(args, "--storage-driver="+driver)
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
	out, err := sb.Shell(ctx, fuseOverlayfsProbeCmd, msb.ExecRequest{User: DefaultSandboxUser})
	if err != nil {
		return false
	}
	return out.Success()
}

// guestDaemonConfig is what the launcher needs from the image's daemon.json:
// whether the file exists, and the launcher-owned directives it already sets
// ("" when unset).
type guestDaemonConfig struct {
	present       bool
	storageDriver string
	dataRoot      string
}

// readGuestDaemonConfig reads the image's daemon.json. An absent file, or one
// that is not parseable as JSON, sets no directive; dockerd reports the parse
// error itself. A directive that is set but not a string is an error, since
// dockerd could not read it either.
func readGuestDaemonConfig(ctx context.Context, sb msb.Sandbox) (guestDaemonConfig, error) {
	exists, err := sb.FS().Exists(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return guestDaemonConfig{}, fmt.Errorf("inspect %s: %w", dockerdDaemonConfigPath, err)
	}
	if !exists {
		return guestDaemonConfig{}, nil
	}
	raw, err := sb.FS().Read(ctx, dockerdDaemonConfigPath)
	if err != nil {
		return guestDaemonConfig{}, fmt.Errorf("read %s: %w", dockerdDaemonConfigPath, err)
	}
	var directives map[string]json.RawMessage
	// json.Unmarshal pre-validates the whole document before decoding into the
	// map, and a map of RawMessage cannot raise a type error, so an unparseable
	// document always leaves directives empty, which the lookups below read as
	// unset. dockerd reports the parse error itself.
	_ = json.Unmarshal(raw, &directives)
	storageDriver, err := stringDirective(directives, "storage-driver")
	if err != nil {
		return guestDaemonConfig{}, err
	}
	dataRoot, err := stringDirective(directives, "data-root")
	if err != nil {
		return guestDaemonConfig{}, err
	}
	return guestDaemonConfig{present: true, storageDriver: storageDriver, dataRoot: dataRoot}, nil
}

// stringDirective returns a string directive from daemon.json, or "" when it is
// absent or null.
func stringDirective(directives map[string]json.RawMessage, key string) (string, error) {
	rawValue, ok := directives[key]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return "", fmt.Errorf("%s has a non-string %s value", dockerdDaemonConfigPath, key)
	}
	return value, nil
}

// startDockerdIfPresent starts the Docker engine inside the VM, rootless as
// dev, as a runtime-owned detached job if the docker image is in use, or does
// nothing otherwise. It is safe to call on every VM bootstrap because it checks
// for the dockerd binary and handles the case where the engine is already
// running. An image with dockerd but without the rootless entrypoint predates
// the rootless engine and must be rebuilt; the launcher never falls back to a
// root-equivalent engine. The launch/readiness sequence is the shared
// ensureDetachedService recipe.
func startDockerdIfPresent(ctx context.Context, sb msb.Sandbox, ui termio.Logger) error {
	out, checkErr := sb.Shell(ctx, dockerdBinaryCheckCmd, msb.ExecRequest{User: RootUser})
	if checkErr != nil {
		return fmt.Errorf("while checking dockerd binary: %w", checkErr)
	}
	if !out.Success() {
		ui.Verbosef("/usr/bin/dockerd not present, skipping Docker startup")
		return nil
	}

	return ensureDetachedService(ctx, sb, ui, detachedService{
		helper: dockerdHelper(),
		probe: func(probeCtx context.Context) (bool, string) {
			return probeHealthy(probeCtx, sb, dockerdReadyCmd)
		},
		onUnhealthy: func(detail string) {
			ui.Verbosef("dockerd not ready: %s", detail)
		},
		label:      dockerdBinary,
		startLabel: dockerdBinary,
		launchArgs: func(launchCtx context.Context) ([]string, string, error) {
			if err := prepareRootlessDockerd(launchCtx, sb); err != nil {
				return nil, "", err
			}
			args, driver, err := dockerdArgs(launchCtx, sb)
			if err != nil {
				return nil, "", err
			}
			return args, fmt.Sprintf("starting rootless dockerd with %s storage driver", driver), nil
		},
		execUser: DefaultSandboxUser,
		env:      map[string]string{"XDG_RUNTIME_DIR": dockerdRuntimeDir},
		// Killing the engine job does not necessarily clear its helper
		// processes and runtime state, so clear the residue before the fresh
		// launch. Best-effort: a failure is reported and the readiness probe
		// decides.
		cleanup: func(cleanupCtx context.Context) error {
			_, cleanupErr := sb.Shell(cleanupCtx, dockerdCleanupCmd, msb.ExecRequest{User: RootUser})
			return cleanupErr
		},
		timeout:            dockerdReadyTimeout,
		pollInterval:       dockerdPollInterval,
		alreadyHealthyNote: "using already running dockerd",
		readyNote:          "dockerd is ready",
		closeNote:          "closing dockerd job handle failed: %v",
		cleanupNote:        "dockerd residue cleanup failed (continuing): %v",
	})
}

// prepareRootlessDockerd verifies the image carries the rootless entrypoint and
// performs the root-side setup the rootless engine needs before each launch.
func prepareRootlessDockerd(ctx context.Context, sb msb.Sandbox) error {
	out, err := sb.Shell(ctx, dockerdRootlessCheckCmd, msb.ExecRequest{User: RootUser})
	if err != nil {
		return fmt.Errorf("while checking %s: %w", dockerdRootlessBinary, err)
	}
	if !out.Success() {
		return fmt.Errorf(
			"the runner image has dockerd but not %s, so it predates the rootless Docker engine; "+
				"rebuild it with --rebuild",
			dockerdRootlessBinary,
		)
	}
	out, err = sb.Shell(ctx, dockerdRootlessPrepareCmd, msb.ExecRequest{User: RootUser})
	if err != nil {
		return fmt.Errorf("prepare the rootless Docker engine: %w", err)
	}
	if !out.Success() {
		return fmt.Errorf("prepare the rootless Docker engine: %s", strings.TrimSpace(out.Stderr()+out.Stdout()))
	}
	return nil
}
