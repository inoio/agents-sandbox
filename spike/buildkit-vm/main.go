// Command bkvmspike is a throwaway feasibility spike for issue #15: build the
// runner image with no Docker/Podman on the host by running BuildKit inside a
// microsandbox (msb) VM on every platform.
//
// Two transports are probed:
//
//   - -transport grpc:    the BuildKit Go client runs on the host and streams
//     the context up / archive down over the published TCP port.
//   - -transport buildctl: buildctl runs inside the VM and reads/writes
//     bind-mounted context/output directories (no gRPC over the network).
//
// Requires a host with KVM (msb boots a Linux microVM); it cannot run inside
// the agents-sandbox dev VM, which has no /dev/kvm.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/platforms"
	distreference "github.com/distribution/reference"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	guestContextDir = "/in"
	guestOutputDir  = "/out"
)

func main() {
	var (
		image     = flag.String("image", "moby/buildkit:v0.25.2", "builder image")
		transport = flag.String("transport", "buildctl", "build transport: buildctl or grpc")
		guestPort = flag.Uint("guest-port", 1234, "guest port buildkitd listens on (grpc transport)")
		hostPort  = flag.Uint("host-port", 11234, "host port published for buildkitd gRPC (grpc transport)")
		exporter  = flag.String("exporter", "oci", "exporter type: oci or docker")
		outTar    = flag.String("out", "", "output archive path (defaults to a temp file)")
		scratch   = flag.Bool("scratch", false, "build a FROM scratch image (no base-image pull)")
		msbLoad   = flag.Bool("msb-load", true, "load the exported archive into the microsandbox image cache")
		keep      = flag.Bool("keep", false, "keep the builder sandbox after the spike (for inspection)")
		cacheVol  = flag.String(
			"cache-volume",
			"agents-sandbox-buildkit-spike-cache",
			"named msb volume for the BuildKit cache",
		)
		handshake = flag.Duration("handshake-timeout", 6*time.Second, "grpc client dial+list-workers timeout")
		ready     = flag.Duration(
			"ready-timeout",
			30*time.Second,
			"how long to wait for buildkitd to accept connections",
		)
		addr   = flag.String("addr", "", "diagnose: buildkit host addr, e.g. tcp://127.0.0.1:11234 (skips sandbox creation)")
		attach = flag.String("attach", "", "diagnose: existing msb sandbox name to fund the host port and tail buildkitd's log")
		base   = flag.String("base", "debian:trixie-slim", "diagnose: base image ref for the ResolveImageConfig step")
		stepTO = flag.Duration("step-timeout", 20*time.Second, "diagnose: per-step timeout (a step exceeding it is reported as HUNG)")
		bkLog  = flag.String("buildkitd-log", "/var/lib/buildkit/buildkitd.log", "diagnose: in-VM buildkitd log path to tail (-attach)")
	)
	flag.Parse()

	cfg := config{
		image: *image, transport: *transport,
		guestPort: uint16(*guestPort), hostPort: uint16(*hostPort),
		exporter: *exporter, outTar: *outTar, scratch: *scratch,
		msbLoad: *msbLoad, keep: *keep, cacheVol: *cacheVol,
		handshakeTimeout: *handshake, readyTimeout: *ready,
		addr: *addr, attach: *attach, base: *base,
		stepTimeout: *stepTO, buildkitdLog: *bkLog,
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "spike failed:", err)
		os.Exit(1)
	}
}

type config struct {
	image            string
	transport        string
	guestPort        uint16
	hostPort         uint16
	exporter         string
	outTar           string
	scratch          bool
	msbLoad          bool
	keep             bool
	cacheVol         string
	handshakeTimeout time.Duration
	readyTimeout     time.Duration
	addr             string
	attach           string
	base             string
	stepTimeout      time.Duration
	buildkitdLog     string
}

func run(cfg config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	if _, err := microsandbox.EnsureRuntime(
		ctx,
		microsandbox.RuntimeConfig{},
		microsandbox.InstallOptions{},
	); err != nil {
		return fmt.Errorf("ensure msb runtime: %w", err)
	}

	if cfg.transport == "porttest" {
		return runPortTest(ctx, cfg)
	}
	if cfg.transport == "httpprobe" {
		return runHTTPProbe(ctx, cfg)
	}
	if cfg.transport == "diagnose" {
		return runDiagnose(ctx, cfg)
	}

	contextDir, err := writeBuildContext(cfg.scratch)
	if err != nil {
		return err
	}

	// Persistent BuildKit cache: a named volume mounted at buildkitd's root so
	// layers survive across ephemeral builder VMs.
	if _, err := microsandbox.CreateVolume(
		ctx,
		cfg.cacheVol,
	); err != nil &&
		!microsandbox.IsKind(err, microsandbox.ErrVolumeAlreadyExists) {
		return fmt.Errorf("create cache volume: %w", err)
	}

	sandboxName := fmt.Sprintf("agents-sandbox-buildkit-spike-%d", time.Now().Unix())
	mounts := map[string]microsandbox.MountConfig{
		"/var/lib/buildkit": microsandbox.Mount.Named(cfg.cacheVol, microsandbox.MountOptions{}),
	}
	if cfg.transport == "buildctl" {
		outDir, err := os.MkdirTemp("", "bkvm-out-*")
		if err != nil {
			return err
		}
		mounts[guestContextDir] = microsandbox.Mount.Bind(contextDir, microsandbox.MountOptions{Readonly: true})
		mounts[guestOutputDir] = microsandbox.Mount.Bind(outDir, microsandbox.MountOptions{})
		cfg.outTar = filepath.Join(outDir, "image.tar")
	}
	if cfg.outTar == "" {
		f, err := os.CreateTemp("", "bkvm-image-*.tar")
		if err != nil {
			return err
		}
		cfg.outTar = f.Name()
		_ = f.Close()
	}

	fmt.Printf("spike: creating builder sandbox %q from %s (transport=%s)\n", sandboxName, cfg.image, cfg.transport)
	sb, err := microsandbox.CreateSandbox(ctx, sandboxName,
		microsandbox.WithImage(cfg.image),
		microsandbox.WithPortBindings(portBinding(cfg)),
		microsandbox.WithMounts(mounts),
		// The builder must reach container registries; default egress is deny.
		microsandbox.WithNetwork(microsandbox.NetworkPolicy.AllowAll()),
		microsandbox.WithDetached(),
		microsandbox.WithReplace(),
	)
	if err != nil {
		return fmt.Errorf("create builder sandbox: %w", err)
	}
	defer func() {
		if cfg.keep {
			fmt.Printf("spike: keeping sandbox %q\n", sandboxName)
			return
		}
		_ = microsandbox.RemoveSandbox(context.Background(), sandboxName)
	}()

	// msb does not auto-run a long-lived service from the image entrypoint;
	// start buildkitd explicitly and stream its output.
	// Wildcard bind, matching the published-port pattern used by serve-only and
	// the SDK ports example. Go turns "0.0.0.0" into a dual-stack [::] listener.
	buildkitdArgs := "--oci-worker-snapshotter native"
	if cfg.transport == "grpc" || cfg.transport == "h2probe" {
		buildkitdArgs += fmt.Sprintf(" --addr tcp://0.0.0.0:%d", cfg.guestPort)
	}
	buildkitdCmd := "exec buildkitd " + buildkitdArgs
	fmt.Printf("spike: starting builder: %s\n", buildkitdCmd)
	proc, err := sb.ShellStream(ctx, buildkitdCmd)
	if err != nil {
		return fmt.Errorf("start buildkitd: %w", err)
	}
	defer proc.Close()
	go func() { _, _ = streamExec(ctx, proc, "buildkitd") }()

	switch cfg.transport {
	case "grpc":
		if err := waitForBuildkitdTCP(ctx, sb, cfg.guestPort, cfg.readyTimeout); err != nil {
			dumpDiagnostics(ctx, sb)
			return err
		}
		if err := buildViaGRPC(ctx, cfg, sb); err != nil {
			dumpDiagnostics(ctx, sb)
			return err
		}
	case "h2probe":
		if err := waitForBuildkitdTCP(ctx, sb, cfg.guestPort, cfg.readyTimeout); err != nil {
			dumpDiagnostics(ctx, sb)
			return err
		}
		if err := probeHTTP2(ctx, cfg); err != nil {
			dumpDiagnostics(ctx, sb)
			return err
		}
		return nil
	case "buildctl":
		if err := buildViaBuildctl(ctx, cfg, sb); err != nil {
			dumpDiagnostics(ctx, sb)
			return err
		}
	default:
		return fmt.Errorf("unknown transport %q (want buildctl or grpc)", cfg.transport)
	}

	info, err := os.Stat(cfg.outTar)
	if err != nil {
		return err
	}
	fmt.Printf("spike: archive %s (%d bytes)\n", cfg.outTar, info.Size())

	if cfg.msbLoad {
		ref := fmt.Sprintf("agents-sandbox-buildkit-spike:%d", time.Now().Unix())
		if _, err := microsandbox.Image.Load(ctx, cfg.outTar, ref); err != nil {
			return fmt.Errorf("microsandbox.Image.Load(%s): %w", cfg.outTar, err)
		}
		detail, err := microsandbox.Image.Inspect(ctx, ref)
		if err != nil {
			return fmt.Errorf("microsandbox.Image.Inspect(%s): %w", ref, err)
		}
		fmt.Printf("spike: loaded %s into msb cache (%d layers)\n", ref, len(detail.Layers))
	}
	return nil
}

// portBinding mirrors the serve-only published-port configuration used by the
// launcher (explicit loopback host bind via PortBindings, not the legacy
// WithPorts map).
func portBinding(cfg config) microsandbox.PortBinding {
	return microsandbox.PortBinding{
		Bind:      "127.0.0.1",
		HostPort:  cfg.hostPort,
		GuestPort: cfg.guestPort,
		Protocol:  microsandbox.PortProtocolTCP,
	}
}

// runPortTest mirrors the SDK's own ports example: a guest `nc` listener on
// 0.0.0.0 writes a payload that the host reads through the published port. This
// separates "published ports are broken here" from "only buildkitd fails".
func runPortTest(ctx context.Context, cfg config) error {
	name := fmt.Sprintf("agents-sandbox-porttest-%d", time.Now().Unix())
	sb, err := microsandbox.CreateSandbox(ctx, name,
		microsandbox.WithImage("alpine:3.19"),
		microsandbox.WithPortBindings(portBinding(cfg)),
	)
	if err != nil {
		return fmt.Errorf("create porttest sandbox: %w", err)
	}
	defer func() {
		if !cfg.keep {
			_ = microsandbox.RemoveSandbox(context.Background(), name)
		}
	}()

	payload := "pong-from-vm"
	cmd := fmt.Sprintf("printf '%s' | nc -l -p %d", payload, cfg.guestPort)
	h, err := sb.ShellStream(ctx, cmd)
	if err != nil {
		return fmt.Errorf("start nc: %w", err)
	}
	defer h.Close()
	go func() { _, _ = streamExec(ctx, h, "nc") }()

	if err := waitForGuestPort(ctx, sb, cfg.guestPort, cfg.readyTimeout); err != nil {
		dumpDiagnostics(ctx, sb)
		return err
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.hostPort)
	var conn net.Conn
	for i := 0; i < 20; i++ {
		conn, err = (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("dial published port %s: %w", addr, err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(cfg.handshakeTimeout))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read published port: %w", err)
	}
	fmt.Printf("spike: PORT TEST WORKS — read %q from guest\n", string(buf[:n]))
	return nil
}

// runHTTPProbe verifies HTTP/1 client-first traffic through a published port:
// a guest `busybox httpd` (HTTP/1 server) only responds after it has read the
// request, so a response proves the host's bytes were delivered to the guest.
func runHTTPProbe(ctx context.Context, cfg config) error {
	name := fmt.Sprintf("agents-sandbox-httpprobe-%d", time.Now().Unix())
	sb, err := microsandbox.CreateSandbox(ctx, name,
		microsandbox.WithImage("alpine:3.19"),
		microsandbox.WithPortBindings(portBinding(cfg)),
	)
	if err != nil {
		return fmt.Errorf("create httpprobe sandbox: %w", err)
	}
	defer func() {
		if !cfg.keep {
			_ = microsandbox.RemoveSandbox(context.Background(), name)
		}
	}()

	// alpine's busybox omits httpd; busybox-extras provides it. Guest egress works.
	cmd := fmt.Sprintf(
		"sh -c 'apk add --no-cache busybox-extras >/dev/null 2>&1; echo ok > /tmp/index.html; exec httpd -f -p %d -h /tmp'",
		cfg.guestPort,
	)
	h, err := sb.ShellStream(ctx, cmd)
	if err != nil {
		return fmt.Errorf("start httpd: %w", err)
	}
	defer h.Close()
	go func() { _, _ = streamExec(ctx, h, "httpd") }()

	// Confirm httpd is actually serving from inside the guest before blaming the
	// host-side published port.
	if err := waitForGuestHTTP(ctx, sb, cfg); err != nil {
		dumpDiagnostics(ctx, sb)
		return err
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.hostPort)
	request := "GET / HTTP/1.0\r\nHost: probe\r\n\r\n"
	deadline := time.Now().Add(cfg.readyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(cfg.handshakeTimeout))
		if _, err := conn.Write([]byte(request)); err != nil {
			lastErr = err
			_ = conn.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		_ = conn.Close()
		if err == nil && n > 0 {
			fmt.Printf("spike: HTTP/1 WORKS — guest responded: %q\n", strings.SplitN(string(buf[:n]), "\r\n", 2)[0])
			return nil
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("published port did not carry HTTP/1: %v", lastErr)
}

// waitForGuestPort polls from inside the guest until something is listening on
// port, proving the guest-side server is up independently of the published port.
func waitForGuestPort(ctx context.Context, sb *microsandbox.Sandbox, port uint16, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := fmt.Sprintf("netstat -ltn 2>/dev/null | grep -q ':%d ' && echo LISTEN_OK", port)
	var last string
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, cmd)
		if err == nil && strings.Contains(out.Stdout(), "LISTEN_OK") {
			fmt.Printf("spike: guest listener on :%d is up\n", port)
			return nil
		}
		if err == nil {
			last = strings.TrimSpace(out.Stderr())
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("guest listener on :%d not up: %s", port, last)
}

// waitForGuestHTTP polls from inside the guest until busybox httpd answers,
// proving the guest-side server is up independently of the published port.
func waitForGuestHTTP(ctx context.Context, sb *microsandbox.Sandbox, cfg config) error {
	deadline := time.Now().Add(cfg.readyTimeout)
	cmd := fmt.Sprintf("wget -q -O /dev/null http://127.0.0.1:%d/ && echo GUEST_HTTP_UP", cfg.guestPort)
	var last string
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, cmd)
		if err == nil && strings.Contains(out.Stdout(), "GUEST_HTTP_UP") {
			fmt.Println("spike: guest httpd is serving")
			return nil
		}
		if err == nil {
			last = strings.TrimSpace(out.Stderr())
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("guest httpd not serving on :%d: %s", cfg.guestPort, last)
}

// probeHTTP2 sends the raw HTTP/2 client connection preface (magic + an empty
// SETTINGS frame) over the published port and reports whether the guest sends
// any bytes back. This isolates "does the published port carry an HTTP/2
// connection at all?" from the BuildKit gRPC stack.
func probeHTTP2(ctx context.Context, cfg config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.hostPort)
	conn, err := (&net.Dialer{Timeout: cfg.handshakeTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	preface := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	settings := []byte{0, 0, 0, 0x04, 0, 0, 0, 0, 0}
	if _, err := conn.Write(append(preface, settings...)); err != nil {
		return fmt.Errorf("write h2 preface: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(cfg.handshakeTimeout)); err != nil {
		return err
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if n > 0 {
		fmt.Printf("spike: HTTP/2 WORKS — guest sent %d bytes: % x\n", n, buf[:n])
		return nil
	}
	return fmt.Errorf("published port did not carry HTTP/2: read %d bytes, err=%v", n, err)
}

// waitForBuildkitdTCP verifies from inside the guest that buildkitd's TCP
// listener is accepting before the host connects. Connecting through the msb
// published port before the guest listener is up races the relay and yields a
// guest RST (observed as gRPC "error reading server preface: EOF").
func waitForBuildkitdTCP(ctx context.Context, sb *microsandbox.Sandbox, port uint16, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := fmt.Sprintf("buildctl --addr tcp://127.0.0.1:%d debug workers", port)
	var last string
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, cmd)
		if err == nil && out.Success() {
			fmt.Printf("spike: guest TCP listener on :%d is up\n", port)
			return nil
		}
		if err == nil {
			last = strings.TrimSpace(out.Stderr())
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("buildkitd TCP listener on :%d not ready: %s", port, last)
}

// buildViaBuildctl runs buildctl inside the VM, reading the bind-mounted
// context and writing the archive to the bind-mounted output directory.
func buildViaBuildctl(ctx context.Context, cfg config, sb *microsandbox.Sandbox) error {
	if err := waitForBuildctl(ctx, sb, cfg.readyTimeout); err != nil {
		return err
	}
	outSpec := fmt.Sprintf("type=%s,dest=%s/image.tar", cfg.exporter, guestOutputDir)
	cmd := strings.Join([]string{
		"buildctl", "build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + guestContextDir,
		"--local", "dockerfile=" + guestContextDir,
		"--output", outSpec,
	}, " ")
	fmt.Printf("spike: building: %s\n", cmd)
	h, err := sb.ShellStream(ctx, cmd)
	if err != nil {
		return fmt.Errorf("run buildctl: %w", err)
	}
	defer h.Close()
	code, err := streamExec(ctx, h, "buildctl")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("buildctl exited %d", code)
	}
	return nil
}

func waitForBuildctl(ctx context.Context, sb *microsandbox.Sandbox, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := sb.Shell(ctx, "buildctl debug workers")
		if err == nil && out.Success() {
			return nil
		}
		if err == nil {
			last = strings.TrimSpace(out.Stderr())
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("buildkitd not ready for buildctl: %s", last)
}

// runDiagnose connects to an existing BuildKit host and replays the launcher's
// exact pre-build sequence — dial, ListWorkers, the gateway ResolveImageConfig
// used by resolveBaseDigest, then Solve — with a per-step timeout so the step
// that hangs (never returns) is named explicitly. It also tails buildkitd's
// in-VM log when -attach names a running builder sandbox.
//
// Point it at a live builder while a launcher build is wedged:
//
//	./bkvmspike -transport diagnose -attach agents-sandbox-buildkit-v0.25.2
//	./bkvmspike -transport diagnose -addr tcp://127.0.0.1:11234
func runDiagnose(ctx context.Context, cfg config) error {
	addr := cfg.addr
	if cfg.attach != "" {
		handle, err := microsandbox.GetSandbox(ctx, cfg.attach)
		if err != nil {
			return fmt.Errorf("get sandbox %q: %w", cfg.attach, err)
		}
		fmt.Printf("diagnose: sandbox %q status %s\n", cfg.attach, handle.Status())
		// A stopped builder has been reaped (e.g. by its idle timeout), and a
		// fresh boot has no buildkitd running. Bring both up so the launcher's
		// sequence can be replayed against a live daemon.
		sb, err := handle.ConnectOrStart(ctx)
		if err != nil {
			return fmt.Errorf("connect/start sandbox %q: %w", cfg.attach, err)
		}
		defer func() { _ = sb.Close() }()

		// Hold the idle timer open for the whole diagnose run; otherwise the
		// builder's short idle timeout reaps the VM mid-step and every step
		// looks like a hang. Any Touch error is printed — a silent failure here
		// would mean the launcher's own keep-alive is equally ineffective.
		keepAliveDone := make(chan struct{})
		defer close(keepAliveDone)
		go keepSandboxAlive(ctx, handle, keepAliveDone)

		if err := ensureBuildkitdRunning(ctx, sb, cfg); err != nil {
			return err
		}

		// Derive the published host port now that buildkitd is serving. The
		// stored config may not carry the resolved binding, so fall back to
		// probing the builder host-port range for a live BuildKit endpoint.
		if addr == "" {
			addr = publishedAddr(ctx, handle)
		}
		if addr == "" {
			fmt.Fprintf(
				os.Stderr,
				"diagnose: no port in stored config for %q (config JSON: %s); probing loopback\n",
				cfg.attach, handle.ConfigJSON(),
			)
			addr = discoverBuilderAddr(ctx, cfg)
		}
		go tailBuildkitdLog(ctx, sb, cfg)
	}
	if addr == "" {
		return fmt.Errorf("diagnose needs -addr or an -attach sandbox publishing a port " +
			"(pass -addr tcp://127.0.0.1:PORT)")
	}
	fmt.Printf("diagnose: target %s (step timeout %s)\n", addr, cfg.stepTimeout)

	c, err := dialBuildkit(ctx, addr, cfg.handshakeTimeout)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := runStep(ctx, cfg.stepTimeout, "ListWorkers", func(sctx context.Context) error {
		_, err := c.ListWorkers(sctx)
		return err
	}); err != nil {
		return err
	}

	qualified := qualifyBaseRef(cfg.base)
	if err := runStep(ctx, cfg.stepTimeout, "ResolveImageConfig "+qualified, func(sctx context.Context) error {
		_, dgst, _, err := resolveViaGateway(sctx, c, qualified)
		if err == nil {
			fmt.Printf("diagnose:   resolved digest %s\n", dgst)
		}
		return err
	}); err != nil {
		return err
	}

	contextDir, err := writeBuildContext(cfg.scratch)
	if err != nil {
		return err
	}
	out, err := os.Create(cfg.outTar)
	if err != nil {
		return err
	}
	defer out.Close()

	statusCh := make(chan *client.SolveStatus)
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for status := range statusCh {
			printProgress(status)
		}
	}()
	err = runStep(ctx, cfg.stepTimeout, "Solve", func(sctx context.Context) error {
		_, err := c.Solve(sctx, nil, client.SolveOpt{
			Frontend: "dockerfile.v0",
			LocalDirs: map[string]string{
				"context":    contextDir,
				"dockerfile": contextDir,
			},
			Exports: []client.ExportEntry{{
				Type: cfg.exporter,
				Output: func(map[string]string) (io.WriteCloser, error) {
					return out, nil
				},
			}},
			Session: authSession(),
		}, statusCh)
		return err
	})
	<-progressDone
	if err != nil {
		return err
	}

	if info, statErr := out.Stat(); statErr == nil {
		fmt.Printf("diagnose: archive %s (%d bytes)\n", cfg.outTar, info.Size())
	}
	fmt.Println("diagnose: all steps completed")
	return nil
}

// ensureBuildkitdRunning makes sure a buildkitd is listening in sb, starting
// one (with its log appended to the same file the launcher uses) if the freshly
// booted sandbox has none. msb does not auto-run a long-lived service from the
// image entrypoint, so a restarted sandbox needs buildkitd launched explicitly.
func ensureBuildkitdRunning(ctx context.Context, sb *microsandbox.Sandbox, cfg config) error {
	probe := fmt.Sprintf("buildctl --addr tcp://127.0.0.1:%d debug workers", cfg.guestPort)
	if out, err := sb.Shell(ctx, probe); err == nil && out.Success() {
		fmt.Println("diagnose: buildkitd already running")
		return nil
	}

	cmd := fmt.Sprintf(
		"exec buildkitd --oci-worker-snapshotter native --addr tcp://0.0.0.0:%d >> %s 2>&1",
		cfg.guestPort, cfg.buildkitdLog,
	)
	fmt.Printf("diagnose: starting buildkitd: %s\n", cmd)
	h, err := sb.ShellStream(ctx, cmd)
	if err != nil {
		return fmt.Errorf("start buildkitd: %w", err)
	}
	go drainExec(ctx, h)

	deadline := time.Now().Add(cfg.readyTimeout)
	for time.Now().Before(deadline) {
		if out, err := sb.Shell(ctx, probe); err == nil && out.Success() {
			fmt.Println("diagnose: buildkitd ready")
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("buildkitd did not become ready within %s", cfg.readyTimeout)
}

// discoverBuilderAddr probes the builder host-port range for a live BuildKit
// endpoint, returning the first address that answers ListWorkers. Used when the
// sandbox's stored config does not expose its resolved published port. It scans
// from the configured -host-port base.
func discoverBuilderAddr(ctx context.Context, cfg config) string {
	base := int(cfg.hostPort)
	for port := base; port < base+256; port++ {
		addr := fmt.Sprintf("tcp://127.0.0.1:%d", port)
		c, err := dialBuildkit(ctx, addr, 750*time.Millisecond)
		if err != nil {
			continue
		}
		_ = c.Close()
		fmt.Printf("diagnose: discovered BuildKit endpoint at %s\n", addr)
		return addr
	}
	return ""
}

// publishedAddr returns the buildkit host address derived from a sandbox's
// published port, refreshing the handle first so a just-started sandbox reports
// its resolved binding. Empty when no published host port is known.
func publishedAddr(ctx context.Context, handle *microsandbox.SandboxHandle) string {
	candidates := []*microsandbox.SandboxHandle{handle}
	if refreshed, err := handle.Refresh(ctx); err == nil {
		candidates = append(candidates, refreshed)
	}
	for _, h := range candidates {
		c, err := h.Config()
		if err != nil {
			continue
		}
		for _, pb := range c.PortBindings {
			if pb.HostPort != 0 {
				return fmt.Sprintf("tcp://127.0.0.1:%d", pb.HostPort)
			}
		}
	}
	return ""
}

// keepSandboxAlive refreshes handle's idle timer every two seconds (matching
// the launcher's KeepAlive) until done or ctx ends, printing any Touch error.
func keepSandboxAlive(ctx context.Context, handle *microsandbox.SandboxHandle, done <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if _, err := handle.Touch(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "diagnose: keepalive touch failed: %v\n", err)
			}
		}
	}
}

// drainExec reads and discards an exec stream so it is not left unread.
func drainExec(ctx context.Context, h *microsandbox.ExecHandle) {
	defer func() { _ = h.Close() }()
	for {
		if _, err := h.Recv(ctx); err != nil {
			return
		}
	}
}

// runStep runs fn under a per-step timeout, reporting whether it returned or
// hung. A hang is the key signal: the launcher has no per-step timeout, so the
// first step that never returns is the one freezing the build.
func runStep(ctx context.Context, timeout time.Duration, name string, fn func(context.Context) error) error {
	fmt.Printf("diagnose: -> %s\n", name)
	start := time.Now()
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fn(sctx) }()

	select {
	case err := <-done:
		if err != nil {
			fmt.Printf("diagnose: !! %s FAILED after %s: %v\n", name, time.Since(start).Round(time.Millisecond), err)
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("diagnose: ok %s (%s)\n", name, time.Since(start).Round(time.Millisecond))
		return nil
	case <-sctx.Done():
		fmt.Printf("diagnose: !! %s HUNG — no result within %s\n", name, timeout)
		return fmt.Errorf("%s did not return within %s: %w", name, timeout, sctx.Err())
	}
}

// resolveViaGateway mirrors the launcher's realBuildkitClient.ResolveImageConfig:
// base-image metadata is resolved through a BuildKit gateway session, the
// BuildKit-native replacement for a pre-build `docker inspect`.
func resolveViaGateway(
	ctx context.Context,
	c *client.Client,
	ref string,
) (string, digest.Digest, []byte, error) {
	var (
		resolvedRef  string
		resolvedDgst digest.Digest
		resolvedCfg  []byte
	)
	_, err := c.Build(ctx, client.SolveOpt{Session: authSession()}, "",
		func(ctx context.Context, gw gwclient.Client) (*gwclient.Result, error) {
			var err error
			resolvedRef, resolvedDgst, resolvedCfg, err = gw.ResolveImageConfig(
				ctx, ref, sourceresolver.Opt{Platform: normalizePlatform(hostPlatform())},
			)
			if err != nil {
				return nil, err
			}
			return gwclient.NewResult(), nil
		},
		nil,
	)
	if err != nil {
		return "", "", nil, err
	}
	return resolvedRef, resolvedDgst, resolvedCfg, nil
}

// qualifyBaseRef normalizes a short image reference into its fully-qualified
// form, mirroring the launcher.
func qualifyBaseRef(baseRef string) string {
	named, err := distreference.ParseNormalizedNamed(baseRef)
	if err != nil {
		return baseRef
	}
	return named.String()
}

// hostPlatform is the linux platform matching the host architecture.
func hostPlatform() string {
	if runtime.GOARCH == "arm64" {
		return "linux/arm64"
	}
	return "linux/amd64"
}

func normalizePlatform(platform string) *ocispecs.Platform {
	parsed, err := platforms.Parse(platform)
	if err != nil {
		return nil
	}
	normalized := platforms.Normalize(parsed)
	return &normalized
}

// printProgress surfaces the BuildKit client's step stream: build log lines and
// CACHED vertices (cached RUN steps emit no logs).
func printProgress(status *client.SolveStatus) {
	for _, log := range status.Logs {
		for _, line := range strings.Split(strings.TrimRight(string(log.Data), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(os.Stderr, "[step] %s\n", line)
			}
		}
	}
	for _, vertex := range status.Vertexes {
		if vertex.Cached {
			fmt.Fprintf(os.Stderr, "[step] %s CACHED\n", vertex.Name)
		}
	}
}

// tailBuildkitdLog streams the in-VM buildkitd log to stderr so buildkitd's own
// view (e.g. "running server", TLS/registry errors) sits next to the client
// steps.
func tailBuildkitdLog(ctx context.Context, sb *microsandbox.Sandbox, cfg config) {
	h, err := sb.ShellStream(ctx, "tail -n +1 -f "+cfg.buildkitdLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: tail buildkitd log failed:", err)
		return
	}
	defer func() { _ = h.Close() }()
	for {
		ev, err := h.Recv(ctx)
		if err != nil {
			return
		}
		switch ev.Kind {
		case microsandbox.ExecEventStdout, microsandbox.ExecEventStderr:
			fmt.Fprintf(os.Stderr, "[buildkitd.log] %s", ev.Data)
		case microsandbox.ExecEventExited, microsandbox.ExecEventFailed, microsandbox.ExecEventDone:
			return
		}
	}
}

// authSession supplies registry credentials from the host's Docker config,
// mirroring the launcher's registryAuthSession.
func authSession() []session.Attachable {
	return []session.Attachable{
		authprovider.NewDockerAuthProvider(
			authprovider.DockerAuthProviderConfig{ConfigFile: dockerconfig.LoadDefaultConfigFile(os.Stderr)},
		),
	}
}

// buildViaGRPC drives the VM's buildkitd from a host-side BuildKit client.
func buildViaGRPC(ctx context.Context, cfg config, sb *microsandbox.Sandbox) error {
	addr := fmt.Sprintf("tcp://127.0.0.1:%d", cfg.hostPort)

	probeCtx, cancel := context.WithTimeout(ctx, cfg.handshakeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", fmt.Sprintf("127.0.0.1:%d", cfg.hostPort))
	if err != nil {
		return fmt.Errorf("published port %d not reachable: %w", cfg.hostPort, err)
	}
	_ = conn.Close()
	fmt.Printf("spike: published tcp port %d reachable\n", cfg.hostPort)

	c, err := dialBuildkit(ctx, addr, cfg.handshakeTimeout)
	if err != nil {
		return err
	}
	defer c.Close()

	contextDir, err := writeBuildContext(cfg.scratch)
	if err != nil {
		return err
	}

	out, err := os.Create(cfg.outTar)
	if err != nil {
		return err
	}
	defer out.Close()

	statusCh := make(chan *client.SolveStatus)
	go func() {
		for status := range statusCh {
			printProgress(status)
		}
	}()

	start := time.Now()
	_, err = c.Solve(ctx, nil, client.SolveOpt{
		Frontend: "dockerfile.v0",
		LocalDirs: map[string]string{
			"context":    contextDir,
			"dockerfile": contextDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: cfg.exporter,
				Output: func(map[string]string) (io.WriteCloser, error) {
					return out, nil
				},
			},
		},
		Session: []session.Attachable{
			authprovider.NewDockerAuthProvider(
				authprovider.DockerAuthProviderConfig{ConfigFile: dockerconfig.LoadDefaultConfigFile(os.Stderr)},
			),
		},
	}, statusCh)
	if err != nil {
		return fmt.Errorf("solve: %w", err)
	}
	fmt.Printf("spike: gRPC build OK in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// dialBuildkit connects a BuildKit client with a bounded handshake so a dead
// port does not hang for the whole run.
func dialBuildkit(ctx context.Context, addr string, timeout time.Duration) (*client.Client, error) {
	type result struct {
		c   *client.Client
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := client.New(ctx, addr)
		if err != nil {
			ch <- result{err: err}
			return
		}
		listCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if _, err := c.ListWorkers(listCtx); err != nil {
			_ = c.Close()
			ch <- result{err: err}
			return
		}
		ch <- result{c: c}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("buildkit client handshake timed out for %s", addr)
	}
}

// streamExec drains an exec handle, mirroring output to stderr, and returns the
// process exit code.
func streamExec(ctx context.Context, h *microsandbox.ExecHandle, prefix string) (int, error) {
	exitCode := -1
	for {
		ev, err := h.Recv(ctx)
		if err != nil {
			return exitCode, err
		}
		switch ev.Kind {
		case microsandbox.ExecEventStdout, microsandbox.ExecEventStderr:
			for _, line := range strings.Split(strings.TrimRight(string(ev.Data), "\n"), "\n") {
				if line != "" {
					fmt.Fprintf(os.Stderr, "[%s] %s\n", prefix, line)
				}
			}
		case microsandbox.ExecEventExited, microsandbox.ExecEventFailed:
			exitCode = ev.ExitCode
			fmt.Fprintf(os.Stderr, "[%s] process ended (exit=%d)\n", prefix, ev.ExitCode)
			return exitCode, nil
		case microsandbox.ExecEventDone:
			return exitCode, nil
		}
	}
}

// dumpDiagnostics prints sandbox logs and running processes to help debug a
// builder that never came up.
func dumpDiagnostics(ctx context.Context, sb *microsandbox.Sandbox) {
	fmt.Fprintln(os.Stderr, "spike: ===== sandbox diagnostics =====")
	if entries, err := sb.Logs(ctx, microsandbox.LogOptions{Tail: 200}); err == nil {
		for _, e := range entries {
			fmt.Fprintf(os.Stderr, "[log %s] %s\n", e.Source, strings.TrimRight(e.Text(), "\n"))
		}
	} else {
		fmt.Fprintln(os.Stderr, "spike: logs unavailable:", err)
	}
	if out, err := sb.Shell(
		ctx,
		"ps -ef; echo ---LISTENERS---; (ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null || echo no-ss); echo ---; ls -la /var/lib/buildkit",
	); err == nil {
		fmt.Fprintln(os.Stderr, out.Stdout())
		if out.Stderr() != "" {
			fmt.Fprintln(os.Stderr, out.Stderr())
		}
	}
	fmt.Fprintln(os.Stderr, "spike: =================================")
}

func writeBuildContext(scratch bool) (string, error) {
	dir, err := os.MkdirTemp("", "bkvm-context-*")
	if err != nil {
		return "", err
	}
	body := "FROM alpine:3.19\nRUN echo hello-from-buildkit-vm > /hello\n"
	if scratch {
		body = "FROM scratch\nCOPY hello.txt /hello\n"
		if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello-from-buildkit-vm\n"), 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(body), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}
