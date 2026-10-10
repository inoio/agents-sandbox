package builder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func applySandboxOpts(cfg *msbSdk.SandboxConfig, opts []msbSdk.SandboxOption) {
	for _, opt := range opts {
		opt(cfg)
	}
}

func TestEnsureCreatesBuilderVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	var createdVolumes []string
	shellCmds := []string{}
	client := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: builderSandboxName()}
		},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			createdVolumes = append(createdVolumes, name)
			return &msb.MockVolumeHandle{Name_: name}, nil
		},
		CreatedSandbox: msb.NewMockSandbox(msb.SandboxOpts{ShellStreamCmds: &shellCmds}),
	}

	var readyPort int
	vm := &VM{
		Msb: client,
		Ready: func(_ context.Context, hostPort int) error {
			readyPort = hostPort
			return nil
		},
	}

	port, err := vm.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if port < builderHostPortBase {
		t.Errorf("Ensure returned port %d below base %d", port, builderHostPortBase)
	}
	if readyPort != port {
		t.Errorf("Ready called with port %d, want %d", readyPort, port)
	}

	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected 1 CreateSandbox call, got %d", len(client.CreatedSandboxCalls))
	}
	call := client.CreatedSandboxCalls[0]
	if call.Name != builderSandboxName() {
		t.Errorf("created sandbox name = %q, want %q", call.Name, builderSandboxName())
	}

	var cfg msbSdk.SandboxConfig
	applySandboxOpts(&cfg, call.Opts)
	if !cfg.Detached {
		t.Error("expected detached sandbox")
	}
	mount, ok := cfg.Volumes[cacheMountPath]
	if !ok {
		t.Fatalf("expected mount at %q, got %v", cacheMountPath, cfg.Volumes)
	}
	if mount.Named != cacheVolumeName {
		t.Errorf("mount %q named volume = %q, want %q", cacheMountPath, mount.Named, cacheVolumeName)
	}
	if len(cfg.PortBindings) != 1 {
		t.Fatalf("expected 1 port binding, got %d", len(cfg.PortBindings))
	}
	binding := cfg.PortBindings[0]
	if binding.Bind != "127.0.0.1" {
		t.Errorf("port binding addr = %q, want 127.0.0.1", binding.Bind)
	}
	if int(binding.HostPort) != port {
		t.Errorf("port binding host port = %d, want %d", binding.HostPort, port)
	}
	if binding.GuestPort != builderGuestPort {
		t.Errorf("port binding guest port = %d, want %d", binding.GuestPort, builderGuestPort)
	}

	if len(createdVolumes) != 1 || createdVolumes[0] != cacheVolumeName {
		t.Errorf("created volumes = %v, want [%s]", createdVolumes, cacheVolumeName)
	}
	if len(shellCmds) != 1 || shellCmds[0] != buildkitdCommand {
		t.Errorf("shell stream commands = %v, want [%s]", shellCmds, buildkitdCommand)
	}
}

func TestEnsureReusesExistingBuilderVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const existingHostPort = 4242
	handle := &msb.MockSandboxHandle{
		Name_: builderSandboxName(),
		Cfg: &msbSdk.SandboxConfig{
			PortBindings: []msbSdk.PortBinding{
				{Bind: "127.0.0.1", HostPort: existingHostPort, GuestPort: builderGuestPort},
			},
		},
	}
	client := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return handle, nil
		},
		CreateVolumeFn: func(_ context.Context, name string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
			return nil, fmt.Errorf("CreateVolume must not be called for an existing builder VM (called with %q)", name)
		},
	}

	readyPort := -1
	vm := &VM{
		Msb: client,
		Ready: func(_ context.Context, hostPort int) error {
			readyPort = hostPort
			return nil
		},
	}

	port, err := vm.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if port != existingHostPort {
		t.Errorf("Ensure returned port %d, want %d", port, existingHostPort)
	}
	if readyPort != existingHostPort {
		t.Errorf("Ready called with port %d, want %d", readyPort, existingHostPort)
	}
	if len(client.CreatedSandboxCalls) != 0 {
		t.Errorf("expected no CreateSandbox call, got %d", len(client.CreatedSandboxCalls))
	}
}

func TestEnsureSerializesConcurrentCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	var created atomic.Bool
	handle := &msb.MockSandboxHandle{Name_: builderSandboxName()}
	shellCmds := []string{}

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(context.Context, string) (msb.SandboxHandle, error) {
		if created.Load() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: builderSandboxName()}
	}
	client.CreateSandboxFn = func(_ context.Context, name string, opts ...msbSdk.SandboxOption) (msb.Sandbox, error) {
		var cfg msbSdk.SandboxConfig
		applySandboxOpts(&cfg, opts)
		handle.Cfg = &cfg
		created.Store(true)
		return msb.NewMockSandbox(msb.SandboxOpts{ShellStreamCmds: &shellCmds}), nil
	}

	vm := &VM{
		Msb:   client,
		Ready: func(context.Context, int) error { return nil },
	}

	const callers = 2
	ports := make([]int, callers)
	errs := make([]error, callers)
	var atStart sync.WaitGroup
	atStart.Add(callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			atStart.Done()
			<-start
			ports[i], errs[i] = vm.Ensure(context.Background())
		}(i)
	}
	atStart.Wait()
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Ensure[%d] failed: %v", i, err)
		}
	}
	if len(client.CreatedSandboxCalls) != 1 {
		t.Fatalf("expected exactly 1 CreateSandbox call (flock serialization), got %d", len(client.CreatedSandboxCalls))
	}
	if ports[0] != ports[1] {
		t.Errorf("expected both callers to receive the same port, got %d and %d", ports[0], ports[1])
	}
	if ports[0] < builderHostPortBase {
		t.Errorf("port %d below base %d", ports[0], builderHostPortBase)
	}
}

func TestEnsureReadinessRunsOutsideFlock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{
		Name_: builderSandboxName(),
		Cfg: &msbSdk.SandboxConfig{
			PortBindings: []msbSdk.PortBinding{
				{Bind: "127.0.0.1", HostPort: 4242, GuestPort: builderGuestPort},
			},
		},
	}
	client := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return handle, nil
		},
	}

	var entered sync.WaitGroup
	entered.Add(2)
	bothReady := make(chan struct{})
	go func() {
		entered.Wait()
		close(bothReady)
	}()
	vm := &VM{
		Msb: client,
		Ready: func(ctx context.Context, _ int) error {
			entered.Done()
			select {
			case <-bothReady:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("readiness ran serially; the builder flock was held during Ready")
			}
		},
	}

	const callers = 2
	errs := make([]error, callers)
	var atStart sync.WaitGroup
	atStart.Add(callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			atStart.Done()
			<-start
			_, errs[i] = vm.Ensure(context.Background())
		}(i)
	}
	atStart.Wait()
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Ensure[%d] failed: %v", i, err)
		}
	}
}

func TestBuilderSandboxNameIncludesVersion(t *testing.T) {
	want := "agents-sandbox-buildkit-" + buildkitVersion
	if got := builderSandboxName(); got != want {
		t.Errorf("builderSandboxName() = %q, want %q", got, want)
	}
}

func TestPruneCacheSkipsWhenNoBuilderVM(t *testing.T) {
	var queried string
	client := &msb.MockMsbClient{
		GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
			queried = name
			return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
		},
	}
	vm := &VM{Msb: client}

	if err := vm.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	if queried != builderSandboxName() {
		t.Errorf("GetSandbox queried %q, want %q", queried, builderSandboxName())
	}
}

func TestPruneCacheRunsBuildctl(t *testing.T) {
	shellCmds := []string{}
	sb := &msb.MockSandbox{ShellCalls: &shellCmds}
	client := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{Name_: builderSandboxName(), ConnectSb: sb}, nil
		},
	}
	vm := &VM{Msb: client}

	if err := vm.PruneCache(context.Background()); err != nil {
		t.Fatalf("PruneCache: %v", err)
	}
	want := "buildctl --addr tcp://127.0.0.1:1234 prune --filter until=24h --all"
	if len(shellCmds) != 1 || shellCmds[0] != want {
		t.Errorf("Shell calls = %v, want [%q]", shellCmds, want)
	}
}

func TestPruneCacheErrorsOnFailedPrune(t *testing.T) {
	sb := &msb.MockSandbox{ShellErr: errors.New("buildctl boom")}
	client := &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			return &msb.MockSandboxHandle{Name_: builderSandboxName(), ConnectSb: sb}, nil
		},
	}
	vm := &VM{Msb: client}

	if err := vm.PruneCache(context.Background()); err == nil {
		t.Fatal("expected an error when buildctl prune fails")
	}
}
