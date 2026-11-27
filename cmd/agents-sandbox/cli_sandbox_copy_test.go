package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// setupCopyConfig installs a mock msb client for a sandbox copy command.
func setupCopyConfig(
	t *testing.T,
	args []string,
	mockSetup func(*msbtest.MockMsbClient),
) (*cobra.Command, *termiotest.Mock) {
	t.Helper()
	cmd, ui := setupCommandFixtures(t, args...)
	mock := &msbtest.MockMsbClient{}
	if mockSetup != nil {
		mockSetup(mock)
	}
	msbtest.WithMsbMock(t, mock)
	return cmd, ui
}

// runningCopyMock sets a running project VM whose sandbox exposes fs.
func runningCopyMock(fs *msbtest.TestFS) func(*msbtest.MockMsbClient) {
	return func(m *msbtest.MockMsbClient) {
		m.SetGotSandbox(&msbtest.MockSandboxHandle{
			Name_: "vm", Status_: msb.SandboxStatusRunning,
			ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
		})
	}
}

func TestSandboxCopyHostToVM(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, ui := setupCopyConfig(
		t,
		[]string{"sandbox", "cp", "./patch.diff", "vm:/workspace/sub/patch.diff"},
		runningCopyMock(fs),
	)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp: %v", err)
	}
	if got := fs.CopiedFromHost; len(got) != 1 || got[0].Destination != "/workspace/sub/patch.diff" {
		t.Fatalf("CopiedFromHost = %+v", got)
	}
	if len(fs.Mkdirs) != 1 || fs.Mkdirs[0] != "/workspace/sub" {
		t.Fatalf("Mkdirs = %v, want the nested guest parent", fs.Mkdirs)
	}
	if !containsNormalized(ui.InfoCalls, "Copied ./patch.diff -> vm:/workspace/sub/patch.diff") {
		t.Fatalf("InfoCalls = %v", ui.InfoCalls)
	}
}

func TestSandboxCopyVMToHost(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, _ := setupCopyConfig(
		t,
		[]string{"sandbox", "copy", "vm:/workspace/out.diff", "./out.diff"},
		runningCopyMock(fs),
	)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox copy: %v", err)
	}
	if got := fs.CopiedToHost; len(got) != 1 || got[0].Destination != "./out.diff" {
		t.Fatalf("CopiedToHost = %+v", got)
	}
}

func TestSandboxCopyRejectsOperands(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"two vm operands", []string{"vm:/a", "vm:/b"}, "exactly one"},
		{"no vm operand", []string{"./a", "./b"}, "exactly one"},
		{"relative guest destination", []string{"./a", "vm:rel/b"}, "must be absolute"},
		{"relative guest source", []string{"vm:rel/a", "./b"}, "must be absolute"},
		{"empty guest destination", []string{"./a", "vm:"}, "must be absolute"},
		{"empty guest source", []string{"vm:", "./b"}, "must be absolute"},
		{"json and quiet", []string{"--format", "json", "-q", "./a", "vm:/b"}, "mutually exclusive"},
		{"unsupported format", []string{"--format", "yaml", "./a", "vm:/b"}, "yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initTestProjectDir(t)
			cmd, _ := setupCopyConfig(t, append([]string{"sandbox", "copy"}, tt.args...), nil)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSandboxCopyRequiresRunningVM(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "cp", "./a", "vm:/b"},
		func(m *msbtest.MockMsbClient) {
			m.SetGotSandbox(&msbtest.MockSandboxHandle{
				Name_: "vm", Status_: msb.SandboxStatusStopped,
				ConnectSb: &msbtest.MockSandbox{FSValue_: fs},
			})
		})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error = %v, want a not-running refusal", err)
	}
	if len(fs.CopiedFromHost) != 0 {
		t.Error("a stopped VM must not copy anything")
	}
}

func TestSandboxCopyQuietSuppressesOutput(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCopyConfig(
		t,
		[]string{"sandbox", "cp", "-q", "./a", "vm:/b"},
		runningCopyMock(msbtest.NewTestFS(nil)),
	)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp -q: %v", err)
	}
	if len(ui.InfoCalls) != 0 || len(ui.OutCalls) != 0 {
		t.Errorf("quiet copy wrote output: info=%v out=%v", ui.InfoCalls, ui.OutCalls)
	}
}

func TestSandboxCopyJSON(t *testing.T) {
	initTestProjectDir(t)
	cmd, ui := setupCopyConfig(
		t,
		[]string{"sandbox", "cp", "--format", "json", "./patch.diff", "vm:/workspace/patch.diff"},
		runningCopyMock(msbtest.NewTestFS(nil)),
	)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sandbox cp --format json: %v", err)
	}
	if len(ui.OutCalls) != 1 {
		t.Fatalf("OutCalls = %v, want the one JSON document", ui.OutCalls)
	}
	var got copyJSON
	if err := json.Unmarshal([]byte(ui.OutCalls[0]), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", ui.OutCalls[0], err)
	}
	if got.VM != "vm" || got.Direction != "host-to-vm" || got.Source != "./patch.diff" ||
		got.Destination != "vm:/workspace/patch.diff" {
		t.Fatalf("copyJSON = %+v", got)
	}
}

func TestSandboxCopySurfacesCopyError(t *testing.T) {
	initTestProjectDir(t)
	fs := msbtest.NewTestFS(nil)
	fs.CopyErr = errBoom
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "cp", "./a", "vm:/b"}, runningCopyMock(fs))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "copy ./a -> /b") {
		t.Fatalf("error = %v, want the copy failure", err)
	}
}

// TestTopLevelCopyRunsTheTransfer pins the root registration and the
// `sandbox copy` alias annotation: both top-level spellings must reach the
// transfer seam, not just the nested `sandbox` form.
func TestTopLevelCopyRunsTheTransfer(t *testing.T) {
	for _, name := range []string{"copy", "cp"} {
		t.Run(name, func(t *testing.T) {
			initTestProjectDir(t)
			fs := msbtest.NewTestFS(nil)
			cmd, ui := setupCopyConfig(
				t,
				[]string{name, "./patch.diff", "vm:/workspace/patch.diff"},
				runningCopyMock(fs),
			)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got := fs.CopiedFromHost; len(got) != 1 || got[0].Destination != "/workspace/patch.diff" {
				t.Fatalf("CopiedFromHost = %+v", got)
			}
			if !containsNormalized(ui.InfoCalls, "Copied ./patch.diff -> vm:/workspace/patch.diff") {
				t.Fatalf("InfoCalls = %v", ui.InfoCalls)
			}
		})
	}
}

// TestCopyCommandHasAgentFlag asserts the top-level copy command exposes the
// --agent flag defaulting to defaultAgentName.
func TestCopyCommandHasAgentFlag(t *testing.T) {
	ui := &termiotest.Mock{}
	root := buildRootCmd(ui)
	cmd, _, err := root.Find([]string{cmdCopy})
	if err != nil {
		t.Fatalf("copy command not found: %v", err)
	}
	flag := cmd.Flags().Lookup(flagAgent)
	if flag == nil {
		t.Fatal("copy command missing --agent flag")
	}
	if got := flag.DefValue; got != defaultAgentName {
		t.Errorf("--agent default = %q, want %q", got, defaultAgentName)
	}
}

// TestSandboxCopyUnknownAgent asserts copy rejects an unknown --agent before it
// reaches the transfer.
func TestSandboxCopyUnknownAgent(t *testing.T) {
	initTestProjectDir(t)
	cmd, _ := setupCopyConfig(t, []string{"sandbox", "copy", "--agent", "bogus", "./a", "vm:/b"}, nil)
	assertErrContains(t, cmd.Execute(), `unknown agent "bogus"`)
}

// TestCopyUseTheConfiguredAgent pins that copy acts on the agent the launcher
// config or AGENTS_SANDBOX_AGENT names, as stop/kill do, and that an explicit
// --agent still wins over both.
func TestCopyUseTheConfiguredAgent(t *testing.T) {
	for name, tc := range map[string]struct {
		config, env string
		flagArgs    []string
		want        string
	}{
		"config key":           {config: "agent: pi\n", want: "pi"},
		"environment variable": {env: "pi", want: "pi"},
		"flag over both":       {config: "agent: pi\n", env: "pi", flagArgs: []string{"--agent", "codex"}, want: "codex"},
	} {
		t.Run(name, func(t *testing.T) {
			initTestProjectDir(t)
			if tc.env != "" {
				t.Setenv("AGENTS_SANDBOX_AGENT", tc.env)
			}
			var looked []string
			cmd, _ := setupCopyConfig(t, append([]string{cmdCopy, "./a", "vm:/b"}, tc.flagArgs...),
				func(m *msbtest.MockMsbClient) {
					m.GetSandboxFn = func(_ context.Context, name string) (msb.SandboxHandle, error) {
						looked = append(looked, name)
						return &msbtest.MockSandboxHandle{
							Name_: "vm", Status_: msb.SandboxStatusRunning,
							ConnectSb: &msbtest.MockSandbox{FSValue_: msbtest.NewTestFS(nil)},
						}, nil
					}
				})
			if tc.config != "" {
				testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", tc.config)
			}

			if err := cmd.Execute(); err != nil {
				t.Fatalf("copy: %v", err)
			}
			if len(looked) == 0 || !strings.HasSuffix(looked[0], "-"+tc.want) {
				t.Errorf("looked up %v, want the %s project VM", looked, tc.want)
			}
		})
	}
}
