package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
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
