package prepare

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// TestResolveReconfigDecisionConfigError covers the branch where reading the existing
// VM config fails: the failure is surfaced as a warning and the decision is
// forced to recreate, so the run cannot silently reuse a VM whose image, env,
// or network may differ from the desired config (#339).
func TestResolveReconfigDecisionConfigError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{
		ConfigErr: errors.New("config read failed"),
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected a forced recreate when the existing VM's config cannot be read")
	}
	if !contains(joinStrings(ui.WarnCalls), "config read failed") {
		t.Errorf("expected a warning naming the config read failure, got %v", ui.WarnCalls)
	}
	if !contains(joinStrings(ui.WarnCalls), "testproj-opencode") {
		t.Errorf("expected the warning to name the VM, got %v", ui.WarnCalls)
	}
}

// TestResolveReconfigDecisionConfigErrorPromptsAttachedClients covers the attached-client
// branch of the forced recreate: with another client attached, the unreadable
// config is offered through the rebuild prompt, and choosing "keep" defers it.
func TestResolveReconfigDecisionConfigErrorPromptsAttachedClients(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	release, err := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	handle := &msb.MockSandboxHandle{
		ConfigErr: errors.New("config read failed"),
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	var prompt string
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(p string, _ []termio.Choice, _ string) (string, error) {
			prompt = p
			return "k", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected the forced recreate to be deferred when the user keeps the current VM")
	}
	if !contains(prompt, "unreadable VM config") {
		t.Errorf("rebuild prompt %q does not name the unreadable VM config", prompt)
	}
}

// TestResolveReconfigDecisionDeferredPortsKeepsServePort covers the deferred
// ports change on a kept VM: when another client is attached and the user keeps
// the current VM, the VM still serves on its existing agent binding, so the
// decision must not adopt the freshly probed serve port (that port applies at
// the next recreate).
func TestResolveReconfigDecisionDeferredPortsKeepsServePort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	release, err := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			Image:     "img:tag",
			CPUs:      4,
			MemoryMiB: 4096,
		},
		PortBindings: []msbSdk.PortBinding{
			{
				Bind:      "127.0.0.1",
				HostPort:  ports.ServeOnlyBasePort,
				GuestPort: ports.ServeOnlyBasePort,
				Protocol:  msbSdk.PortProtocolTCP,
			},
		},
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "k", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{
			Agent:     "opencode",
			ServeOnly: true,
			// The new mapping claims the agent's host port, so the plan must
			// probe a different serve port for the recreated VM.
			Ports: []ports.PortMapping{{HostPort: ports.ServeOnlyBasePort, GuestPort: 3000}},
		},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected the ports change to be deferred when the user keeps the current VM")
	}
	if !decision.PortsChanged {
		t.Error("expected PortsChanged for a mapping that claims the agent host port")
	}
	if decision.ServeHostPort != ports.ServeOnlyBasePort {
		t.Errorf("ServeHostPort = %d, want %d (the kept VM still serves on its existing binding)",
			decision.ServeHostPort, ports.ServeOnlyBasePort)
	}
}

// TestResolveReconfigDecisionRecreateAdoptsProbedServePort covers the accepted
// ports change: with no other client attached the recreate applies without a
// prompt, and the decision adopts the freshly probed serve port the recreated
// VM will bind.
func TestResolveReconfigDecisionRecreateAdoptsProbedServePort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			Image:     "img:tag",
			CPUs:      4,
			MemoryMiB: 4096,
		},
		PortBindings: []msbSdk.PortBinding{
			{
				Bind:      "127.0.0.1",
				HostPort:  ports.ServeOnlyBasePort,
				GuestPort: ports.ServeOnlyBasePort,
				Protocol:  msbSdk.PortProtocolTCP,
			},
		},
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{
			Agent:     "opencode",
			ServeOnly: true,
			Ports:     []ports.PortMapping{{HostPort: ports.ServeOnlyBasePort, GuestPort: 3000}},
		},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected an automatic recreate with no other client attached")
	}
	if decision.ServeHostPort == ports.ServeOnlyBasePort {
		t.Errorf("ServeHostPort = %d, want a freshly probed port above the user-claimed %d",
			decision.ServeHostPort, ports.ServeOnlyBasePort)
	}
}

// TestResolveReconfigDecisionUnchangedPortsNoRecreate pins the read-back
// contract for the "unchanged second run": the ports the persisted config
// records (via ConfigPorts) equal the desired bindings, so nothing reads as
// changed, the VM is not recreated, and the serve port is reused.
func TestResolveReconfigDecisionUnchangedPortsNoRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			Image:     "img:tag",
			CPUs:      4,
			MemoryMiB: 4096,
		},
		PortBindings: append(
			ports.ServeOnlyBindings(ports.ServeOnlyBasePort),
			ports.UserPortBindings([]ports.PortMapping{{HostPort: 3000, GuestPort: 3000}})...,
		),
		ConnectSb: &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())
	persisted := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:same",
		NetworkState: state.BuildNetworkState(network.Policy{}),
	}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{
			Agent:     "opencode",
			ServeOnly: true,
			Ports:     []ports.PortMapping{{HostPort: 3000, GuestPort: 3000}},
		},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.PortsChanged {
		t.Error("an unchanged second run must not read the ports as changed")
	}
	if decision.Recreate {
		t.Error("an unchanged second run must not recreate the VM")
	}
	if decision.ServeHostPort != ports.ServeOnlyBasePort {
		t.Errorf(
			"ServeHostPort = %d, want %d (the reused serve binding)",
			decision.ServeHostPort,
			ports.ServeOnlyBasePort,
		)
	}
}

// TestResolveReconfigDecisionDroppedPortsRecreate pins the other side: a run
// without user ports against a VM that has them is a change — the recreate
// drops the published ports.
func TestResolveReconfigDecisionDroppedPortsRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	handle := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{
			Image:     "img:tag",
			CPUs:      4,
			MemoryMiB: 4096,
		},
		PortBindings: ports.UserPortBindings([]ports.PortMapping{{HostPort: 3000, GuestPort: 3000}}),
		ConnectSb:    &msb.MockSandbox{},
	}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())
	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.PortsChanged {
		t.Error("a run without ports against a VM that has them must read as changed")
	}
}

// TestResolveReconfigDecisionFindProjectVMError covers the fail-closed branch where the
// VM lookup itself fails: resolveReconfigDecision returns the error instead of treating
// the failure as "no VM" and silently skipping reconfiguration (#339).
func TestResolveReconfigDecisionFindProjectVMError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.SetGetSandboxErr(errors.New("msb unreachable"))
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:same"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"},
		cfs,
		&ui,
	)
	if err == nil {
		t.Fatal("expected an error when the VM lookup fails")
	}
	if !contains(err.Error(), "locate project VM") {
		t.Errorf("error = %v, want it to mention locating the project VM", err)
	}
}

// TestResolveReconfigDecisionNoVMSkipsRecreate guards against over-triggering: with no VM
// present, a changed setting must not force a recreate (the VM is created fresh
// with the desired config anyway).
func TestResolveReconfigDecisionNoVMSkipsRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no VM"}
	}
	msb.WithMsbMock(t, mock)

	ui := termio.NewTestMock(t)
	volumes := volume.NewManager(docker.Get())

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{
			Agent:   "opencode",
			Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}},
		},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:new"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"},
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate when no VM exists, even with changed settings")
	}
}

// TestResolveReconfigDecisionQuitAtRecreatePrompt covers the branch where ResolveReconfig
// reports an error: with another client attached, an image change prompts the
// user, and choosing "quit" aborts the reconfig with an error.
func TestResolveReconfigDecisionQuitAtRecreatePrompt(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.MustCheckoutSlug()

	release, err := state.AcquireClientLease(state.Key{Slug: slug, Agent: "opencode"})
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			return "q", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	_, err = readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when the user quits the rebuild prompt")
	}
}
