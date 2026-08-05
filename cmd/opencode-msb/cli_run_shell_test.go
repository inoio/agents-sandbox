package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// setupRunMocks configures all mock dependencies needed for run/shell tests.
// It always returns the given mock so callers can inspect its call history, and
// also returns a cleanup function that restores the original factory.
func setupRunMocks(t *testing.T, mock *sandbox.MockMsbClient, sandboxToReturn sandbox.Sandbox) {
	t.Helper()
	mock.CreatedSandbox = sandboxToReturn

	// The default GetSandbox error must be an msb.Error with ErrSandboxNotFound
	// so EnsureProjectVM treats it as "not found → create" rather than a real error.
	mock.SetGetSandboxErr(&msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"})

	origMSB := sandbox.SetNewMsbClient(func() sandbox.MsbClient { return mock })
	t.Cleanup(func() { sandbox.SetNewMsbClient(origMSB) })

	docker.WithNoopDockerMock(t)
	origCheck := sandbox.SetEnsureInstalled(func(_ context.Context) error { return nil })
	t.Cleanup(func() { sandbox.SetEnsureInstalled(origCheck) })

	origCheckAll := sandbox.CheckAllFunc
	sandbox.CheckAllFunc = func(context.Context, stdio.UI) bool { return true }
	t.Cleanup(func() { sandbox.CheckAllFunc = origCheckAll })

	origShell := sandbox.SetDaemonShellFunc(
		func(ctx context.Context, sb sandbox.Sandbox, command string) (string, int, error) {
			_ = ctx
			_ = sb
			_ = command
			return `{"healthy": true}`, 0, nil
		},
	)
	t.Cleanup(func() { sandbox.SetDaemonShellFunc(origShell) })
}

// setupShellRunMocks is like setupRunMocks but adds shell output for worktree creation.
// Branch scenarios need the mock sandbox to return valid JSON for the worktree curl command.
func setupShellRunMocks(t *testing.T, mock *sandbox.MockMsbClient, sandboxToReturn sandbox.Sandbox) {
	t.Helper()

	// Ensure shell responses include a JSON worktree response for curl commands.
	if sb, ok := sandboxToReturn.(*sandbox.MockSandbox); ok {
		if sb.ShellOut == nil {
			sb.ShellOut = make(map[string]sandbox.ShellResult)
		}
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"x\"}'"] = &workspaceResult{
			dir: "/workspace/worktrees/x",
		}
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"main\"}'"] = &workspaceResult{
			dir: "/workspace/worktrees/main",
		}
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"foo\"}'"] = &workspaceResult{
			dir: "/workspace/worktrees/foo",
		}
	}

	setupRunMocks(t, mock, sandboxToReturn)
}

// workspaceResult implements sandbox.ShellResult for worktree responses.
type workspaceResult struct {
	dir string
}

func (w *workspaceResult) Success() bool       { return true }
func (w *workspaceResult) ExitCode() int       { return 0 }
func (w *workspaceResult) Stdout() string      { return `{"directory":"` + w.dir + `"}` }
func (w *workspaceResult) Stderr() string      { return "" }
func (w *workspaceResult) StdoutBytes() []byte { return []byte(w.Stdout()) }

// R1: run --dry-run.
func TestRunShell_R1_dryRunRun(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--dry-run"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would run opencode" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would run opencode'; got: %v", ui.InfoCalls)
	}

	foundVerbose := false
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "dry-run-vm: auto-enabled") {
			foundVerbose = true
			break
		}
	}
	if !foundVerbose {
		t.Errorf("expected verbose 'dry-run-vm: auto-enabled'; got: %v", ui.VerboseCalls)
	}
}

// R2: shell --dry-run.
func TestRunShell_R2_dryRunShell(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell", "--dry-run"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would start interactive shell session" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would start interactive shell session'; got: %v", ui.InfoCalls)
	}
}

// R3: run --dry-run --dry-run-vm.
func TestRunShell_R3_dryRunWithVmRun(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--dry-run", "--dry-run-vm"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would run opencode" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would run opencode'; got: %v", ui.InfoCalls)
	}
}

// R4: shell --dry-run --dry-run-vm.
func TestRunShell_R4_dryRunWithVmShell(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell", "--dry-run", "--dry-run-vm"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would start interactive shell session" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would start interactive shell session'; got: %v", ui.InfoCalls)
	}
}

// R5: run (default) → error from Attach.
func TestRunShell_R5_runDefaultAttachError(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("connection refused")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from attach failure; got nil")
	}
	if !strings.Contains(err.Error(), "opencode session failed") {
		t.Errorf("expected error containing 'opencode session failed'; got: %v", err)
	}
}

// R6: shell (default) → error from Attach.
func TestRunShell_R6_shellDefaultAttachError(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("shell error")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from attach failure; got nil")
	}
	if !strings.Contains(err.Error(), "opencode session failed") {
		t.Errorf("expected error containing 'opencode session failed'; got: %v", err)
	}
}

// R7: run --dry-run --no-auto.
func TestRunShell_R7_dryRunNoAuto(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--dry-run", "--no-auto"})

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would run opencode" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would run opencode'; got: %v", ui.InfoCalls)
	}
}

// R8: shell no --auto (shell always has Auto=false).
func TestRunShell_R8_shellNoAuto(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell"})
	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R9: run with --branch --cpus --memory --user.
func TestRunShell_R9_runWithAllFlags(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--branch", "x", "--cpus", "2", "--memory", "8G", "--user", "alice"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R10: run with short flags.
func TestRunShell_R10_runWithShortFlags(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--branch", "main", "-c", "4", "-m", "16G", "-u", "root"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R11: run success (Attach returns code 0).
func TestRunShell_R11_runSuccessDetachOk(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachCode: 0, AttachErr: nil})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run"})

	err := root.Execute()
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got: %T (%v)", err, err)
	}
	if exitErr.Code != 0 {
		t.Errorf("expected exit code 0, got %d", exitErr.Code)
	}
}

// R12: shell with --branch --cpus.
func TestRunShell_R12_shellWithBranchCpus(t *testing.T) {
	initTestRepo(t)

	ui := &stdio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell", "--branch", "foo", "--cpus", "2"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}
