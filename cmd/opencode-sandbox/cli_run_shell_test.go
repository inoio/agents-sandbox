package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/configpaths"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/docker"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/doctor"
	sandboxmsb "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/session"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// setupRunMocks configures all mock dependencies needed for run/shell tests.
// It always returns the given mock so callers can inspect its call history, and
// also returns a cleanup function that restores the original factory.
func setupRunMocks(t *testing.T, mock *sandboxmsb.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox) {
	t.Helper()
	configpaths.WithMockConfigPaths(t)
	mock.CreatedSandbox = sandboxToReturn

	// The default GetSandbox error must be an msb.Error with ErrSandboxNotFound
	// so EnsureProjectVM treats it as "not found → create" rather than a real error.
	sandboxmsb.WithMsbMock(t, mock.SetGetSandboxErr(&msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}))

	docker.WithNoopDockerMock(t)
	doctor.MockedCheckAll(t, true)

	origShell := session.SetDaemonShellFunc(
		func(ctx context.Context, sb sandboxmsb.Sandbox, command string) (string, int, error) {
			_ = ctx
			_ = sb
			_ = command
			return `{"healthy": true}`, 0, nil
		},
	)
	t.Cleanup(func() { session.SetDaemonShellFunc(origShell) })
}

// setupShellRunMocks is like setupRunMocks but adds shell output for worktree creation.
// Worktree scenarios need the mock sandbox to return valid JSON for the worktree curl commands.
func setupShellRunMocks(t *testing.T, mock *sandboxmsb.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox) {
	t.Helper()

	// Ensure shell responses include a JSON worktree response for curl commands.
	if sb, ok := sandboxToReturn.(*sandboxmsb.MockSandbox); ok {
		if sb.ShellOut == nil {
			sb.ShellOut = make(map[string]sandboxmsb.ShellResult)
		}
		sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = sandboxmsb.NewTestResult(
			true,
			0,
			`[]`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"x\"}'"] = sandboxmsb.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/x"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"main\"}'"] = sandboxmsb.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/main"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"foo\"}'"] = sandboxmsb.NewTestResult(
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
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{})

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
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{})

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

// R5: run (default) → error from Attach.
func TestRunShell_R5_runDefaultAttachError(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("connection refused")})

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
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("shell error")})

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

// R9: run with --worktree --cpus --memory --user.
func TestRunShell_R9_runWithAllFlags(t *testing.T) {
	initTestRepo(t)

	ui := &termio.Mock{}
	mock := &sandboxmsb.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")})

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
	mock := &sandboxmsb.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")})

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
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 0, AttachErr: nil})

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
	mock := &sandboxmsb.MockMsbClient{}
	sb := &sandboxmsb.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandboxmsb.ShellResult{},
		ShellCalls: &[]string{},
	}
	sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = sandboxmsb.NewTestResult(
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
	mock := &sandboxmsb.MockMsbClient{}
	setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"shell", "--cpus", "2"})

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

// W2: run --worktree with a non-slug name fails fast.
func TestRunShell_W2_worktreeRejectsNonSlug(t *testing.T) {
	initTestRepo(t)
	ui := &termio.Mock{}
	mock := &sandboxmsb.MockMsbClient{}
	sb := &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")}
	setupRunMocks(t, mock, sb)

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run", "--worktree", "feature/foo"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-slug worktree name")
	}
}

// runShellScenario drives a run/shell command that must fail with a given
// error substring, with the doctor preflight forced to a controllable result.
type runShellScenario struct {
	name        string
	args        []string
	doctorPass  bool
	sandbox     *sandboxmsb.MockSandbox
	wantErrPart string
}

func runRunShellErrorScenario(t *testing.T, tc runShellScenario) {
	t.Helper()
	initTestRepo(t)
	ui := &termio.Mock{}
	mock := &sandboxmsb.MockMsbClient{}
	if tc.sandbox == nil {
		tc.sandbox = &sandboxmsb.MockSandbox{}
	}
	setupRunMocks(t, mock, tc.sandbox)

	doctor.MockedCheckAll(t, tc.doctorPass)

	root := buildRootCmd(ui)
	root.SetArgs(tc.args)
	err := root.Execute()
	if err == nil {
		t.Errorf("expected error containing %q, got none", tc.wantErrPart)
		return
	}
	if !strings.Contains(err.Error(), tc.wantErrPart) {
		t.Errorf("expected error containing %q, got: %v", tc.wantErrPart, err)
	}
}

func TestRunShell_PreflightFailure(t *testing.T) {
	for _, tc := range []runShellScenario{
		{name: "run", args: []string{"run"}, wantErrPart: "preflight failed"},
		{name: "shell", args: []string{"shell"}, wantErrPart: "preflight failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRunShellErrorScenario(t, tc)
		})
	}
}

// R13: run with a valid start but a non-zero exit code must surface an
// ExitError with that code (not a cobra "Error: exit code" usage dump).
func TestRunShell_R13_runNonZeroExit(t *testing.T) {
	initTestRepo(t)
	ui := &termio.Mock{}
	mock := &sandboxmsb.MockMsbClient{}
	setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 5})

	root := buildRootCmd(ui)
	root.SetArgs([]string{"run"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-zero exit code")
	}
	var exitErr *session.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("expected session.ExitError, got %T: %v", err, err)
		return
	}
	if exitErr.Code != 5 {
		t.Errorf("ExitError.Code = %d, want 5", exitErr.Code)
	}
}

// R14: invalid --tmp-size / --disk-size values must fail through the CLI.
func TestRunShell_R14_InvalidSizeFlags(t *testing.T) {
	for _, tc := range []runShellScenario{
		{name: "tmp-size", args: []string{"run", "--tmp-size", "bogus"}, wantErrPart: "invalid --tmp-size"},
		{name: "disk-size", args: []string{"run", "--disk-size", "bogus"}, wantErrPart: "invalid --disk-size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestRepo(t)
			ui := &termio.Mock{}
			mock := &sandboxmsb.MockMsbClient{}
			setupRunMocks(t, mock, &sandboxmsb.MockSandbox{})

			root := buildRootCmd(ui)
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Errorf("expected error containing %q, got none", tc.wantErrPart)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErrPart, err)
			}
		})
	}
}
