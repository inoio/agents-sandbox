package prepare

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/sandbox/pressure"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

// TestResolveReconfigDecisionRecreatesWhenARecordedFingerprintDiffers pins how
// the env, secret and network fingerprints recorded for a running VM drive the
// decision: none of them can be applied live, so any difference recreates the
// VM (never a daemon restart), and matching fingerprints reuse it. Each case
// starts from a state matching the desired config and changes one fingerprint.
func TestResolveReconfigDecisionRecreatesWhenARecordedFingerprintDiffers(t *testing.T) {
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	desired := envsecret.EnvSecrets{
		Vars:    map[string]string{"FOO": "bar"},
		Secrets: []msbSdk.SecretEntry{{EnvVar: "TOKEN", Value: "secret"}},
	}
	matching := func() state.HomeState {
		return state.HomeState{
			HomeVolume:   "vol",
			ImageDigest:  "sha256:samedigest",
			EnvState:     state.BuildEnvState(desired.Vars),
			SecretState:  state.BuildSecretState(desired.Secrets),
			NetworkState: state.BuildNetworkState(policy),
		}
	}
	cases := []struct {
		name         string
		recorded     func() state.HomeState
		desired      envsecret.EnvSecrets
		wantRecreate bool
	}{
		{name: "all fingerprints match", recorded: matching, desired: desired},
		{
			name: "nothing recorded and no env or secrets configured",
			recorded: func() state.HomeState {
				recorded := matching()
				recorded.EnvState, recorded.SecretState = state.EnvState{}, state.SecretState{}
				return recorded
			},
			desired: envsecret.EnvSecrets{},
		},
		{
			name: "env differs",
			recorded: func() state.HomeState {
				recorded := matching()
				recorded.EnvState = state.EnvState{Hash: "sha256:differenthash", Names: []string{"OLD_KEY"}}
				return recorded
			},
			desired:      desired,
			wantRecreate: true,
		},
		{
			name: "secrets differ",
			recorded: func() state.HomeState {
				recorded := matching()
				recorded.SecretState = state.SecretState{Hash: "sha256:oldsecrethash", Names: []string{"DB_PASS"}}
				return recorded
			},
			desired:      desired,
			wantRecreate: true,
		},
		{
			name: "network policy differs",
			recorded: func() state.HomeState {
				recorded := matching()
				recorded.NetworkState = state.BuildNetworkState(
					network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
				)
				return recorded
			},
			desired:      desired,
			wantRecreate: true,
		},
		{
			name: "no network policy recorded yet",
			recorded: func() state.HomeState {
				recorded := matching()
				recorded.NetworkState = state.NetworkState{}
				return recorded
			},
			desired:      desired,
			wantRecreate: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			mock := reconfigMockClient()
			msb.WithMsbMock(t, mock)
			ui := termio.NewTestMock(t)
			configFiles := loadOpencodeConfigFiles(t, &ui)
			k := state.Key{Slug: "testproj", Agent: "opencode"}
			live, err := readLiveProjectVM(context.Background(), mock, k, configFiles.Keys, &ui)
			if err != nil {
				t.Fatalf("readLiveProjectVM: %v", err)
			}

			decision, err := resolveReconfigDecision(
				context.Background(),
				volume.NewManager(docker.Get()),
				k,
				options.RunOptions{Agent: "opencode", Network: policy},
				image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag", Digest: "sha256:samedigest"},
				"vol",
				tc.recorded(),
				configFiles,
				tc.desired,
				live,
				&ui,
			)
			if err != nil {
				t.Fatalf("resolveReconfigDecision: %v", err)
			}
			if decision.Recreate != tc.wantRecreate {
				t.Errorf(
					"Recreate = %v, want %v (rebuild reasons: %v)",
					decision.Recreate,
					tc.wantRecreate,
					ui.InfoCalls,
				)
			}
			if decision.Restart {
				t.Error("Restart = true, want false: a fingerprint change is folded into the recreate")
			}
		})
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

func TestResolveReconfigDecisionAgentConfigChangeOnStoppedVM(t *testing.T) {
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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

func TestResolveReconfigDecisionHomePromptDeferredWhenRebuildDeferred(t *testing.T) {
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

func TestResolveReconfigDecisionHomePromptAskedWhenRebuildConfirmed(t *testing.T) {
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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

// loadOpencodeConfigFiles loads opencode's config files from an empty host
// config directory.
func loadOpencodeConfigFiles(t *testing.T, ui termio.UI) *reprovision.ConfigFiles {
	t.Helper()
	configFiles, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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
	cfs, err := reprovision.LoadConfigFilesForHost(
		opencodeResolved(t),
		"",
		t.TempDir(),
		reprovision.VMHomeDir,
		&ui,
		true,
	)
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

// TestApplyLivePortsReadFailureDegrades covers the read-back failure branch:
// when the live VM's published ports cannot be read, the bindings are left
// untouched and the failure is reported at verbose level instead of aborting.
func TestApplyLivePortsReadFailureDegrades(t *testing.T) {
	cfg := &msbSdk.SandboxConfig{}
	failing := &msb.MockSandboxHandle{PortsErr: errors.New("ports boom")}
	ui := termio.NewTestMock(t)

	applyLivePorts(cfg, failing, &ui)

	if cfg.PortBindings != nil {
		t.Errorf("PortBindings = %+v, want untouched (nil) after a read failure", cfg.PortBindings)
	}
	assertVerbose(t, &ui, "reading VM ports failed")
}

func TestExistingServeHostPort(t *testing.T) {
	if got := existingServeHostPort(nil, 4100); got != 4100 {
		t.Errorf("existingServeHostPort(nil, 4100) = %d, want the fallback", got)
	}
	cfg := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP},
		{Bind: "127.0.0.1", HostPort: 4096, GuestPort: ports.ServeOnlyBasePort, Protocol: msbSdk.PortProtocolTCP},
	}}
	if got := existingServeHostPort(cfg, 4100); got != 4096 {
		t.Errorf("existingServeHostPort = %d, want 4096 (the agent guest port binding)", got)
	}
	userOnly := &msbSdk.SandboxConfig{PortBindings: []msbSdk.PortBinding{
		{Bind: "127.0.0.1", HostPort: 3000, GuestPort: 3000, Protocol: msbSdk.PortProtocolTCP},
	}}
	if got := existingServeHostPort(userOnly, 4100); got != 4100 {
		t.Errorf("existingServeHostPort(no serve binding) = %d, want the fallback", got)
	}
}

// TestBootMaxMemoryGrew pins the flag that lets the plan recreate for memory
// above the live boot maximum: only a live VM booted with a lower maximum than
// a fresh VM would get counts.
func TestBootMaxMemoryGrew(t *testing.T) {
	freshMaxMiB := vm.BootMaxMemoryMiB() //testenv:allow the recreate flag compares against the host's current boot maximum
	tests := []struct {
		name string
		live liveProjectVM
		want bool
	}{
		{name: "no live config", live: liveProjectVM{}, want: false},
		{
			name: "same maximum",
			live: liveProjectVM{config: &msbSdk.SandboxConfig{MaxMemoryMiB: freshMaxMiB}},
			want: false,
		},
		{
			name: "lower live maximum",
			live: liveProjectVM{config: &msbSdk.SandboxConfig{MaxMemoryMiB: freshMaxMiB - 1024}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bootMaxMemoryGrew(tc.live); got != tc.want {
				t.Errorf("bootMaxMemoryGrew = %v, want %v", got, tc.want)
			}
		})
	}
}

// memoryRaiseLogEntry builds a system-log entry carrying a parseable OOM kill
// naming the harness.
func memoryRaiseLogEntry(at time.Time) msbSdk.LogEntry {
	return msbSdk.LogEntry{
		Source:    msbSdk.LogSourceSystem,
		Timestamp: at,
		Data:      []byte("Out of memory: Killed process 42 (opencode) total-vm:1kB"),
	}
}

// trippedMetrics is a snapshot at ~95% memory usage, above the 90% trip level.
func trippedMetrics() *msbSdk.Metrics {
	return &msbSdk.Metrics{MemoryBytes: 7800 << 20, MemoryLimitBytes: 8192 << 20}
}

// floorTrippedMetrics is a snapshot at ~79% memory usage: below the 80% re-arm
// level but with only ~861 MiB free, so the absolute floor (1024 MiB) still
// trips. It is the overlap that must not count as recovery.
func floorTrippedMetrics() *msbSdk.Metrics {
	return &msbSdk.Metrics{MemoryBytes: 3235 << 20, MemoryLimitBytes: 4096 << 20}
}

// seedEmptyHomeState creates the key's state file with no recorded pressure, so
// the writers (which no-op without a state file) take effect.
func seedEmptyHomeState(t *testing.T, k state.Key) {
	t.Helper()
	if err := seedHomeState(k, state.HomeState{}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
}

// readPressureMarker reads the key's current pressure marker.
func readPressureMarker(t *testing.T, k state.Key) *state.ResourcePressureState {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return hs.ResourcePressure
}

// pressureHomeState re-reads the key's HomeState, as a fresh launch's
// ensure-home would.
func pressureHomeState(t *testing.T, k state.Key) state.HomeState {
	t.Helper()
	hs, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return *hs
}

// resolvePreparedMemory reads back the resolved memory value for slug.
func resolvePreparedMemory(t *testing.T, slug string) string {
	t.Helper()
	r, err := viperconfig.NewResolver(nil, configpaths.Get(), slug)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r.Config().Memory
}

// withoutMemoryEnv clears the memory env vars so PersistMemory's shadow guard
// does not refuse the write.
func withoutMemoryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTS_SANDBOX_MEMORY", "")
	t.Setenv("OPENCODE_SANDBOX_MEMORY", "")
}

// TestDetectAndOfferMemoryRaiseNewerOOMOffersAndPersists pins the primary path:
// an OOM kill newer than the marker's recorded, answered kill is offered once
// and an accepted raise is persisted.
func TestDetectAndOfferMemoryRaiseNewerOOMOffersAndPersists(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withoutMemoryEnv(t)
	k := testVMKey()
	oldKill := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := seedHomeState(k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceOOMLog), Victim: "old",
		OOMSeenAt: oldKill, OfferedAt: oldKill.Add(time.Minute),
	}}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	newKill := oldKill.Add(time.Hour)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192}
	handle := &msb.MockSandboxHandle{
		Cfg:        cfg,
		LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(newKill)},
	}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "raise", nil
		},
		InputFn: func(string, string) (string, error) { return "8G", nil },
	}

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1", selects)
	}
	marker := readPressureMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(newKill) || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the new kill recorded and answered", marker)
	}
	if got := resolvePreparedMemory(t, k.Slug); got != "8192M" {
		t.Errorf("persisted memory = %q, want 8192M", got)
	}
}

// TestDetectAndOfferMemoryRaiseDeclineDoesNotReOfferSameOOM pins the decline
// answer: it advances the marker, so the same log entry is not offered again.
func TestDetectAndOfferMemoryRaiseDeclineDoesNotReOfferSameOOM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testVMKey()
	seedEmptyHomeState(t, k)
	kill := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096}
	handle := &msb.MockSandboxHandle{Cfg: cfg, LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(kill)}}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "keep", nil
		},
	}
	live := liveProjectVM{handle: handle, config: cfg}

	detectAndOfferMemoryRaise(
		context.Background(),
		live,
		k,
		options.RunOptions{},
		pressureHomeState(t, k),
		configpaths.Get(),
		ui,
	)
	detectAndOfferMemoryRaise(
		context.Background(),
		live,
		k,
		options.RunOptions{},
		pressureHomeState(t, k),
		configpaths.Get(),
		ui,
	)

	if selects != 1 {
		t.Fatalf("Select called %d times, want 1 (a declined kill is not re-offered)", selects)
	}
	if marker := readPressureMarker(t, k); marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the decline recorded", marker)
	}
}

// TestDetectAndOfferMemoryRaiseLogOrMetricsErrorIsBestEffort pins that a failed
// evidence read never prompts and never fails.
func TestDetectAndOfferMemoryRaiseLogOrMetricsErrorIsBestEffort(t *testing.T) {
	cases := []struct {
		name   string
		handle *msb.MockSandboxHandle
	}{
		{"system log read fails", &msb.MockSandboxHandle{LogsErr: errors.New("logs boom")}},
		{"metrics read fails", &msb.MockSandboxHandle{MetricsErr: errors.New("metrics boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configpaths.WithMockConfigPaths(t)
			k := testVMKey()
			seedEmptyHomeState(t, k)
			var selects int
			ui := &termio.Mock{
				IsInteractiveResult: true,
				SelectFn: func(string, []termio.Choice, string) (string, error) {
					selects++
					return "raise", nil
				},
			}

			detectAndOfferMemoryRaise(
				context.Background(), liveProjectVM{handle: tc.handle}, k,
				options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
			)

			if selects != 0 {
				t.Errorf("Select called %d times, want 0 after an evidence read error", selects)
			}
			if marker := readPressureMarker(t, k); marker != nil {
				t.Errorf("marker = %+v, want none recorded after an evidence read error", marker)
			}
		})
	}
}

// TestDetectAndOfferMemoryRaiseNonInteractiveRecordsAndLaterOffers pins the §7
// deferral: a non-interactive launch records the hit but does not answer it, so
// the next interactive launch offers it.
func TestDetectAndOfferMemoryRaiseNonInteractiveRecordsAndLaterOffers(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testVMKey()
	seedEmptyHomeState(t, k)
	kill := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096}
	handle := &msb.MockSandboxHandle{Cfg: cfg, LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(kill)}}
	live := liveProjectVM{handle: handle, config: cfg}

	detectAndOfferMemoryRaise(
		context.Background(), live, k, options.RunOptions{},
		pressureHomeState(t, k), configpaths.Get(), &termio.Mock{},
	)

	marker := readPressureMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(kill) || !marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the kill recorded but unanswered", marker)
	}

	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "keep", nil
		},
	}
	detectAndOfferMemoryRaise(
		context.Background(),
		live,
		k,
		options.RunOptions{},
		pressureHomeState(t, k),
		configpaths.Get(),
		ui,
	)

	if selects != 1 {
		t.Fatalf("Select called %d times on the interactive launch, want 1", selects)
	}
	if marker := readPressureMarker(t, k); marker.OfferedAt.IsZero() {
		t.Error("OfferedAt is zero after the interactive launch answered")
	}
}

// TestDetectAndOfferMemoryRaiseFreshHeadroomAtLaunchDoesNotOffer pins that the
// next-launch path offers no raise for a busy VM: a tripped metrics read with no
// OOM line and no recorded hit is not a limit hit (another client may simply be
// using the memory).
func TestDetectAndOfferMemoryRaiseFreshHeadroomAtLaunchDoesNotOffer(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	withoutMemoryEnv(t)
	k := testVMKey()
	seedEmptyHomeState(t, k)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192}
	handle := &msb.MockSandboxHandle{Cfg: cfg, MetricsResult: floorTrippedMetrics()}
	var selects int
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(string, []termio.Choice, string) (string, error) {
			selects++
			return "raise", nil
		},
	}

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	if selects != 0 {
		t.Fatalf("selects=%d, want no offer for a busy VM at launch", selects)
	}
}

// TestDetectAndOfferMemoryRaisePreservesHeadroomSeenTime pins the fingerprint
// of a recorded-but-unanswered headroom trip: offering it again keeps the
// first-seen time rather than starting a new one.
func TestDetectAndOfferMemoryRaisePreservesHeadroomSeenTime(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testVMKey()
	seen := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := seedHomeState(k, state.HomeState{ResourcePressure: &state.ResourcePressureState{
		Kind: "memory", Evidence: string(pressure.EvidenceHeadroom), OOMSeenAt: seen,
	}}); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096}
	handle := &msb.MockSandboxHandle{Cfg: cfg, MetricsResult: trippedMetrics()}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "keep", nil },
	}

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	marker := readPressureMarker(t, k)
	if marker == nil || !marker.OOMSeenAt.Equal(seen) {
		t.Fatalf("marker = %+v, want the first-seen headroom time %v preserved", marker, seen)
	}
}

// TestDetectAndOfferMemoryRaiseCurrentMemoryFallbacks pins the suggestion
// basis: the metrics limit when the live config is unreadable, and no offer
// when neither is known (a zero basis would pre-fill a decrease).
func TestDetectAndOfferMemoryRaiseCurrentMemoryFallbacks(t *testing.T) {
	t.Run("metrics limit when the config is unreadable", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		withoutMemoryEnv(t)
		k := testVMKey()
		seedEmptyHomeState(t, k)
		handle := &msb.MockSandboxHandle{
			LogsResult:    []msbSdk.LogEntry{memoryRaiseLogEntry(time.Now())},
			MetricsResult: trippedMetrics(),
		}
		ui, inputDefault := raiseUI(t)
		detectAndOfferMemoryRaise(
			context.Background(), liveProjectVM{handle: handle}, k,
			options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
		)
		if *inputDefault != "10G" {
			t.Errorf("Input default = %q, want 10G from the metrics limit", *inputDefault)
		}
	})

	t.Run("no offer when neither is known", func(t *testing.T) {
		configpaths.WithMockConfigPaths(t)
		withoutMemoryEnv(t)
		k := testVMKey()
		seedEmptyHomeState(t, k)
		kill := time.Now()
		handle := &msb.MockSandboxHandle{
			LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(kill)},
		}
		var selects int
		ui := &termio.Mock{
			IsInteractiveResult: true,
			SelectFn: func(string, []termio.Choice, string) (string, error) {
				selects++
				return "raise", nil
			},
		}
		detectAndOfferMemoryRaise(
			context.Background(), liveProjectVM{handle: handle}, k,
			options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
		)
		if selects != 0 {
			t.Fatalf("Select called %d times, want 0 without a known limit", selects)
		}
		marker := readPressureMarker(t, k)
		if marker == nil || !marker.OOMSeenAt.Equal(kill) || !marker.OfferedAt.IsZero() {
			t.Fatalf("marker = %+v, want the kill recorded but unanswered", marker)
		}
	})
}

// raiseUI returns an interactive UI that accepts the raise, plus a pointer to
// the value prompt's default.
func raiseUI(t *testing.T) (*termio.Mock, *string) {
	t.Helper()
	inputDefault := ""
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn: func(_ string, defaultValue string) (string, error) {
			inputDefault = defaultValue
			return defaultValue, nil
		},
	}
	return ui, &inputDefault
}

// TestDetectAndOfferMemoryRaisePromptErrorIsBestEffort pins that a prompt
// failure records the hit unanswered and does not fail the run.
func TestDetectAndOfferMemoryRaisePromptErrorIsBestEffort(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testVMKey()
	seedEmptyHomeState(t, k)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096}
	handle := &msb.MockSandboxHandle{
		Cfg:        cfg,
		LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(time.Now())},
	}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "", errors.New("prompt boom") },
	}

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	if marker := readPressureMarker(t, k); marker == nil || !marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the hit recorded but unanswered after a prompt error", marker)
	}
	assertVerbose(t, ui, "memory raise prompt failed")
}

// TestDetectAndOfferMemoryRaisePersistErrorWarns pins that a persist refusal
// (here a JSON config the launcher does not edit) warns rather than failing, and
// still advances the marker.
func TestDetectAndOfferMemoryRaisePersistErrorWarns(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := testVMKey()
	seedEmptyHomeState(t, k)
	cfg := &msbSdk.SandboxConfig{MemoryMiB: 4096}
	handle := &msb.MockSandboxHandle{
		Cfg:        cfg,
		LogsResult: []msbSdk.LogEntry{memoryRaiseLogEntry(time.Now())},
	}
	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn:            func(string, []termio.Choice, string) (string, error) { return "raise", nil },
		InputFn:             func(string, string) (string, error) { return "8G", nil },
	}
	withoutMemoryEnv(t)
	testutil.WriteFile(t, configpaths.Get().UserSlugConfigDir(k.Slug), "config.json", "{}")

	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{handle: handle, config: cfg}, k,
		options.RunOptions{}, pressureHomeState(t, k), configpaths.Get(), ui,
	)

	if marker := readPressureMarker(t, k); marker == nil || marker.OfferedAt.IsZero() {
		t.Fatalf("marker = %+v, want the answer recorded despite the persist refusal", marker)
	}
	warned := false
	for _, warning := range ui.WarnCalls {
		if strings.Contains(warning, "could not persist") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("warnings %v do not report the persist failure", ui.WarnCalls)
	}
}

// TestDetectAndOfferMemoryRaiseSkipsWithoutVM pins the no-VM guard.
func TestDetectAndOfferMemoryRaiseSkipsWithoutVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	detectAndOfferMemoryRaise(
		context.Background(), liveProjectVM{}, testVMKey(), options.RunOptions{},
		state.HomeState{}, configpaths.Get(), &ui,
	)
}

// TestBootMaxMemoryMiB pins the live-VM boot-maximum read: the configured value
// is returned, and an unread config reports 0 (no clamp).
func TestBootMaxMemoryMiB(t *testing.T) {
	live := liveProjectVM{config: &msbSdk.SandboxConfig{MemoryMiB: 4096, MaxMemoryMiB: 8192}}
	if got := bootMaxMemoryMiB(live); got != 8192 {
		t.Errorf("bootMaxMemoryMiB = %d, want 8192", got)
	}
	if got := bootMaxMemoryMiB(liveProjectVM{}); got != 0 {
		t.Errorf("bootMaxMemoryMiB(nil config) = %d, want 0", got)
	}
}
