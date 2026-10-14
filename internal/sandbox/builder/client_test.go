package builder

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/opencontainers/go-digest"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

type closeCountingClient struct {
	closeCalls *atomic.Int32
}

func (c *closeCountingClient) ListWorkers(context.Context) error { return nil }

func (c *closeCountingClient) Solve(
	context.Context, *llb.Definition, client.SolveOpt, chan *client.SolveStatus,
) (*client.SolveResponse, error) {
	return &client.SolveResponse{}, nil
}

func (c *closeCountingClient) ResolveImageConfig(
	context.Context, string, sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("unsupported")
}

func (c *closeCountingClient) Close() error {
	c.closeCalls.Add(1)
	return nil
}

func TestDefaultReadyRetriesThenSucceeds(t *testing.T) {
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })

	var dials atomic.Int32
	var closeCalls atomic.Int32
	newBuildkitClient = func(_ context.Context, _ string) (buildkitClient, error) {
		if dials.Add(1) <= 2 {
			return nil, errors.New("connection refused")
		}
		return &closeCountingClient{closeCalls: &closeCalls}, nil
	}

	vm := &VM{}
	if err := vm.defaultReady(context.Background(), 1234); err != nil {
		t.Fatalf("defaultReady returned error: %v", err)
	}
	if got := dials.Load(); got != 3 {
		t.Errorf("dial attempts = %d, want 3", got)
	}
	if got := closeCalls.Load(); got != 1 {
		t.Errorf("close calls = %d, want 1", got)
	}
}

func TestDefaultReadyTimesOut(t *testing.T) {
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })

	newBuildkitClient = func(_ context.Context, _ string) (buildkitClient, error) {
		return nil, errors.New("connection refused")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	vm := &VM{}
	err := vm.defaultReady(ctx, 1234)
	if err == nil {
		t.Fatal("defaultReady returned nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1234") {
		t.Errorf("error %q does not mention the address", err)
	}
}

func TestBuilderCacheExportOptionsUseRegistry(t *testing.T) {
	const cacheRef = cacheregistry.GuestHost + ":5000/agents-sandbox/project-opencode-amd64:cache"

	imports, exports, err := builderCacheExportOptions(context.Background(), cacheRef)
	if err != nil {
		t.Fatalf("builderCacheExportOptions: %v", err)
	}

	if len(imports) != 1 || imports[0].Type != "registry" || imports[0].Attrs["ref"] != cacheRef {
		t.Errorf("imports = %+v, want a single registry import of %q", imports, cacheRef)
	}
	if len(exports) != 1 || exports[0].Type != "registry" {
		t.Fatalf("exports = %+v, want a single registry export", exports)
	}
	if exports[0].Attrs["ref"] != cacheRef || exports[0].Attrs["mode"] != "max" {
		t.Errorf("export attrs = %+v, want ref %q mode max", exports[0].Attrs, cacheRef)
	}
	for name, entry := range map[string]client.CacheOptionsEntry{"import": imports[0], "export": exports[0]} {
		if entry.Attrs["registry.insecure"] != "true" {
			t.Errorf("%s attrs = %+v, want registry.insecure=true for the plain-HTTP registry", name, entry.Attrs)
		}
	}
	for _, entry := range append(imports, exports...) {
		if entry.Type == "local" || entry.Type == "inline" {
			t.Errorf("cache entry %+v is not a registry entry", entry)
		}
	}
}

func TestBuilderCacheRefDelegatesToEndpoint(t *testing.T) {
	endpoint := cacheregistry.Endpoint{Host: cacheregistry.GuestHost, Port: 5000, Token: "t"}

	got := builderCacheRef(endpoint, "project/opencode:1.2.3", "linux/amd64")
	if want := endpoint.CacheRef("project/opencode:1.2.3", "linux/amd64"); got != want {
		t.Errorf("builderCacheRef = %q, want endpoint.CacheRef %q", got, want)
	}
}

func TestBuildkitdCommandDefaultsToFuseOverlayfs(t *testing.T) {
	command := buildkitdCommand()
	if !strings.Contains(command, "--oci-worker-snapshotter "+snapshotterFuseOverlayfs) {
		t.Errorf("buildkitdCommand() = %q, want the fuse-overlayfs snapshotter", command)
	}
	if !strings.Contains(command, "--addr tcp://0.0.0.0:") {
		t.Errorf("buildkitdCommand() = %q, want the guest-port bind", command)
	}
	if !strings.Contains(command, "command -v fuse-overlayfs") {
		t.Errorf("buildkitdCommand() = %q, want the fuse-overlayfs binary fallback", command)
	}
	if !strings.Contains(command, workerSnapshotterMarker) {
		t.Errorf("buildkitdCommand() = %q, want the snapshotter-change worker reset", command)
	}
	if !strings.Contains(command, "--oci-worker-gc-keepstorage "+builderCacheGCKeepStorage) {
		t.Errorf("buildkitdCommand() = %q, want the GC keep-storage budget", command)
	}
	if strings.Contains(command, "--oci-worker-no-process-sandbox") {
		t.Errorf("buildkitdCommand() = %q, must not disable the process sandbox by default", command)
	}
}

func TestBuildkitdCommandNoProcessSandbox(t *testing.T) {
	t.Setenv(builderNoProcessSandboxEnv, "1")
	if command := buildkitdCommand(); !strings.Contains(command, "--oci-worker-no-process-sandbox") {
		t.Errorf("buildkitdCommand() = %q, want the process-sandbox opt-out from the env", command)
	}
	t.Setenv(builderNoProcessSandboxEnv, "false")
	if command := buildkitdCommand(); strings.Contains(command, "--oci-worker-no-process-sandbox") {
		t.Errorf("buildkitdCommand() = %q, want the process sandbox kept for a false env", command)
	}
}

func TestBuildkitdCommandHonorsSnapshotterOverride(t *testing.T) {
	t.Setenv(builderSnapshotterEnv, snapshotterNative)
	command := buildkitdCommand()
	if !strings.Contains(command, "--oci-worker-snapshotter native") {
		t.Errorf("buildkitdCommand() = %q, want the native snapshotter from the override", command)
	}
	if strings.Contains(command, "apk add") {
		t.Errorf("buildkitdCommand() = %q, native must not install a snapshotter", command)
	}
	if !strings.Contains(command, workerSnapshotterMarker) {
		t.Errorf("buildkitdCommand() = %q, want the snapshotter-change worker reset", command)
	}
}

func TestSnapshotterPrepareCommand(t *testing.T) {
	if got := snapshotterPrepareCommand(""); got != "" {
		t.Errorf("snapshotterPrepareCommand(\"\") = %q, want empty", got)
	}
	native := snapshotterPrepareCommand(snapshotterNative)
	if !strings.Contains(native, workerSnapshotterMarker) || strings.Contains(native, "apk add") {
		t.Errorf("snapshotterPrepareCommand(native) = %q, want only the worker reset", native)
	}
	fuse := snapshotterPrepareCommand(snapshotterFuseOverlayfs)
	if !strings.Contains(fuse, "apk add --no-cache fuse-overlayfs") {
		t.Errorf("snapshotterPrepareCommand(fuse-overlayfs) = %q, want the install fallback", fuse)
	}
}

func TestBuilderCacheMountIsDiskBackedOwnedVolume(t *testing.T) {
	mount := builderCacheMount()
	if mount.Tmpfs || mount.Owned != "disk" {
		t.Fatalf("builderCacheMount() = %+v, want a disk-backed owned volume (not tmpfs)", mount)
	}
	if mount.SizeMiB != builderCacheDiskSizeMiB {
		t.Errorf("cache size = %d MiB, want %d", mount.SizeMiB, builderCacheDiskSizeMiB)
	}
}

func TestNewClientWiresReadySeam(t *testing.T) {
	c := newClient()
	if c == nil {
		t.Fatal("newClient returned nil")
	}
	mb, ok := c.backend.(*msbBackend)
	if !ok {
		t.Fatalf("newClient backend = %T, want *msbBackend", c.backend)
	}
	if mb.vm == nil {
		t.Fatal("newClient did not construct a VM")
	}
	if mb.vm.Ready == nil {
		t.Fatal("newClient left VM.Ready nil; readiness would be silently skipped")
	}
	if mb.vm.Msb == nil {
		t.Fatal("newClient did not wire an msb.Client")
	}
}

func TestGetReturnsBuilder(t *testing.T) {
	if Get() == nil {
		t.Fatal("Get returned nil Builder")
	}
}
