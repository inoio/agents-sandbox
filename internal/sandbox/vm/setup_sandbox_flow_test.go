package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// SetUpSandbox consumes the run's already-resolved agent capabilities (the
// caller holds them) instead of re-resolving opts.Agent. The mismatched
// opts.Agent ("does-not-exist") is deliberate: production always passes the pair
// produced together at the run boundary, and this contradictory pair is the only
// way to prove SetUpSandbox reads the threaded value rather than opts.Agent —
// were it still resolving opts.Agent, an unknown name would fail here.
func TestSetUpSandboxUsesTheThreadedResolvedAgent(t *testing.T) {
	seams := healthyOpencodeDaemon(t)
	configpaths.WithMockConfigPaths(t)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil)}
	ui := termio.NewTestMock(t)

	if _, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "does-not-exist"},
		seams,
		&reprovision.ConfigFiles{},
		&ui,
		false,
		VMBootConnected,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("SetUpSandbox with a threaded resolved agent error = %v", err)
	}
}

// TestSetUpSandboxContinuesWhenProvisioningFails pins that a failed config
// write is a warning, not a setup failure: provisioning is non-disruptive.
func TestSetUpSandboxContinuesWhenProvisioningFails(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	}}

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: fs,
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
	}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte("{}")}
	if _, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		&ui,
		false,
		VMBootStarted,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}

	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "provision failed") {
		t.Errorf("expected a provision-failure warning, got %v", ui.WarnCalls)
	}
}

// TestSetUpSandboxReportsADaemonThatCannotStart pins that an unhealthy daemon
// that cannot be started fails the setup with the start's cause.
func TestSetUpSandboxReportsADaemonThatCannotStart(t *testing.T) {
	startFailed := errors.New("daemon start failed")
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return "", 0, errors.New("daemon shell failed")
	}}

	ui := termio.NewTestMock(t)
	sb := &msb.MockSandbox{
		Name_:    "vm",
		FSValue_: msb.NewTestFS(nil),
		ShellOut: map[string]msb.ShellResult{
			dockerdBinaryCheckCmd: msb.NewTestResult(false, 1, "", "", nil),
		},
		ExecDetachedFn: func(context.Context, string, []string, msb.JobSpec) (msb.Job, error) {
			return nil, startFailed
		},
	}

	configpaths.WithMockConfigPaths(t)
	cfs := &reprovision.ConfigFiles{}
	_, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		&ui,
		false,
		VMBootConnected,
		mounts.WorkspaceMountPath,
	)
	if !errors.Is(err, startFailed) {
		t.Fatalf("SetUpSandbox error = %v, want it to wrap %v", err, startFailed)
	}
}

// TestSetUpSandboxProvisionsEachConfigKind pins that each kind of config file
// alone is enough to trigger provisioning.
func TestSetUpSandboxProvisionsEachConfigKind(t *testing.T) {
	const path = "/home/dev/.config/example"
	for _, tc := range []struct {
		name        string
		cfs         *reprovision.ConfigFiles
		provisioned func(fs *msb.TestFS) bool
	}{
		{
			name:        "home files",
			cfs:         &reprovision.ConfigFiles{HomeFiles: map[string][]byte{path: []byte("x")}},
			provisioned: func(fs *msb.TestFS) bool { return fs.Writes[path] != nil },
		},
		{
			name:        "drop-in files",
			cfs:         &reprovision.ConfigFiles{Provisioned: map[string][]byte{path: []byte("x")}},
			provisioned: func(fs *msb.TestFS) bool { return fs.Writes[path] != nil },
		},
		{
			name:        "mirror files",
			cfs:         &reprovision.ConfigFiles{Mirror: map[string][]byte{path: []byte("x")}},
			provisioned: func(fs *msb.TestFS) bool { return fs.Writes[path] != nil },
		},
		{
			name:        "stale files to remove",
			cfs:         &reprovision.ConfigFiles{Remove: []string{path}},
			provisioned: func(fs *msb.TestFS) bool { return slices.Contains(fs.Removed, path) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := healthyOpencodeDaemon(t)
			configpaths.WithMockConfigPaths(t)
			fs := msb.NewTestFS(map[string][]byte{path: []byte("old")})
			sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}
			ui := termio.NewTestMock(t)

			if err := setUpOpencodeSandbox(t, sb, tc.cfs, &ui, VMBootConnected, seams); err != nil {
				t.Fatalf("SetUpSandbox: %v", err)
			}
			if !tc.provisioned(fs) {
				t.Errorf("config was not provisioned; writes %v, removed %v", fs.Writes, fs.Removed)
			}
		})
	}
}

// TestSetUpSandboxRecordsProvenanceOnlyOnBoot pins that the image's install
// provenance is recorded when this run booted the VM, and left alone when it
// merely connected to a running one.
func TestSetUpSandboxRecordsProvenanceOnlyOnBoot(t *testing.T) {
	for _, tc := range []struct {
		name       string
		boot       VMBoot
		wantSource string
	}{
		{name: "connected", boot: VMBootConnected, wantSource: state.AgentSourceTool},
		{name: "started", boot: VMBootStarted, wantSource: state.AgentSourceUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := healthyOpencodeDaemon(t)
			configpaths.WithMockConfigPaths(t)
			if err := seedHomeState(testVMKey(), state.HomeState{AgentSource: state.AgentSourceTool}); err != nil {
				t.Fatalf("seed home state: %v", err)
			}
			fs := msb.NewTestFS(map[string][]byte{agentSourcePath: []byte(state.AgentSourceUser + "\n")})
			sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}
			ui := termio.NewTestMock(t)

			if err := setUpOpencodeSandbox(t, sb, &reprovision.ConfigFiles{}, &ui, tc.boot, seams); err != nil {
				t.Fatalf("SetUpSandbox: %v", err)
			}
			hs, err := state.ReadState(testVMKey())
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if hs.AgentSource != tc.wantSource {
				t.Errorf("agent source = %q, want %q", hs.AgentSource, tc.wantSource)
			}
		})
	}
}

// TestSetUpSandboxStopsWhenDockerdCannotStart pins that a dockerd startup
// failure ends the setup with its cause, before the agent daemon is started.
func TestSetUpSandboxStopsWhenDockerdCannotStart(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	shellErr := errors.New("dockerd binary check failed")
	calls := &[]msb.MockDetachedCall{}
	sb := &msb.MockSandbox{
		Name_:             "vm",
		FSValue_:          msb.NewTestFS(nil),
		ShellErr:          shellErr,
		ExecDetachedCalls: calls,
	}
	ui := termio.NewTestMock(t)
	// An unhealthy daemon would be probed and launched if the setup went on.
	probes := 0
	seams := options.Seams{DaemonHealth: func(context.Context, msb.Sandbox, string) (string, int, error) {
		probes++
		return "", 1, nil
	}}

	err := setUpOpencodeSandbox(t, sb, &reprovision.ConfigFiles{}, &ui, VMBootConnected, seams)
	if !errors.Is(err, shellErr) || !strings.Contains(err.Error(), "docker startup") {
		t.Fatalf("SetUpSandbox error = %v, want the docker startup failure wrapping %v", err, shellErr)
	}
	if probes != 0 || len(*calls) != 0 {
		t.Errorf("daemon probed %d times and launched %+v, want neither after dockerd failed", probes, *calls)
	}
}

// TestSetUpSandboxReportsWhatItDoes pins the messages a user sees while the
// sandbox is set up: the expected config files, each startup hook, and a
// daemon restart.
func TestSetUpSandboxReportsWhatItDoes(t *testing.T) {
	seams := healthyOpencodeDaemon(t)
	configpaths.WithMockConfigPaths(t)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil)}
	ui := termio.NewTestMock(t)
	cfs := &reprovision.ConfigFiles{
		Keys:  []string{"/home/dev/.config/example"},
		Hooks: []homeconfig.HookSpec{{Target: "/home/dev/.hello.sh", Source: "x", HasShebang: true}},
	}

	if err := setUpOpencodeSandbox(t, sb, cfs, &ui, VMBootStarted, seams); err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if _, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		&ui,
		true,
		VMBootConnected,
		mounts.WorkspaceMountPath,
	); err != nil {
		t.Fatalf("SetUpSandbox (restart): %v", err)
	}

	for _, want := range []struct {
		calls   []string
		message string
	}{
		{ui.VerboseCalls, "expected config files: [/home/dev/.config/example]"},
		{ui.InfoCalls, "running startup hook /home/dev/.hello.sh (as dev, via /home/dev/.hello.sh)"},
		{ui.InfoCalls, "opencode serve restarting…"},
	} {
		if !slices.ContainsFunc(want.calls, func(call string) bool { return strings.Contains(call, want.message) }) {
			t.Errorf("missing message %q in %v", want.message, want.calls)
		}
	}
}

// TestSetUpSandboxProvisionsTheMergedConfig pins that the merged agent config
// from the host snippets is written to a VM whose config dir is empty, and that
// the agent starts in the workspace.
func TestSetUpSandboxProvisionsTheMergedConfig(t *testing.T) {
	setUpSandboxProvisionsConfig(t)
}

func TestSetUpSandboxRestartsDaemonsOnReuseDecision(t *testing.T) {
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, _ string) (string, int, error) {
		return `{"healthy":true,"version":"x"}`, 0, nil
	}}

	calls := &[]msb.MockDetachedCall{}
	fs := msb.NewTestFS(nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs, ExecDetachedCalls: calls}
	ui := termio.NewTestMock(t)

	configpaths.WithMockConfigPaths(t)

	// SetUpSandbox's signature is
	//   (ctx, k, sb, opts, seams, cfs, ui, restart, boot, guestCWD).
	// Pass restart=true to force the daemon-restart path on a reused VM.
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
	target, err := SetUpSandbox(
		context.Background(), testVMKey(), sb, opencodeResolved(t), options.RunOptions{Agent: "opencode"},
		seams, cfs, &ui, true, VMBootStarted, mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if target == "" {
		t.Error("expected a resolved target")
	}
	if len(*calls) != 1 {
		t.Errorf("expected opencode serve restart on restartDaemons=true, got %+v", *calls)
	}
	assertDetachedHelperLaunch(t, *calls, "agent-daemon", "opencode")
}

// TestSetUpSandboxSkipsRestartOnProvisionError verifies that when a daemon
// restart is warranted (restart=true) but provisioning the updated config
// fails, the running daemon is preserved: restartDaemons is not called and no
// daemon commands are issued, but a target is still returned.
func TestSetUpSandboxSkipsRestartOnProvisionError(t *testing.T) {
	var commands []string
	seams := options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		commands = append(commands, command)
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"x"}`, 0, nil
		}
		return "", 0, nil
	}}

	configpaths.WithMockConfigPaths(t)

	ui := termio.NewTestMock(t)
	fs := msb.NewTestFS(nil)
	fs.WriteErr = errors.New("write denied")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: fs}

	cfs := &reprovision.ConfigFiles{HasSnippets: true, Merged: []byte("{}")}
	target, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		&ui,
		true,
		VMBootStarted,
		mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if target == "" {
		t.Error("expected a resolved target even when provisioning failed")
	}

	joined := strings.Join(commands, "\n")
	if len(commands) > 0 {
		t.Errorf("expected no daemon commands on provision failure (daemon preserved), got %q", joined)
	}
	if !strings.Contains(strings.Join(ui.WarnCalls, "\n"), "provision failed") {
		t.Errorf("expected a provision-failure warning, got %v", ui.WarnCalls)
	}
}

func TestSetUpSandboxProvisionsUpdatedConfigOnKeep(t *testing.T) {
	seams := healthyOpencodeDaemon(t)

	// The VM already contains an OLD opencode.json (content differs from desired).
	// This simulates attaching to a running VM and choosing "keep": no daemon
	// restart, but the updated config must still be provisioned so the next
	// daemon start picks it up.
	ocPath := opencodeMergedConfigPath
	fs := msb.NewTestFS(map[string][]byte{
		ocPath: []byte(`{"model":"old"}`),
	})
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}

	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), opencodeAgent(t).ConfigDirName())),
		"opencode-x.json5",
	)
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"new"}`)

	ui := &termio.Mock{}
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
	_, err = SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		ui,
		false, // restart=false (user chose "keep")
		VMBootStarted,
		mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}

	wrote := fs.Writes != nil && fs.Writes[ocPath] != nil
	if !wrote {
		t.Error("expected updated opencode.json to be provisioned even when daemon restart is deferred (keep)")
	}
}

// TestSetUpSandboxRunsHooksOnlyOnBoot verifies that startup hooks run only
// when the VM transitioned to running this run (started/created), and are
// skipped when the VM was already running and merely connected to.
func TestSetUpSandboxRunsHooksOnlyOnBoot(t *testing.T) {
	seams := healthyOpencodeDaemon(t)

	fs := msb.NewTestFS(nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	ui := &termio.Mock{}
	cfs := &reprovision.ConfigFiles{
		Hooks: []homeconfig.HookSpec{
			{Target: "/home/dev/.hello.sh", Source: "x", HasShebang: true},
		},
	}

	for _, tc := range []struct {
		name   string
		boot   VMBoot
		expect bool
	}{
		{"connected VM skips hooks", VMBootConnected, false},
		{"started VM runs hooks", VMBootStarted, true},
		{"created VM runs hooks", VMBootCreated, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb.AttachCmd = ""
			sb.AttachUser = ""
			_, err := SetUpSandbox(
				context.Background(),
				testVMKey(),
				sb,
				opencodeResolved(t),
				options.RunOptions{Agent: "opencode"},
				seams,
				cfs,
				ui,
				false,
				tc.boot,
				mounts.WorkspaceMountPath,
			)
			if err != nil {
				t.Fatalf("SetUpSandbox: %v", err)
			}
			ran := sb.AttachCmd != ""
			if ran != tc.expect {
				t.Errorf("hook ran = %v, want %v", ran, tc.expect)
			}
		})
	}
}

func TestSetUpSandboxReturnsGuestCWD(t *testing.T) {
	seams := healthyOpencodeDaemon(t)

	fs := msb.NewTestFS(nil)
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), opencodeAgent(t).ConfigDirName())),
		"opencode-x.json5",
	)
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
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
	target, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		ui,
		false,
		VMBootStarted,
		"/workspace/sub",
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if target != "/workspace/sub" {
		t.Errorf("target = %q, want %q", target, "/workspace/sub")
	}
}

// healthyOpencodeDaemon returns options.Seams whose daemon health probe answers
// healthy, so SetUpSandbox finds the opencode daemon running and starts nothing.
func healthyOpencodeDaemon(t *testing.T) options.Seams {
	t.Helper()
	return options.Seams{DaemonHealth: func(_ context.Context, _ msb.Sandbox, command string) (string, int, error) {
		if command == opencodeProvider(t).DaemonHealthCmd() {
			return `{"healthy":true,"version":"test"}`, 0, nil
		}
		return "", 0, nil
	}}
}

func setUpOpencodeSandbox(
	t *testing.T,
	sb *msb.MockSandbox,
	cfs *reprovision.ConfigFiles,
	ui termio.UI,
	boot VMBoot,
	seams options.Seams,
) error {
	t.Helper()
	_, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		ui,
		false,
		boot,
		mounts.WorkspaceMountPath,
	)
	return err
}

// opencodeMergedConfigPath is where the merged opencode config lands in the
// sandbox home (reprovision exposes it as ConfigFiles.MergedPath).
const opencodeMergedConfigPath = "/home/dev/.config/opencode/opencode.jsonc"

func setUpSandboxProvisionsConfig(t *testing.T) {
	t.Helper()
	seams := healthyOpencodeDaemon(t)

	fs := msb.NewTestFS(nil) // empty FS simulates a VM with empty config dir
	sb := &msb.MockSandbox{Name_: "test-vm", FSValue_: fs}
	configpaths.WithMockConfigPaths(t)
	cp := configpaths.Get()
	snippet := filepath.Join(
		configpaths.CreateDir(filepath.Join(cp.UserConfigDir(), opencodeAgent(t).ConfigDirName())),
		"opencode-x.json5",
	)
	if err := os.MkdirAll(filepath.Dir(snippet), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, snippet, `{"model":"x"}`)

	ui := &termio.Mock{}
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
	target, err := SetUpSandbox(
		context.Background(),
		testVMKey(),
		sb,
		opencodeResolved(t),
		options.RunOptions{Agent: "opencode"},
		seams,
		cfs,
		ui,
		false,
		VMBootStarted,
		mounts.WorkspaceMountPath,
	)
	if err != nil {
		t.Fatalf("SetUpSandbox: %v", err)
	}
	if target != defaultTargetDir {
		t.Errorf("target = %q, want %q", target, defaultTargetDir)
	}

	wroteConfig := fs.Writes != nil &&
		fs.Writes[opencodeMergedConfigPath] != nil
	if !wroteConfig {
		t.Error("expected the merged config to be provisioned, but it was never written")
	}
}
