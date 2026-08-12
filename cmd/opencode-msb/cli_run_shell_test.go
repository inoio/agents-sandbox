package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/doctor"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

// setupRunMocks configures all mock dependencies needed for run/shell tests.
// It always returns the given mock so callers can inspect its call history, and
// also returns a cleanup function that restores the original factory.
func setupRunMocks(t *testing.T, mock *sandbox.MockMsbClient, sandboxToReturn sandbox.Sandbox) {
	t.Helper()
	sandbox.WithMockConfigPaths(t)
	mock.CreatedSandbox = sandboxToReturn

	// The default GetSandbox error must be an msb.Error with ErrSandboxNotFound
	// so EnsureProjectVM treats it as "not found → create" rather than a real error.
	sandbox.WithMsbMock(t, mock.SetGetSandboxErr(&msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}))

	docker.WithNoopDockerMock(t)
	origCheck := doctor.SetEnsureInstalled(func(_ context.Context) error { return nil })
	t.Cleanup(func() { doctor.SetEnsureInstalled(origCheck) })

	origCheckAll := doctor.CheckAllFunc
	doctor.CheckAllFunc = func(context.Context, termio.UI) bool { return true }
	t.Cleanup(func() { doctor.CheckAllFunc = origCheckAll })

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
// Worktree scenarios need the mock sandbox to return valid JSON for the worktree curl commands.
func setupShellRunMocks(t *testing.T, mock *sandbox.MockMsbClient, sandboxToReturn sandbox.Sandbox) {
	t.Helper()

	// Ensure shell responses include a JSON worktree response for curl commands.
	if sb, ok := sandboxToReturn.(*sandbox.MockSandbox); ok {
		if sb.ShellOut == nil {
			sb.ShellOut = make(map[string]sandbox.ShellResult)
		}
		sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = sandbox.NewTestResult(
			true,
			0,
			`[]`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"x\"}'"] = sandbox.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/x"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"main\"}'"] = sandbox.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/main"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"foo\"}'"] = sandbox.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/foo"}`,
			"",
			nil,
		)
	}

	setupRunMocks(t, mock, sandboxToReturn)
}

// R1: run --dry-run.
func TestRunShell_R1_dryRunRun(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
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

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell"})
	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R9: run with --worktree --cpus --memory --user.
func TestRunShell_R9_runWithAllFlags(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "x", "--cpus", "2", "--memory", "8G", "--user", "alice"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R10: run with short flags.
func TestRunShell_R10_runWithShortFlags(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "main", "-c", "4", "-m", "16G", "-u", "root"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// R11: run success (Attach returns code 0). A clean exit must not be surfaced
// as a cobra error (would print "Error: exit code 0" + usage).
func TestRunShell_R11_runSuccessDetachOk(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupRunMocks(t, mock, &sandbox.MockSandbox{AttachCode: 0, AttachErr: nil})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error on clean exit, got: %T (%v)", err, err)
	}
}

// R12b: run --worktree reuses an existing worktree instead of creating a new one.
func TestRunShell_R12b_worktreeReusesExistingWorktree(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sb := &sandbox.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandbox.ShellResult{},
		ShellCalls: &[]string{},
	}
	sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = sandbox.NewTestResult(
		true,
		0,
		`["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`,
		"",
		nil,
	)
	setupRunMocks(t, mock, sb)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "bugfix-exit-zero"})
	_ = root.Execute()

	found := false
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "reusing existing worktree \"bugfix-exit-zero\"") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected verbose 'reusing existing worktree'; got: %v", ui.VerboseCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

// R12: shell with --cpus.
func TestRunShell_R12_shellWithBranchCpus(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandbox.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell", "--cpus", "2"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// W1: run --worktree with a valid slug reuses an existing worktree.
func TestRunShell_W1_worktreeReusesExisting(t *testing.T) {
	initTestRepo(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sb := &sandbox.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandbox.ShellResult{},
		ShellCalls: &[]string{},
	}
	sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = sandbox.NewTestResult(
		true, 0, `["/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"]`, "", nil,
	)
	setupRunMocks(t, mock, sb)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "bugfix-exit-zero"})
	_ = root.Execute()

	found := false
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "reusing existing worktree") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected verbose 'reusing existing worktree'; got: %v", ui.VerboseCalls)
	}
}

// W2: run --worktree with a non-slug name fails fast.
func TestRunShell_W2_worktreeRejectsNonSlug(t *testing.T) {
	initTestRepo(t)
	ui := &termio.Mock{}
	mock := &sandbox.MockMsbClient{}
	sb := &sandbox.MockSandbox{AttachErr: errors.New("fail")}
	setupRunMocks(t, mock, sb)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "feature/foo"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-slug worktree name")
	}
}
