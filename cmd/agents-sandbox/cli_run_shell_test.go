package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configmigration"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

// setupRunMocks configures all mock dependencies needed for run/shell tests,
// builds the root command for the given args, and returns it with the UI so
// callers can Execute and assert. The common configpaths/noop-docker/doctor
// mocks come from the command fixture. It always registers the given mock so
// callers can inspect its call history, and installs a daemon shell stub that
// reports the sandbox healthy.
func setupRunMocks(t *testing.T, mock *sandboxmsb.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox,
	args ...string) (*cobra.Command, *termio.Mock) {
	t.Helper()
	mock.CreatedSandbox = sandboxToReturn

	cmd, ui := setupCommandFixtures(t, args...)

	// The default GetSandbox error must be an msb.Error with ErrSandboxNotFound
	// so EnsureProjectVM treats it as "not found → create" rather than a real error.
	sandboxmsb.WithMsbMock(t, mock.SetGetSandboxErr(&msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}))

	origShell := sandbox.SetDaemonShellFunc(
		func(ctx context.Context, sb sandboxmsb.Sandbox, command string) (string, int, error) {
			_ = ctx
			_ = sb
			_ = command
			return `{"healthy": true}`, 0, nil
		},
	)
	t.Cleanup(func() { sandbox.SetDaemonShellFunc(origShell) })

	return cmd, ui
}

// setupShellRunMocks is like setupRunMocks but adds shell output for worktree creation.
// Worktree scenarios need the mock sandbox to return valid JSON for the worktree curl commands.
func setupShellRunMocks(t *testing.T, mock *sandboxmsb.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox,
	args ...string) (*cobra.Command, *termio.Mock) {
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

	return setupRunMocks(t, mock, sandboxToReturn, args...)
}

func TestRunShellDryRunRun(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "--dry-run")

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would run agent session" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would run agent session'; got: %v", ui.InfoCalls)
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

func TestRunShellDryRunShell(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "shell", "--dry-run")

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

func TestRunShellRunAttachError(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("connection refused")}, "run")

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from attach failure; got nil")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("expected error containing 'agent session failed'; got: %v", err)
	}
}

func TestRunShellShellAttachError(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("shell error")}, "shell")

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from attach failure; got nil")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("expected error containing 'agent session failed'; got: %v", err)
	}
}

func TestRunShellRunWithAllFlags(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "x", "--cpus", "2", "--memory", "8G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellRunWithShortFlags(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "main", "-c", "4", "-m", "16G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellCleanExitNoError(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 0, AttachErr: nil}, "run")

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error on clean exit, got: %T (%v)", err, err)
	}
}

func TestRunShellWorktreeReusesExisting(t *testing.T) {
	initTestRepo(t)

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
	root, ui := setupRunMocks(t, mock, sb, "run", "--worktree", "bugfix-exit-zero")
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

func TestRunShellWithCpus(t *testing.T) {
	initTestRepo(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupShellRunMocks(
		t,
		mock,
		&sandboxmsb.MockSandbox{AttachErr: errors.New("fail")},
		"shell",
		"--cpus",
		"2",
	)

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellWorktreeRejectsNonSlug(t *testing.T) {
	initTestRepo(t)
	mock := &sandboxmsb.MockMsbClient{}
	sb := &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")}
	root, _ := setupRunMocks(t, mock, sb, "run", "--worktree", "feature/foo")
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
	mock := &sandboxmsb.MockMsbClient{}
	if tc.sandbox == nil {
		tc.sandbox = &sandboxmsb.MockSandbox{}
	}
	root, _ := setupRunMocks(t, mock, tc.sandbox, tc.args...)

	doctor.MockedCheckAll(t, tc.doctorPass)

	err := root.Execute()
	if err == nil {
		t.Errorf("expected error containing %q, got none", tc.wantErrPart)
		return
	}
	if !strings.Contains(err.Error(), tc.wantErrPart) {
		t.Errorf("expected error containing %q, got: %v", tc.wantErrPart, err)
	}
}

func TestRunShellPreflightFailure(t *testing.T) {
	for _, tc := range []runShellScenario{
		{name: "run", args: []string{"run"}, wantErrPart: "preflight failed"},
		{name: "shell", args: []string{"shell"}, wantErrPart: "preflight failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRunShellErrorScenario(t, tc)
		})
	}
}

func TestRunShellRunNonZeroExit(t *testing.T) {
	initTestRepo(t)
	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 5}, "run")
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-zero exit code")
	}
	var exitErr *sandbox.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("expected sandbox.ExitError, got %T: %v", err, err)
		return
	}
	if exitErr.Code != 5 {
		t.Errorf("ExitError.Code = %d, want 5", exitErr.Code)
	}
}

func TestRunShellInvalidSizeFlags(t *testing.T) {
	for _, tc := range []runShellScenario{
		{name: "tmp-size", args: []string{"run", "--tmp-size", "bogus"}, wantErrPart: "invalid --tmp-size"},
		{name: "disk-size", args: []string{"run", "--disk-size", "bogus"}, wantErrPart: "invalid --disk-size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestRepo(t)
			mock := &sandboxmsb.MockMsbClient{}
			root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, tc.args...)

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

func TestHandleMigrationError(t *testing.T) {
	if err := handleMigrationError(configmigration.ErrRerunRequired); err == nil {
		t.Fatal("expected exit error")
	}
	want := errors.New("migration failed")
	if err := handleMigrationError(want); !errors.Is(err, want) {
		t.Fatalf("handleMigrationError = %v, want original error", err)
	}
}

func TestGuideConfigMigrationErrorBranches(t *testing.T) {
	ui := &termio.Mock{}
	if err := guideConfigMigration(options.RunOptions{DryRun: false}, ui, true); err != nil {
		t.Fatalf("dry-run guidance error = %v", err)
	}
	if err := guideConfigMigration(
		options.RunOptions{Agent: "unknown"},
		ui,
		false,
	); err == nil ||
		!strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("unknown agent guidance error = %v", err)
	}
	originalHome := migrationHomeDir
	t.Cleanup(func() { migrationHomeDir = originalHome })
	migrationHomeDir = func() (string, error) { return "", errors.New("home failed") }
	if err := guideConfigMigration(
		options.RunOptions{Agent: "opencode"},
		ui,
		false,
	); err == nil ||
		!strings.Contains(err.Error(), "home failed") {
		t.Fatalf("home guidance error = %v", err)
	}
}

func TestGuideConfigMigrationOffersClaudeCodeSetupWithoutNativeConfig(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	hostHome := t.TempDir()
	originalHome := migrationHomeDir
	t.Cleanup(func() { migrationHomeDir = originalHome })
	migrationHomeDir = func() (string, error) { return hostHome, nil }
	ui := termio.NewTestMock(t)
	ui.IsInteractiveResult = true
	var prompts []string
	ui.SelectFn = func(prompt string, _ []termio.Choice, _ string) (string, error) {
		prompts = append(prompts, prompt)
		return "d", nil
	}
	if err := guideConfigMigration(options.RunOptions{Agent: "claude-code"}, &ui, false); err != nil {
		t.Fatalf("guideConfigMigration: %v", err)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0], "Set up") {
		t.Fatalf("prompts = %v, want the Claude Code setup prompt", prompts)
	}
}

func TestRunPropagatesMigrationGuidanceError(t *testing.T) {
	initTestRepo(t)
	originalHome := migrationHomeDir
	t.Cleanup(func() { migrationHomeDir = originalHome })
	migrationHomeDir = func() (string, error) { return "", errors.New("home failed") }

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run")
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "resolve host home: home failed") {
		t.Fatalf("run migration guidance error = %v", err)
	}
}

func TestRunPropagatesPostMigrationRefreshError(t *testing.T) {
	originalRefresh := refreshRunOptionsAfterMigrationFn
	t.Cleanup(func() { refreshRunOptionsAfterMigrationFn = originalRefresh })
	refreshRunOptionsAfterMigrationFn = func(*cobra.Command, []string, termio.UI) (options.RunOptions, *launcherconfig.Resolver, error) {
		return options.RunOptions{}, nil, errors.New("refresh failed")
	}
	initTestRepo(t)
	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "ping")
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("post-migration refresh error = %v", err)
	}
}

func TestRefreshRunOptionsAfterMigrationErrors(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	cmd := buildRunCmd(&termio.Mock{})
	cmd.SetContext(context.WithValue(
		context.Background(),
		(*launcherConfigKey)(nil),
		launcherconfig.NewResolverWithConfig(launcherconfig.Config{}),
	))
	oldHome := migrationHomeDir
	t.Cleanup(func() { migrationHomeDir = oldHome })
	if err := os.MkdirAll(configpaths.Get().UserConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WritePath(t, filepath.Join(configpaths.Get().UserConfigDir(), "config.yaml"), "network: [")
	if _, _, err := refreshRunOptionsAfterMigration(cmd, nil, &termio.Mock{}); err == nil {
		t.Fatal("expected resolver refresh error")
	}
}
