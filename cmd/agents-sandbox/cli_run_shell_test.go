package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
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

	origShell := sandbox.SetDaemonHealthFunc(
		func(ctx context.Context, sb sandboxmsb.Sandbox, command string) (string, int, error) {
			_ = ctx
			_ = sb
			_ = command
			// A body both daemon parsers accept: v1 reads `healthy`, v2 reads a
			// version from its ServerInfo.
			return `{"healthy":true,"version":"0.0.0-test"}`, 0, nil
		},
	)
	t.Cleanup(func() { sandbox.SetDaemonHealthFunc(origShell) })

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
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "--dry-run-vm")

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInfo := false
	for _, call := range ui.InfoCalls {
		if strings.TrimSpace(call) == "dry-run: Would start agent session in VM" {
			foundInfo = true
			break
		}
	}
	if !foundInfo {
		t.Errorf("expected info 'dry-run: Would start agent session in VM'; got: %v", ui.InfoCalls)
	}
}

func TestRunShellDryRunShell(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "shell", "--dry-run-vm")

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
	initTestProjectDir(t)

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
	initTestProjectDir(t)

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
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "x", "--cpus", "2", "--memory", "8G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellRunWithShortFlags(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "main", "-c", "4", "-m", "16G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellCleanExitNoError(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{AttachCode: 0, AttachErr: nil}, "run")

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error on clean exit, got: %T (%v)", err, err)
	}
}

func TestRunShellWorktreeReusesExisting(t *testing.T) {
	initTestProjectDir(t)

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
	root, ui := setupRunMocks(t, mock, sb, "run", "--agent", "opencode", "--worktree", "bugfix-exit-zero")
	_ = root.Execute()

	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, "reusing existing worktree \"bugfix-exit-zero\"") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected default-visible 'reusing existing worktree'; got: %v", ui.InfoCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestRunShellWithCpus(t *testing.T) {
	initTestProjectDir(t)

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

// TestRunShellWorktreeOpencode2ReusesExisting mirrors the v1 reuse test for
// opencode2: the --worktree gate admits opencode2, the launcher drives v2's
// project-scoped list command, and an existing worktree is reused without a
// create. It fails if the gate stops admitting opencode2, the launch directory
// stops reaching the v2 command, or the list route regresses.
func TestRunShellWorktreeOpencode2ReusesExisting(t *testing.T) {
	initTestProjectDir(t)

	a, ok := agent.Lookup("opencode2")
	if !ok {
		t.Fatal("opencode2 agent not registered")
	}
	provider, ok := agent.AsWorktreeProvider(a)
	if !ok {
		t.Fatal("opencode2 does not implement WorktreeProvider")
	}

	mock := &sandboxmsb.MockMsbClient{}
	sb := &sandboxmsb.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandboxmsb.ShellResult{},
		ShellCalls: &[]string{},
	}
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	sb.ShellOut[provider.WorktreeResolveCmd(mounts.WorkspaceMountPath)] = sandboxmsb.NewTestResult(
		true,
		0,
		`{"project":{"id":"abc","canonical":"/repo"}}`,
		"",
		nil,
	)
	sb.ShellOut[provider.WorktreeListCmd(project)] = sandboxmsb.NewTestResult(
		true,
		0,
		`[{"directory":"/home/dev/.local/share/opencode/worktree/abc/bugfix-exit-zero"}]`,
		"",
		nil,
	)
	root, ui := setupRunMocks(t, mock, sb, "run", "--agent", "opencode2", "--worktree", "bugfix-exit-zero")
	_ = root.Execute()

	found := false
	for _, call := range ui.InfoCalls {
		if strings.Contains(call, `reusing existing worktree "bugfix-exit-zero"`) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected default-visible 'reusing existing worktree'; got: %v", ui.InfoCalls)
	}
	for _, call := range *sb.ShellCalls {
		if strings.Contains(call, "POST /api/worktree") {
			t.Errorf("expected reuse without create, but created a new worktree: %q", call)
		}
	}
}

func TestRunShellWorktreeRejectsNonSlug(t *testing.T) {
	initTestProjectDir(t)
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
	initTestProjectDir(t)
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
	initTestProjectDir(t)
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
		{name: "tmp-size-out-of-range", args: []string{"run", "--tmp-size", "4294967296"}, wantErrPart: "invalid --tmp-size"},
		{name: "disk-size-out-of-range", args: []string{"run", "--disk-size", "4294967296"}, wantErrPart: "invalid --disk-size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestProjectDir(t)
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

// setupAbsentImagePreviewMocks makes the fresh-project preview report an absent
// image: it overrides the command fixture's noop docker mock so the local
// inspect misses, and fails the test if the preview resolves a version, builds
// an image, or creates a volume. Call it AFTER setupRunMocks: the two
// t.Cleanup-based overrides (agent-version resolver, docker) are scoped to the
// calling test by cleanup ordering, while the CreateVolumeFn assignment is a
// direct per-test field change on the caller's mock.
func setupAbsentImagePreviewMocks(t *testing.T, mock *sandboxmsb.MockMsbClient) {
	t.Helper()
	image.WithMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("the --dry-run preview must not resolve an agent version")
		return "", nil
	})
	docker.WithDockerMock(t, &docker.MockDockerClient{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("no such image")
		},
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			t.Error("the --dry-run preview must not build the runner image")
			return client.ImageBuildResult{}, errors.New("unexpected build")
		},
	})
	mock.CreateVolumeFn = func(context.Context, string, ...msb.VolumeOption) (sandboxmsb.VolumeHandle, error) {
		t.Error("the --dry-run preview must not create a home volume")
		return nil, errors.New("unexpected CreateVolume")
	}
}

// assertPreviewInfoLines pins the four rendered preview lines for a fresh
// project with an absent image: version, build decision, home volume, session.
// The runner tag embeds the temp project's slug, so it is asserted by shape.
func assertPreviewInfoLines(t *testing.T, ui *termio.Mock, shell bool) {
	t.Helper()
	calls := ui.InfoCalls
	if len(calls) != 4 {
		t.Fatalf("expected exactly the four preview lines, got %d: %v", len(calls), calls)
	}
	if got := strings.TrimSpace(calls[0]); got != "dry-run: agent version resolved at build time" {
		t.Errorf("preview line 1 = %q; want the resolved-at-build message", got)
	}
	imageLine := strings.TrimSpace(calls[1])
	if !strings.HasPrefix(imageLine, "dry-run: would build runner image ") {
		t.Errorf("preview line 2 = %q; want a would-build line", imageLine)
	}
	if !strings.Contains(imageLine, "(runner image not present locally)") {
		t.Errorf("preview line 2 = %q; want the absent-image reason", imageLine)
	}
	if got := strings.TrimSpace(calls[2]); got != "dry-run: would create home volume" {
		t.Errorf("preview line 3 = %q; want would-create home volume", got)
	}
	wantLast := "dry-run: would start agent session"
	if shell {
		wantLast = "dry-run: would start interactive shell session"
	}
	if got := strings.TrimSpace(calls[3]); got != wantLast {
		t.Errorf("preview line 4 = %q; want %q", got, wantLast)
	}
}

// TestRunDryRunPreviewCLI pins the run-level read-only preview: `run --dry-run`
// on a fresh project renders the four plan lines and builds nothing, creates no
// volume, and never resolves a version.
func TestRunDryRunPreviewCLI(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "--dry-run")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, false)
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestShellDryRunPreviewCLI pins the shell-level preview: same read-only plan,
// with the shell session as its last line.
func TestShellDryRunPreviewCLI(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "shell", "--dry-run")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, true)
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunDryRunWithPinnedVersionReportsPin verifies an explicit --agent-version
// is reported verbatim and the upgrade/latest-version seam is never reached.
func TestRunDryRunWithPinnedVersionReportsPin(t *testing.T) {
	initTestProjectDir(t)

	orig := sandbox.SetAgentLatestVersion(func(context.Context, agent.Agent) (string, error) {
		t.Error("the --dry-run preview must not run the agent latest-version check")
		return "", nil
	})
	t.Cleanup(func() { sandbox.SetAgentLatestVersion(orig) })

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "--dry-run", "--agent-version", "9.9.9")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ui.InfoCalls) == 0 || strings.TrimSpace(ui.InfoCalls[0]) != "dry-run: agent version 9.9.9" {
		t.Errorf("expected pinned version line 'dry-run: agent version 9.9.9'; got: %v", ui.InfoCalls)
	}
}

// TestRunDryRunServeOnlyIsPurePreview verifies --serve-only combined with
// --dry-run returns from the preview before serving, so it neither hangs nor
// attaches.
func TestRunDryRunServeOnlyIsPurePreview(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{}, "run", "--dry-run", "--serve-only")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, false)
}

// TestRunDryRunAgentCodex verifies `run --agent codex` resolves the codex
// profile and previews without starting a VM.
func TestRunDryRunAgentCodex(t *testing.T) {
	initTestProjectDir(t)

	mock := &sandboxmsb.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &sandboxmsb.MockSandbox{},
		"run", "--dry-run", "--agent", "codex", "--agent-version", "0.164.0")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 0.164.0" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned codex version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for codex; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentCodexRejectsDaemonOnlyFlags verifies codex, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentCodexRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "codex"}, flagArgs...)
			root, _ := setupRunMocks(t, &sandboxmsb.MockMsbClient{}, &sandboxmsb.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "codex"`) {
				t.Errorf("err = %v, want 'not supported by agent \"codex\"'", err)
			}
		})
	}
}
