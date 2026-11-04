package prepare

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

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
	assertVerbose(t, &ui, "connecting to existing VM failed: not running")
}

// TestResolveReconfigDecisionRecreatesWhenVMConfigIsUnreadable pins that an
// unreadable existing VM config is warned about and forces a recreate, so the
// run cannot silently reuse a VM whose image, env, or network may differ from
// the desired config.
func TestResolveReconfigDecisionRecreatesWhenVMConfigIsUnreadable(t *testing.T) {
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

// TestResolveReconfigDecisionOffersUnreadableVMConfigToAttachedClients pins
// that with another client attached the forced recreate goes through the
// rebuild prompt, and choosing "keep" defers it.
func TestResolveReconfigDecisionOffersUnreadableVMConfigToAttachedClients(t *testing.T) {
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

// TestResolveReconfigDecisionFailsWhenVMLookupFails pins that a failing VM
// lookup is returned instead of being treated as "no VM", which would silently
// skip reconfiguration.
func TestResolveReconfigDecisionFailsWhenVMLookupFails(t *testing.T) {
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

// TestResolveReconfigDecisionQuitAtRecreatePrompt pins that with another client
// attached an image change prompts the user, and choosing "quit" aborts with an
// error.
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

// TestResolveReconfigDecisionLogsDetachFailure pins that a failing detach from
// the live VM is verbose-logged and does not fail the decision.
func TestResolveReconfigDecisionLogsDetachFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	liveSb := &msb.MockSandbox{DetachErr: errors.New("detach failed")}
	sh.ConnectSb = liveSb
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	volumes := volume.NewManager(docker.Get())
	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:same"}

	ui := termio.NewTestMock(t)
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
		persisted,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !contains(joinStrings(ui.VerboseCalls), "failed to detach") {
		t.Errorf("expected a verbose message about the failed detach, got %v", ui.VerboseCalls)
	}
}

// TestResolveReconfigDecisionRecreatesOnContentIDSwitch pins the structured
// image identity end to end: a live VM whose persisted content reference differs
// from the desired identity recreates the VM even when the persisted image
// digest already matches. That is the state a failed replacement leaves behind —
// the image-change home action records the new digest before the VM is replaced
// — so the content id is the only remaining signal that the live VM is still on
// the old image (specs/image-ref-content-addressing.md).
func TestResolveReconfigDecisionRecreatesOnContentIDSwitch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const slug = "testproj"
	const desiredContentID = "9f2c1a7b4d6e0f31"
	f := newReconfigFixtureForImage(
		t,
		naming.ContentImageRef(slug, "opencode", "0123456789abcdef"),
		launcherLiveMounts(1024, 16384, "vol"),
		nil,
	)

	// The persisted digest already equals the desired one, so imageChanged is
	// false and only the content id can drive the recreate.
	persisted := matchingHomeState(network.Policy{})
	info := image.ImageInfo{
		Ref:      naming.ContentImageRef(slug, "opencode", desiredContentID),
		Identity: naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: desiredContentID},
		Digest:   persisted.ImageDigest,
	}

	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		f.client,
		f.volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		info,
		f.homeVol,
		persisted,
		f.cfs,
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected a recreate when the persisted content id differs from the desired one")
	}
	assertRebuildReason(t, f.ui, imageChangeLabel)
}

// TestResolveReconfigDecisionReusesVMOnSameContentIdentity pins the tolerance
// side: a live VM persisted under the name spelling of the desired identity is
// reused, so the content-addressing change alone never recreates an existing VM.
func TestResolveReconfigDecisionReusesVMOnSameContentIdentity(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const slug = "testproj"
	const contentID = "9f2c1a7b4d6e0f31"
	f := newReconfigFixtureForImage(
		t,
		naming.RunnerImageRef(slug, "opencode"),
		launcherLiveMounts(1024, 16384, "vol"),
		nil,
	)

	persisted := matchingHomeState(network.Policy{})
	info := image.ImageInfo{
		Ref:      naming.ContentImageRef(slug, "opencode", contentID),
		Identity: naming.ImageIdentity{Slug: slug, Agent: "opencode", ContentID: contentID},
		Digest:   persisted.ImageDigest,
	}

	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		f.client,
		f.volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		info,
		f.homeVol,
		persisted,
		f.cfs,
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Errorf("expected no recreate for the name spelling of the desired image, got %+v", f.ui.VerboseCalls)
	}
}

// TestResolveReconfigDecisionAgentConfigDrivesRestart pins the agent config
// comparison against the files read from the VM: a differing file restarts the
// daemon, a matching one or none read (VM not connected) does not.
func TestResolveReconfigDecisionAgentConfigDrivesRestart(t *testing.T) {
	const mirrorPath = "/home/dev/.config/opencode/AGENTS.md"
	tests := []struct {
		name             string
		agentConfigFiles map[string][]byte
		wantRestart      bool
	}{
		{"differing file", map[string][]byte{mirrorPath: []byte("old")}, true},
		{"matching file", map[string][]byte{mirrorPath: []byte("new")}, false},
		{"nothing read", nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			ui := termio.NewTestMock(t)
			configFiles := &reprovision.ConfigFiles{
				Mirror: map[string][]byte{mirrorPath: []byte("new")},
				Keys:   []string{mirrorPath},
			}
			persisted := matchingHomeState(network.Policy{})
			live := liveProjectVM{
				handle: &msb.MockSandboxHandle{},
				config: &msbSdk.SandboxConfig{
					Image:     "img:tag",
					CPUs:      4,
					MemoryMiB: 4096,
					Volumes:   launcherLiveMounts(1024, 16384, "vol"),
				},
				agentConfigFiles: test.agentConfigFiles,
			}

			decision, err := resolveReconfigDecision(
				context.Background(),
				volume.NewManager(docker.Get()),
				testVMKey(),
				options.RunOptions{Agent: "opencode"},
				image.ImageInfo{Ref: "img:tag", Digest: persisted.ImageDigest},
				"vol",
				persisted,
				configFiles,
				envsecret.EnvSecrets{},
				live,
				&ui,
			)
			if err != nil {
				t.Fatalf("resolveReconfigDecision: %v", err)
			}
			if decision.Recreate {
				t.Fatalf("decision recreates (%v); only the agent config may differ in this test", ui.VerboseCalls)
			}
			if decision.Restart != test.wantRestart {
				t.Errorf("Restart = %v, want %v", decision.Restart, test.wantRestart)
			}
		})
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
		volume.NewManager(docker.Get()),
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

// TestResolveReconfigDecisionNoHomeActionOnUnchangedDigest pins that the
// home-volume action is resolved only when the image digest changed: a rebuild
// driven by a non-image change must leave HomeAction nil, so no home-volume
// prompt or state write happens for it.
func TestResolveReconfigDecisionNoHomeActionOnUnchangedDigest(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "4G"} // the tmp-size change forces a recreate
	f := newReconfigFixture(t, launcherLiveMounts(1024, 16384, "vol"), nil)
	hs := matchingHomeState(opts.Network) // the seeded digest matches the run's image

	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		f.client,
		f.volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: hs.ImageDigest},
		f.homeVol,
		hs,
		f.cfs,
		f.ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Fatal("expected a recreate for the tmp-size change")
	}
	if decision.HomeAction != nil {
		t.Error("expected no home-volume action when the image digest is unchanged")
	}
}

func TestResolveReconfigDecision_NetworkChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	volumes := volume.NewManager(docker.Get())

	// Zero network state (no policy recorded yet) + a non-empty desired policy
	// => recreate to apply the policy.
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
	}
	opts := options.RunOptions{Network: network.Policy{Profiles: []network.Profile{network.ProfileNone}}}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when network policy differs from persisted state (network cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for network change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_NetworkUnchangedNoRecreate(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)

	volumes := volume.NewManager(docker.Get())

	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	persistedState := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:samedigest",
		NetworkState: state.BuildNetworkState(policy),
	}
	opts := options.RunOptions{Network: policy}

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		opts,
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate when network policy matches persisted state")
	}
	if decision.Restart {
		t.Error("expected no restart when network policy matches persisted state")
	}
}

func TestResolveReconfigDecision_EnvChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Set up handle so reconfig.PlanReconfig gets a non-nil cfg
	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	volumes := volume.NewManager(docker.Get())

	// Write state with env hash that differs from desired (nil desired = empty env)
	// Use matching imageDigest to skip home-volume path
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		EnvState: state.EnvState{
			Hash:  "sha256:differenthash",
			Names: []string{"OLD_KEY"},
		},
	}

	ui := termio.NewTestMock(t)
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
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when env differs from persisted state (env cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for env change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_EnvUnchangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	volumes := volume.NewManager(docker.Get())

	// Write env file and state with matching hash
	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "FOO=bar\n")
	envHash := computeEnvHash(filepath.Join(userDir, configpaths.EnvFileName))

	// Build HomeState with matching env state
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img1",
		EnvState: state.EnvState{
			Hash:  envHash,
			Names: []string{"FOO"},
		},
	}

	ui := termio.NewTestMock(t)
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
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when env matches persisted state")
	}
}

func TestResolveReconfigDecision_SecretsChangedWithPersistedState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	volumes := volume.NewManager(docker.Get())

	// Write state with different secret hash than desired (nil desired = no secrets)
	// Use matching imageDigest to skip home-volume path
	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:samedigest",
		SecretState: state.SecretState{
			Hash:  "sha256:oldsecrethash",
			Names: []string{"DB_PASS"},
		},
	}

	ui := termio.NewTestMock(t)
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
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !decision.Recreate {
		t.Error("expected recreate when secrets differ from persisted state (secrets cannot be applied live)")
	}
	if decision.Restart {
		t.Error("expected no daemon restart for secrets change (folded into recreate)")
	}
}

func TestResolveReconfigDecision_ZeroPersistedStateNoSpuriousChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	volumes := volume.NewManager(docker.Get())

	// Write empty env file → desired is empty.
	// State has no env_state → zero value (first run).
	// Zero applied + empty desired → NOT changed (per ruling)
	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "# nothing here\n")

	ui := termio.NewTestMock(t)
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
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
		"vol",
		state.HomeState{},
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when no env is configured (zero+empty)")
	}
}

func TestResolveReconfigDecision_PersistedSecretsMatchDesired(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	userDir := t.TempDir()

	mock := &msb.MockMsbClient{}
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	volumes := volume.NewManager(docker.Get())

	testutil.WriteFile(t, userDir, configpaths.EnvFileName, "K=V\n")
	desiredEnv := envsecret.MergeEnvMaps(envsecret.BuildEnvMap(filepath.Join(userDir, configpaths.EnvFileName)))
	envHash := state.EnvContentHash(desiredEnv)

	seedHomeState(state.Key{Slug: "myproj5", Agent: "opencode"}, state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: envHash, Names: []string{"K"}},
	})

	persistedState := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:img",
		EnvState:    state.EnvState{Hash: envHash, Names: []string{"K"}},
	}

	ui := termio.NewTestMock(t)
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
		image.ImageInfo{DockerTag: "img", Ref: "img", Digest: "sha256:img"},
		"vol",
		persistedState,
		cfs,
		&ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if decision.Recreate {
		t.Error("expected no recreate")
	}
	if decision.Restart {
		t.Error("expected no restart when env matches persisted state and no secrets")
	}
}

func TestResolveReconfigDecision_HomePromptDeferredWhenRebuildDeferred(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	key := state.Key{Slug: "testproj", Agent: "opencode"}
	release, err := state.AcquireClientLease(key)
	if err != nil {
		t.Fatalf("AcquireClientLease: %v", err)
	}
	defer release()

	reconfigPrompted := false
	homePrompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			switch {
			case strings.Contains(prompt, "Applying requires rebuilding"):
				reconfigPrompted = true
				return "k", nil
			case strings.Contains(prompt, "Docker image changed"):
				homePrompted = true
			}
			return "", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	imageInfo := image.ImageInfo{
		DockerTag: "img:tag",
		Ref:       "img:tag",
		// imageRef == cfg.Image; the digest still identifies the image
		Digest: "sha256:new",
	} // stored digest differs -> image-triggered rebuild
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		key,
		options.RunOptions{Agent: "opencode"},
		imageInfo,
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	homeVol, err := applyReconfigHomeAction(
		context.Background(), mock, volumes, key, "vol", imageInfo,
		false, decision, ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if !reconfigPrompted {
		t.Error("expected VM-recreate prompt when another client is active")
	}
	if homePrompted {
		t.Error("expected home-volume prompt to be deferred with the VM rebuild")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want unchanged %q", homeVol, "vol")
	}
	if decision.Recreate {
		t.Error("expected VM rebuild to be deferred when another client keeps the current VM")
	}
	if decision.Restart {
		t.Error("expected no restart")
	}
	if joined := strings.Join(ui.VerboseCalls, "\n"); !strings.Contains(joined, "image digest changed") {
		t.Errorf("verbose calls = %v, want the image-digest change reason", ui.VerboseCalls)
	}
}

func TestResolveReconfigDecision_HomePromptAskedWhenRebuildConfirmed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	msb.WithMsbMock(t, mock)
	configpaths.WithMockConfigPaths(t)

	prompted := false
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if strings.Contains(prompt, "Docker image changed") {
				prompted = true
				return "k", nil // keep volume
			}
			return "", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}
	slug := git.MustCheckoutSlug()
	seedHomeState(state.Key{Slug: slug, Agent: "opencode"}, persisted)

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	imageInfo := image.ImageInfo{
		DockerTag: "img:new",
		Ref:       "img:new",
		// imageRef != cfg.Image("img:tag") -> image-triggered rebuild
		Digest: "sha256:new",
	} // stored digest differs -> image change detected
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: slug, Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		imageInfo,
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	homeVol, err := applyReconfigHomeAction(
		context.Background(), mock, volumes, state.Key{Slug: slug, Agent: "opencode"}, "vol", imageInfo,
		false, decision, ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if !prompted {
		t.Error("expected home-volume prompt to fire when the rebuild onto the new image is confirmed")
	}
	if !decision.Recreate {
		t.Error("expected recreate when switching to the new image")
	}
	if decision.Restart {
		t.Error("expected no restart (env/secret/config unchanged)")
	}
	if homeVol != "vol" {
		t.Errorf("homeVol = %q, want %q (keep chosen)", homeVol, "vol")
	}

	st, rerr := state.ReadState(state.Key{Slug: slug, Agent: "opencode"})
	if rerr != nil {
		t.Fatalf("ReadState: %v", rerr)
	}
	if st.ImageDigest != "sha256:new" {
		t.Errorf("state ImageDigest = %q, want %q (keep records the new digest)", st.ImageDigest, "sha256:new")
	}
}

func TestResolveReconfigDecision_OpenCodeConfigChanged_StoppedVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Desired opencode config: a snippet producing {"model":"y"} (new config).
	testutil.WriteFile(t,
		configpaths.CreateDir(filepath.Join(configpaths.Get().UserConfigDir(), opencodeAgent(t).ConfigDirName())),
		"a.json",
		`{"model":"y"}`,
	)

	// Existing VM whose Connect fails (stopped/suspended VM):
	// resolveReconfigDecision cannot read live config, so it must neither
	// recreate nor spuriously flag a daemon restart. Provisioning + fresh daemon
	// pickup happen downstream in vm.SetUpSandbox once the VM is started.
	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	sh.ConnectErr = errors.New("vm not running")
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	volumes := volume.NewManager(docker.Get())
	persisted := state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:same",
		NetworkState: state.BuildNetworkState(network.Policy{}),
	}

	ui := termio.NewTestMock(t)
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
	if decision.Recreate {
		t.Error("expected no recreate for an opencode-config-only change on a stopped VM")
	}
	if decision.Restart {
		t.Error("expected no daemon-restart flag from a stopped VM (fresh daemon picks up config on start)")
	}
}

// TestApplyReconfigHomeActionError pins that a failing home-volume action
// aborts the reconfig with that error.
func TestApplyReconfigHomeActionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	mock := reconfigMockClient()
	createErr := errors.New("create volume failed")
	mock.CreateVolumeFn = func(_ context.Context, _ string, _ ...msbSdk.VolumeOption) (msb.VolumeHandle, error) {
		return nil, createErr
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if contains(prompt, "Docker image changed") {
				return "m", nil // migrate -> triggers ApplyHomeAction -> CreateVolume
			}
			return "", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{HomeVolume: "vol", ImageDigest: "sha256:old"}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	_, err = applyReconfigHomeAction(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"vol",
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		false,
		decision,
		ui,
	)
	if err == nil {
		t.Fatal("expected an error when applying the home-volume action fails")
	}
	if !contains(err.Error(), "apply home action") {
		t.Errorf("error = %v, want it to mention the home action", err)
	}
	if !errors.Is(err, createErr) {
		t.Errorf("error = %v, want it to wrap the create-volume failure", err)
	}
}

// TestApplyReconfigHomeActionReturnsNewVolume pins that a migrate action
// returns the freshly created home volume, not the old one, so the run mounts
// what the action produced.
func TestApplyReconfigHomeActionReturnsNewVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	docker.WithNoopDockerMock(t)

	const oldVolume = "home-applyproj-old"
	key := state.Key{Slug: "applyproj", Agent: "opencode"}
	if err := seedHomeState(key, state.HomeState{HomeVolume: oldVolume, ImageDigest: "sha256:old"}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}

	action := volume.ActionMigrate
	decision := reconfigDecision{Recreate: true, HomeAction: &action}

	ui := termio.NewTestMock(t)
	newVolume, err := applyReconfigHomeAction(
		context.Background(),
		&msb.MockMsbClient{},
		volume.NewManager(docker.Get()),
		key,
		oldVolume,
		image.ImageInfo{DockerTag: "img-tag", Ref: "img-tag", Digest: "sha256:new"},
		false,
		decision,
		&ui,
	)
	if err != nil {
		t.Fatalf("applyReconfigHomeAction: %v", err)
	}
	if newVolume == "" || newVolume == oldVolume {
		t.Fatalf("newVolume = %q, want a fresh volume differing from %q", newVolume, oldVolume)
	}
}

// TestApplyReconfigHomeActionQuit pins that answering the home-volume prompt
// with "quit" makes applyReconfigHomeAction abort the session with a
// vm.ExitError.
func TestApplyReconfigHomeActionQuit(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sh := &msb.MockSandboxHandle{
		Cfg: &msbSdk.SandboxConfig{Image: "img:tag", CPUs: 4, MemoryMiB: 4096},
	}
	sh.ConnectSb = &msb.MockSandbox{}
	mock := &msb.MockMsbClient{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	msb.WithMsbMock(t, mock)

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(prompt string, _ []termio.Choice, _ string) (string, error) {
			if contains(prompt, "Docker image changed") {
				return "q", nil
			}
			return "", nil
		},
	}
	volumes := volume.NewManager(docker.Get())

	persisted := state.HomeState{
		HomeVolume:  "vol",
		ImageDigest: "sha256:old",
	}

	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	decision, err := readAndResolveReconfigDecision(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		"vol",
		persisted,
		cfs,
		ui,
	)
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	_, err = applyReconfigHomeAction(
		context.Background(),
		mock,
		volumes,
		state.Key{Slug: "testproj", Agent: "opencode"},
		"vol",
		image.ImageInfo{DockerTag: "img:new", Ref: "img:new", Digest: "sha256:new"},
		false,
		decision,
		ui,
	)
	if err == nil {
		t.Fatal("expected a vm.ExitError when the user quits the home-volume prompt")
	}
	var exitErr *vm.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *vm.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.Code)
	}
	if !contains(joinStrings(ui.InfoCalls), "exiting as requested by user") {
		t.Errorf("expected the quit to log the exit notice, got %v", ui.InfoCalls)
	}
}

func TestResolveReconfigDecisionDetectsTmpSizeChangeFromLiveMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "4G"}
	f := newReconfigFixture(t, launcherLiveMounts(1024, 16384, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate for a tmp size change read from the live VM's mounts")
	}
	assertRebuildReason(t, f.ui, "/tmp tmpfs size")
}

func TestResolveReconfigDecisionNoRecreateWhenLiveMountsMatch(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 16384, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if recreate {
		t.Error("expected no recreate when the live mounts already match the options")
	}
	for _, line := range f.ui.VerboseCalls {
		if strings.Contains(line, "rebuild reasons:") {
			t.Errorf("expected no rebuild reason, got %q", line)
		}
	}
}

func TestResolveReconfigDecisionRecreatesOnWorkspaceQuotaChange(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "32G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 8192, "vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate for a workspace quota change read from the live VM's mounts")
	}
	assertRebuildReason(t, f.ui, "/workspace write quota")
}

// TestResolveReconfigDecisionRecreatesOnHomeVolumeChangeFromLiveMounts pins
// that a live VM still mounting the previous home volume is recreated when home
// resolution points at a different one.
func TestResolveReconfigDecisionRecreatesOnHomeVolumeChangeFromLiveMounts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, launcherLiveMounts(2048, 16384, "old-vol"), nil)

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v", err)
	}
	if !recreate {
		t.Error("expected a recreate when the live home volume differs from the resolved one")
	}
	assertRebuildReason(t, f.ui, "home volume")
}

func TestResolveReconfigDecisionMountsReadFailureDegrades(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	opts := options.RunOptions{TmpSize: "2G", WorkspaceQuota: "16G"}
	f := newReconfigFixture(t, nil, errors.New("mounts boom"))

	recreate, err := f.decide(t, opts, matchingHomeState(opts.Network))
	if err != nil {
		t.Fatalf("resolveReconfigDecision: %v, want a degraded decision instead of aborting the startup", err)
	}
	if recreate {
		t.Error("expected no recreate when the mounts cannot be read")
	}
	assertVerbose(t, f.ui, "reading VM mounts failed")
}

// loadOpencodeConfigFiles loads opencode's config files from an empty host
// config directory.
func loadOpencodeConfigFiles(t *testing.T, ui termio.UI) *reprovision.ConfigFiles {
	t.Helper()
	configFiles, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	return configFiles
}

// imageChangeLabel is the plan's image change label; assertRebuildReason matches
// on the label text reported at verbose level.
const imageChangeLabel = "image"

type callerContextKey struct{}

// newReconfigFixture builds a project VM whose persisted config carries the
// given mounts. The mounts are seeded through the seam (ConfigMounts) rather
// than Cfg.Volumes, because that is how a live VM's mounts reach the decision.
func newReconfigFixture(t *testing.T, liveMounts map[string]msbSdk.MountConfig, mountsErr error) reconfigFixture {
	t.Helper()
	return newReconfigFixtureForImage(t, "img:tag", liveMounts, mountsErr)
}

// newReconfigFixtureForImage is newReconfigFixture with an explicit persisted
// runner-image reference, so a test can drive the image-identity comparison.
func newReconfigFixtureForImage(
	t *testing.T,
	cfgImage string,
	liveMounts map[string]msbSdk.MountConfig,
	mountsErr error,
) reconfigFixture {
	t.Helper()

	handle := &msb.MockSandboxHandle{
		Cfg:       &msbSdk.SandboxConfig{Image: cfgImage, CPUs: 4, MemoryMiB: 4096},
		Mounts:    liveMounts,
		MountsErr: mountsErr,
	}
	handle.ConnectSb = &msb.MockSandbox{}
	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}
	msb.WithMsbMock(t, client)

	ui := termio.NewTestMock(t)
	cfs, err := reprovision.LoadConfigFilesForHost(opencodeResolved(t), t.TempDir(), reprovision.VMHomeDir, &ui, true)
	if err != nil {
		t.Fatalf("LoadConfigFiles: %v", err)
	}
	return reconfigFixture{
		client:  client,
		volumes: volume.NewManager(docker.Get()),
		ui:      &ui,
		cfs:     cfs,
		homeVol: "vol",
	}
}

// launcherLiveMounts is the mount set buildMounts creates: a tmpfs /tmp, a
// quota-bound /workspace bind, and the named home volume.
func launcherLiveMounts(tmpSizeMiB, workspaceQuotaMiB uint32, homeVolume string) map[string]msbSdk.MountConfig {
	return map[string]msbSdk.MountConfig{
		mounts.TmpMountPath:       msbSdk.Mount.Tmpfs(msbSdk.TmpfsOptions{SizeMiB: tmpSizeMiB}),
		mounts.WorkspaceMountPath: msbSdk.Mount.Bind("/host/repo", msbSdk.MountOptions{QuotaMiB: workspaceQuotaMiB}),
		mounts.VMHomeDir:          msbSdk.Mount.Named(homeVolume, msbSdk.MountOptions{}),
	}
}

// matchingHomeState is a persisted state that agrees with the live VM, so only
// the mounts can drive the decision. NetworkState must be seeded explicitly:
// an empty one always reads as "policy changed" and would recreate on its own.
func matchingHomeState(networkPolicy network.Policy) state.HomeState {
	return state.HomeState{
		HomeVolume:   "vol",
		ImageDigest:  "sha256:new",
		NetworkState: state.BuildNetworkState(networkPolicy),
	}
}

// assertRebuildReason fails unless the alone-rebuild log names want, proving the
// expected mount detector drove the recreate rather than some unrelated change.
func assertRebuildReason(t *testing.T, ui *termio.Mock, want string) {
	t.Helper()
	for _, line := range ui.VerboseCalls {
		if strings.Contains(line, "rebuild reasons:") && strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("rebuild reasons %v do not name %q", ui.VerboseCalls, want)
}

// assertVerbose fails unless one verbose line contains want.
func assertVerbose(t *testing.T, ui *termio.Mock, want string) {
	t.Helper()
	for _, line := range ui.VerboseCalls {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("verbose calls %v do not contain %q", ui.VerboseCalls, want)
}

func reconfigMockClient() *msb.MockMsbClient {
	mock := &msb.MockMsbClient{}
	sh := &msb.MockSandboxHandle{Cfg: &msbSdk.SandboxConfig{
		Image: "img:tag", CPUs: 4, MemoryMiB: 4096,
	}}
	sh.ConnectSb = &msb.MockSandbox{}
	mock.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return sh, nil
	}
	return mock
}

func computeEnvHash(envFile string) string {
	env := envsecret.BuildEnvMap(envFile)
	return state.EnvContentHash(env)
}
