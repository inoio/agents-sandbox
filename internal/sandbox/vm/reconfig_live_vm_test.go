package vm

import (
	"context"
	"errors"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

type callerContextKey struct{}

// TestReadLiveProjectVMWithoutVM pins the first-run case: no project VM exists,
// so there is nothing to read and the zero value is returned.
func TestReadLiveProjectVMWithoutVM(t *testing.T) {
	ui := termio.NewTestMock(t)

	live, err := readLiveProjectVM(context.Background(), &msb.MockMsbClient{}, testVMKey(), nil, &ui)
	if err != nil {
		t.Fatalf("readLiveProjectVM: %v", err)
	}
	if live.handle != nil || live.config != nil || live.configUnreadable || live.agentConfigFiles != nil {
		t.Errorf("readLiveProjectVM without a VM = %+v, want the zero value", live)
	}
}

// TestReadLiveProjectVMLookupError pins the fail-closed lookup: a lookup that
// cannot confirm the VM absent aborts instead of being read as "no VM".
func TestReadLiveProjectVMLookupError(t *testing.T) {
	ui := termio.NewTestMock(t)
	lookupErr := errors.New("msb unreachable")
	client := (&msb.MockMsbClient{}).SetGetSandboxErr(lookupErr)

	_, err := readLiveProjectVM(context.Background(), client, testVMKey(), nil, &ui)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("readLiveProjectVM error = %v, want %v", err, lookupErr)
	}
	if !contains(err.Error(), "locate project VM") {
		t.Errorf("readLiveProjectVM error = %v, want it to name the VM lookup", err)
	}
}

// TestReadLiveProjectVMReadsAgentConfigAndDetaches pins the one attach the
// read performs: it reads the agent config files through a connection to the
// running VM and detaches under a context derived from the caller's, not a
// fresh context.Background() (#341).
func TestReadLiveProjectVMReadsAgentConfigAndDetaches(t *testing.T) {
	ui := termio.NewTestMock(t)
	const configPath = "/home/dev/.config/opencode/opencode.json"
	var detachContexts []context.Context
	connected := &msb.MockSandbox{
		FSValue_: msb.NewTestFS(map[string][]byte{configPath: []byte(`{"model":"m"}`)}, nil),
		DetachFn: func(ctx context.Context) error {
			detachContexts = append(detachContexts, ctx)
			return nil
		},
	}
	vmConfig := &msbSdk.SandboxConfig{Image: "img:tag"}
	handle := &msb.MockSandboxHandle{Cfg: vmConfig, ConnectSb: connected}
	client := (&msb.MockMsbClient{}).SetGotSandbox(handle)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), callerContextKey{}, "caller"))
	defer cancel()

	live, err := readLiveProjectVM(ctx, client, testVMKey(), []string{configPath}, &ui)
	if err != nil {
		t.Fatalf("readLiveProjectVM: %v", err)
	}
	if live.handle != handle {
		t.Errorf("handle = %v, want the looked-up handle", live.handle)
	}
	if live.config != vmConfig || live.configUnreadable {
		t.Errorf("config = %v (unreadable %v), want the VM's readable config", live.config, live.configUnreadable)
	}
	if got := string(live.agentConfigFiles[configPath]); got != `{"model":"m"}` {
		t.Errorf("agent config file = %q, want the VM's content", got)
	}
	if len(detachContexts) != 1 {
		t.Fatalf("detached %d times, want exactly once", len(detachContexts))
	}
	if detachContexts[0].Value(callerContextKey{}) != "caller" {
		t.Error("detach did not run under a context derived from the caller's")
	}
	cancel()
	if detachContexts[0].Err() != nil {
		t.Error("detach context is cancelled with the caller's; the cleanup must outlive a cancelled run")
	}
}

// TestReadLiveProjectVMConnectFailureKeepsHandle pins a VM that cannot be
// connected (e.g. stopped): its handle and config still feed the decision, and
// only the agent config comparison is skipped.
func TestReadLiveProjectVMConnectFailureKeepsHandle(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{}, ConnectErr: errors.New("not running")}
	client := (&msb.MockMsbClient{}).SetGotSandbox(handle)

	live, err := readLiveProjectVM(context.Background(), client, testVMKey(), []string{"/x"}, &ui)
	if err != nil {
		t.Fatalf("readLiveProjectVM: %v", err)
	}
	if live.handle != handle {
		t.Errorf("handle = %v, want the looked-up handle", live.handle)
	}
	if live.agentConfigFiles != nil {
		t.Errorf("agent config files = %v, want nil when the VM cannot be connected", live.agentConfigFiles)
	}
}

// TestResolveReconfigDecisionNeedsNoVMAccess pins the split of #341: planning
// works from the facts read up front, so it runs without an msb client at all.
func TestResolveReconfigDecisionNeedsNoVMAccess(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{
		GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
			t.Fatal("resolveReconfigDecision looked up the VM; it must plan from the facts it is given")
			return nil, errors.New("unexpected VM lookup")
		},
	})
	ui := termio.NewTestMock(t)
	cfs := loadOpencodeConfigFiles(t, &ui)
	live := liveProjectVM{
		handle:           &msb.MockSandboxHandle{},
		configUnreadable: true,
	}

	decision, err := resolveReconfigDecision(
		context.Background(),
		volume.NewManager(&ui),
		testVMKey(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{Digest: "sha256:same"},
		"vol",
		state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"},
		cfs,
		envsecret.EnvSecrets{},
		live,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("an unreadable VM config read up front must still force a recreate")
	}
}

// TestPlanAndEnsureVMLookUpProjectVMOnce pins the single lookup of #341: the
// handle plan-reconfig read the decision from is the one ensure-vm attaches to.
func TestPlanAndEnsureVMLookUpProjectVMOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	connected := &msb.MockSandbox{}
	handle := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning, ConnectSb: connected}
	lookups := 0
	client := &msb.MockMsbClient{GetSandboxFn: func(context.Context, string) (msb.SandboxHandle, error) {
		lookups++
		return handle, nil
	}}
	msb.WithMsbMock(t, client)
	provisionHostConfig := false
	opts := options.RunOptions{Agent: "opencode", ProvisionHostConfig: &provisionHostConfig}
	session := sessionIdentity{key: testVMKey(), agent: opencodeAgent(t)}
	home := preparedHome{
		client:     client,
		volumes:    volume.NewManager(&ui),
		homeVolume: "vol",
		homeState:  state.HomeState{HomeVolume: "vol"},
	}

	plan, err := planReconfigStage(context.Background(), session, image.ImageInfo{}, home, false, opts, &ui)
	if err != nil {
		t.Fatalf("planReconfigStage: %v", err)
	}
	sb, boot, err := ensureVMStage(context.Background(), session, image.ImageInfo{}, home, plan, false, opts, &ui)
	if err != nil {
		t.Fatalf("ensureVMStage: %v", err)
	}
	if sb != connected || boot != vmBootConnected {
		t.Errorf("ensureVMStage = (%v, %v), want the running VM connected", sb, boot)
	}
	if lookups != 1 {
		t.Errorf("project VM looked up %d times, want once", lookups)
	}
}

// TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven pins the recreate
// target: the VM the decision was read from is destroyed, even when its name
// no longer resolves by the time ensure-vm runs.
func TestEnsureProjectVMRecreateDestroysTheHandleItIsGiven(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)
	ui := termio.NewTestMock(t)
	msb.WithMsbMock(t, &msb.MockMsbClient{})
	existing := &msb.MockSandboxHandle{Name_: "vm", Status_: msbSdk.SandboxStatusRunning}

	_, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode", Recreate: true},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		existing,
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM: %v", err)
	}
	if !existing.Destroyed {
		t.Error("recreate did not destroy the handle the decision was read from")
	}
	if boot != vmBootCreated {
		t.Errorf("boot = %v, want vmBootCreated", boot)
	}
}
