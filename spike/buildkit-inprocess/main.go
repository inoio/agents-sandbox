// Command bkspike is a throwaway feasibility spike: it embeds BuildKit
// in-process (no buildkitd daemon, no Docker daemon) by reimplementing the
// minimal controller/worker wiring from buildkitd's own newController, then
// drives it with the BuildKit Go client over a local unix socket to build a
// Dockerfile and export an OCI/docker archive.
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
	"time"

	"github.com/containerd/containerd/v2/plugins/snapshots/native"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/db/boltutil"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/runc"
	"google.golang.org/grpc"
)

func main() {
	var (
		rootDir  = flag.String("root", "", "state root (defaults to a temp dir)")
		exporter = flag.String("exporter", "oci", "exporter type: oci or docker")
		outTar   = flag.String("out", "", "output archive path (defaults to <root>/image.tar)")
		scratch  = flag.Bool("scratch", false, "build a FROM scratch image (no base-image pull)")
		rootless = flag.Bool("rootless", true, "run the OCI worker in rootless mode (needs subordinate IDs when true)")
	)
	flag.Parse()

	if err := run(*rootDir, *exporter, *outTar, *scratch, *rootless); err != nil {
		fmt.Fprintln(os.Stderr, "spike failed:", err)
		os.Exit(1)
	}
}

func run(rootDir, exporter, outTar string, scratch, rootless bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if rootDir == "" {
		dir, err := os.MkdirTemp("", "bkspike-state-*")
		if err != nil {
			return err
		}
		rootDir = dir
	}
	if outTar == "" {
		outTar = filepath.Join(rootDir, "image.tar")
	}
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return err
	}
	fmt.Printf("spike: root=%s exporter=%s out=%s goos=%s\n", rootDir, exporter, outTar, runtime.GOOS)

	controller, err := newController(ctx, rootDir, rootless)
	if err != nil {
		return fmt.Errorf("build in-process controller: %w", err)
	}
	defer controller.Close()

	socket := filepath.Join(rootDir, "buildkitd.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	server := grpc.NewServer()
	controller.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	c, err := client.New(ctx, "unix://"+socket)
	if err != nil {
		return fmt.Errorf("connect buildkit client: %w", err)
	}
	defer c.Close()

	contextDir := filepath.Join(rootDir, "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		return err
	}
	dockerfileBody := "FROM alpine:3.19\nRUN echo hello-from-in-process-buildkit > /hello\n"
	if scratch {
		dockerfileBody = "FROM scratch\nCOPY hello.txt /hello\n"
		if err := os.WriteFile(
			filepath.Join(contextDir, "hello.txt"),
			[]byte("hello-from-in-process-buildkit\n"),
			0o644,
		); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(dockerfileBody), 0o644); err != nil {
		return err
	}

	out, err := os.Create(outTar)
	if err != nil {
		return err
	}
	defer out.Close()

	statusCh := make(chan *client.SolveStatus)
	go drainStatus(statusCh)

	solveOpt := client.SolveOpt{
		Frontend: "dockerfile.v0",
		LocalDirs: map[string]string{
			"context":    contextDir,
			"dockerfile": contextDir,
		},
		Exports: []client.ExportEntry{
			{
				Type: exporter,
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
	}

	start := time.Now()
	_, err = c.Solve(ctx, nil, solveOpt, statusCh)
	if err != nil {
		return fmt.Errorf("solve: %w", err)
	}

	info, err := os.Stat(outTar)
	if err != nil {
		return err
	}
	fmt.Printf(
		"spike: build OK in %s, archive %s (%d bytes)\n",
		time.Since(start).Round(time.Millisecond),
		outTar,
		info.Size(),
	)
	return nil
}

func drainStatus(ch chan *client.SolveStatus) {
	for range ch {
	}
}

// newController mirrors the minimal subset of buildkitd's newController: one
// OCI (runc) worker with the native snapshotter, the dockerfile frontend, and
// a bolt-backed cache store.
func newController(ctx context.Context, root string, rootless bool) (*control.Controller, error) {
	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, err
	}

	snFactory := runc.SnapshotterFactory{Name: "native"}
	snFactory.New = native.NewSnapshotter

	workerOpt, err := runc.NewWorkerOpt(
		root,
		snFactory,
		rootless,           // rootless
		oci.ProcessSandbox, // process mode
		nil,                // labels
		nil,                // identity mapping
		netproviders.Opt{Mode: "host"},
		nil,   // dns
		"",    // oci worker binary (default runc)
		"",    // apparmor profile
		false, // selinux
		nil,   // parallelism semaphore
		"",    // trace socket
		"",    // default cgroup parent
		nil,   // cdi manager
	)
	if err != nil {
		return nil, err
	}
	workerOpt.RegistryHosts = resolver.NewRegistryConfig(nil)

	w, err := base.NewWorker(ctx, workerOpt)
	if err != nil {
		return nil, err
	}

	wc := &worker.Controller{}
	if err := wc.Add(w); err != nil {
		return nil, err
	}

	frontends := map[string]frontend.Frontend{
		"dockerfile.v0": forwarder.NewGatewayForwarder(wc.Infos(), dockerfile.Build),
	}

	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(root, "cache.db"))
	if err != nil {
		return nil, err
	}

	historyDB, err := boltutil.Open(filepath.Join(root, "history.db"), 0o600, nil)
	if err != nil {
		return nil, err
	}

	return control.NewController(control.Opt{
		SessionManager:   sessionManager,
		WorkerController: wc,
		Frontends:        frontends,
		CacheManager:     solver.NewCacheManager(ctx, "local", cacheStorage, worker.NewCacheResultStorage(wc)),
		HistoryDB:        historyDB,
		CacheStore:       cacheStorage,
		LeaseManager:     w.LeaseManager(),
		ContentStore:     w.ContentStore(),
		GarbageCollect:   w.GarbageCollect,
	})
}
