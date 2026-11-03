package vm

import (
	"context"
	"errors"
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
)

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
			name:        "stale files to remove",
			cfs:         &reprovision.ConfigFiles{Remove: []string{path}},
			provisioned: func(fs *msb.TestFS) bool { return slices.Contains(fs.Removed, path) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seams := healthyOpencodeDaemon(t)
			configpaths.WithMockConfigPaths(t)
			fs := msb.NewTestFS(map[string][]byte{path: []byte("old")}, nil)
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
			fs := msb.NewTestFS(map[string][]byte{agentSourcePath: []byte(state.AgentSourceUser + "\n")}, nil)
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

// TestSetUpSandboxWrapsDockerdError pins that a dockerd startup failure keeps
// its cause, so a caller can tell it apart from other setup errors.
func TestSetUpSandboxWrapsDockerdError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	shellErr := errors.New("dockerd binary check failed")
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil), ShellErr: shellErr}
	ui := termio.NewTestMock(t)

	err := setUpOpencodeSandbox(t, sb, &reprovision.ConfigFiles{}, &ui, VMBootConnected, options.Seams{})
	if !errors.Is(err, shellErr) {
		t.Fatalf("SetUpSandbox error = %v, want it to wrap %v", err, shellErr)
	}
}

// TestSetUpSandboxReportsWhatItDoes pins the messages a user sees while the
// sandbox is set up: the expected config files, each startup hook, and a
// daemon restart.
func TestSetUpSandboxReportsWhatItDoes(t *testing.T) {
	seams := healthyOpencodeDaemon(t)
	configpaths.WithMockConfigPaths(t)
	sb := &msb.MockSandbox{Name_: "vm", FSValue_: msb.NewTestFS(nil, nil)}
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
