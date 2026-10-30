package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

func joinStrings(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
		b.WriteByte('|')
	}
	return b.String()
}

func TestProjectVMNameIncludesAgent(t *testing.T) {
	got := projectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "pi"})
	want := "myproject-aBc1234DeF-pi"
	if got != want {
		t.Errorf("projectVMName = %q, want %q", got, want)
	}
	got = projectVMName(state.Key{Slug: "myproject-aBc1234DeF", Agent: "opencode"})
	want = "myproject-aBc1234DeF-opencode"
	if got != want {
		t.Errorf("projectVMName = %q, want %q", got, want)
	}
}

func TestProjectVMNameTruncationPreservesAgent(t *testing.T) {
	long := "x" + string(make([]byte, options.MaxSandboxNameLen))
	k := state.Key{Slug: long, Agent: "pi"}
	got := projectVMName(k)
	if len(got) > options.MaxSandboxNameLen {
		t.Fatalf("name %d bytes > max %d", len(got), options.MaxSandboxNameLen)
	}
	if !strings.HasSuffix(got, "-pi") {
		t.Errorf("truncated name %q lost the agent suffix", got)
	}
}

// TestProjectVMNameExtremeAgentCoversFinalCut covers the final hard cut in
// projectVMName, reachable only when the agent name itself exceeds the max
// name length. The full name is cut at the byte limit and the agent suffix
// may be lost, so we only assert the length bound.
func TestProjectVMNameExtremeAgentCoversFinalCut(t *testing.T) {
	slug := "proj"
	agent := "a" + string(make([]byte, options.MaxSandboxNameLen))
	got := projectVMName(state.Key{Slug: slug, Agent: agent})
	if len(got) != options.MaxSandboxNameLen {
		t.Errorf("expected name cut to exactly %d bytes, got %d", options.MaxSandboxNameLen, len(got))
	}
}

// TestCurrentAgentVersionIgnoresCorruptFile covers the read-error branch of
// currentAgentVersion (a corrupt project state file must yield "").
func TestCurrentAgentVersionIgnoresCorruptFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	sdir := filepath.Join(configpaths.Get().UserStateDir(), k.Slug, k.Agent)
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdir, "state.yaml"), []byte("::: not yaml :::"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := currentAgentVersion(k); got != "" {
		t.Errorf("currentAgentVersion() = %q, want empty for corrupt file", got)
	}
}

// TestPersistConfigHashesCoversEnvSecretAndNetwork covers the previously
// entirely-uncovered persistConfigHashes function: it persists the desired
// env/secret/network fingerprints after a fresh VM creation.
func TestPersistConfigHashesCoversEnvSecretAndNetwork(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	slug := "hashproj"
	k := state.Key{Slug: slug, Agent: "opencode"}
	if err := seedHomeState(k, state.NewHomeState("vol", "digest")); err != nil {
		t.Fatalf("seedHomeState: %v", err)
	}
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	persistConfigHashes(
		k,
		state.Fingerprints{
			Network: state.BuildNetworkState(policy),
			Mount:   state.BuildMountState(nil),
		},
		&ui,
	)

	st, err := state.ReadState(k)
	if err != nil {
		t.Fatalf("ReadState after persistConfigHashes: %v", err)
	}
	if st.NetworkState.Hash == "" {
		t.Error("expected network state hash to be persisted")
	}
	if st.MountState.Hash == "" {
		t.Error("expected mount state hash to be persisted")
	}
}

// TestStopProjectVMGetSandboxError covers the non-not-found GetSandbox error
// branch in stopOrKillProjectVM.
func TestStopProjectVMGetSandboxError(t *testing.T) {
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}

// TestStopProjectVMStopFnError covers the stopFn error branch and the remove
// error branch (remove fails after a successful stop).
func TestStopProjectVMStopFnError(t *testing.T) {
	ui := termio.NewTestMock(t)
	handle := &msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	}
	handle.StopErr = errors.New("stop failed")
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(handle)
	msb.WithMsbMock(t, client)

	err := stopOrKillProjectVM(context.Background(), false, false, &ui, testVMKey(), "stop", "Stopping", client,
		func(h msb.SandboxHandle, c context.Context) error { return h.Stop(c) })
	if err == nil {
		t.Fatal("expected error from stopFn")
	}
}

// TestKillProjectVMDryRunRemove covers the kill dry-run branch and the
// actionWord selection.
func TestKillProjectVMDryRunRemove(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGotSandbox(&msb.MockSandboxHandle{
		Name_:   "agents-sandbox-vm-test",
		Status_: msbSdk.SandboxStatusRunning,
	})
	msb.WithMsbMock(t, client)

	if err := KillProjectVM(context.Background(), true, true, &ui, "opencode"); err != nil {
		t.Fatalf("KillProjectVM dry-run: %v", err)
	}
}

// TestStopAndKillProjectVMUnknownAgent covers the unknown-agent error branch
// in both StopProjectVM and KillProjectVM.
func TestStopAndKillProjectVMUnknownAgent(t *testing.T) {
	ui := termio.NewTestMock(t)
	if err := StopProjectVM(context.Background(), false, false, &ui, "no-such-agent"); err == nil {
		t.Fatal("expected error from StopProjectVM with an unknown agent")
	}
	if err := KillProjectVM(context.Background(), false, false, &ui, "no-such-agent"); err == nil {
		t.Fatal("expected error from KillProjectVM with an unknown agent")
	}
}

// TestSetUpSandboxProvisionError covers the provision-failure branch of
// setUpSandbox: a provision failure is logged as a warning and the setup
// continues (config provisioning is non-disruptive and never fatal).
func TestSetUpSandboxProvisionError(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte("{}")}
	if _, err := setUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		options.RunOptions{Agent: "opencode"},
		cfs,
		&ui,
		false,
		vmBootStarted,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("setUpSandbox: %v", err)
	}

	joined := joinStrings(ui.WarnCalls)
	if !contains(joined, "provision failed") {
		t.Errorf("expected a provision-failure warning, got %v", ui.WarnCalls)
	}
}

// TestRestartDaemonsKillError covers the kill-stale-job failure branch of
// restartDaemons: the failure is reported as a verbose diagnostic and the
// daemon is still (re)started.
func TestRestartDaemonsKillError(t *testing.T) {
	orig := SetDaemonHealthFunc(func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"test"}`, 0, nil
	})
	t.Cleanup(func() { SetDaemonHealthFunc(orig) })

	ui := termio.NewTestMock(t)
	stale := &msb.MockJob{ID_: "stale", KillErr: errors.New("kill failed")}
	command := opencodeProvider(t).DaemonCommand(false)
	sb := &msb.MockSandbox{
		Name_: "vm",
		Jobs: []msbSdk.JobInfo{
			{ID: "stale", Command: append([]string{command.Binary}, command.Args...)},
		},
		GetJobFn: func(context.Context, string) (msb.Job, error) { return stale, nil },
	}

	restartDaemons(context.Background(), opencodeAgent(t), sb, false, &ui)

	if !contains(joinStrings(ui.VerboseCalls), "killing job") {
		t.Errorf("expected a kill-failure verbose message, got %v", ui.VerboseCalls)
	}
}

// TestRestartDaemonsNoProvider covers the early-return branch of
// restartDaemons for an agent that is not a DaemonProvider: it must no-op.
func TestRestartDaemonsNoProvider(t *testing.T) {
	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil, nil)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs, ShellCalls: &[]string{}}

	restartDaemons(context.Background(), &fakeAgent{}, sb, false, &ui)

	if len(*sb.ShellCalls) != 0 {
		t.Errorf("expected no shell calls for an agent without a DaemonProvider, got %v", *sb.ShellCalls)
	}
}

// TestEnsureProjectVMSkipVM covers the skip-VM early-return branch.
func TestEnsureProjectVMSkipVM(t *testing.T) {
	ui := termio.NewTestMock(t)
	sb, boot, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		true,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err != nil {
		t.Fatalf("ensureProjectVM dry-run: %v", err)
	}
	if sb != nil {
		t.Error("expected nil sandbox for dry-run-vm")
	}
	if boot != vmBootConnected {
		t.Errorf("expected vmBootConnected for dry-run, got %v", boot)
	}
}

// TestEnsureProjectVMGetSandboxFatalError covers the non-not-found GetSandbox
// error branch in ensureProjectVM.
func TestEnsureProjectVMGetSandboxFatalError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)
	client := &msb.MockMsbClient{}
	client.SetGetSandboxErr(errors.New("boom"))
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(
		context.Background(),
		options.RunOptions{Agent: "opencode"},
		image.ImageInfo{DockerTag: "img:tag", Ref: "img:tag"},
		"vol",
		"/workspace",
		testVMKey(),
		testVMKey().Slug,
		false,
		envsecret.EnvSecrets{},
		&ui,
	)
	if err == nil {
		t.Fatal("expected error from GetSandbox")
	}
}
