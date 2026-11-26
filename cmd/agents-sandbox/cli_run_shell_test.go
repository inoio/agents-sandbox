package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"
	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/hostfigures"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker/dockertest"
	"github.com/inoio/agents-sandbox/internal/sandbox/doctor/doctortest"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// setupRunMocks configures all mock dependencies needed for run/shell tests,
// builds the root command for the given args, and returns it with the UI so
// callers can Execute and assert. The common configpaths/noop-docker/doctor
// mocks come from the command fixture. It always registers the given mock so
// callers can inspect its call history, and installs a daemon shell stub that
// reports the sandbox healthy.
func setupRunMocks(t *testing.T, mock *msbtest.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox,
	args ...string,
) (*cobra.Command, *termiotest.Mock) {
	t.Helper()
	mock.CreatedSandbox = sandboxToReturn

	cmd, ui := setupCommandFixtures(t, args...)

	// The default GetSandbox error must be an msb.Error with ErrSandboxNotFound
	// so EnsureProjectVM treats it as "not found → create" rather than a real error.
	msbtest.WithMsbMock(t, mock.SetGetSandboxErr(&msb.Error{Kind: msb.ErrSandboxNotFound, Message: "not found"}))

	origSeams := runSeams
	runSeams.DaemonHealth = func(ctx context.Context, sb sandboxmsb.Sandbox, command string) (string, int, error) {
		_ = ctx
		_ = sb
		_ = command
		// A body both daemon parsers accept: v1 reads `healthy`, v2 reads a
		// version from its ServerInfo.
		return `{"healthy":true,"version":"0.0.0-test"}`, 0, nil
	}
	t.Cleanup(func() { runSeams = origSeams })

	return cmd, ui
}

// setupShellRunMocks is like setupRunMocks but adds shell output for worktree creation.
// Worktree scenarios need the mock sandbox to return valid JSON for the worktree curl commands.
func setupShellRunMocks(t *testing.T, mock *msbtest.MockMsbClient, sandboxToReturn sandboxmsb.Sandbox,
	args ...string,
) (*cobra.Command, *termiotest.Mock) {
	t.Helper()

	// Ensure shell responses include a JSON worktree response for curl commands.
	if sb, ok := sandboxToReturn.(*msbtest.MockSandbox); ok {
		if sb.ShellOut == nil {
			sb.ShellOut = make(map[string]sandboxmsb.ShellResult)
		}
		sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = msbtest.NewTestResult(
			true,
			0,
			`[]`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"x\"}'"] = msbtest.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/x"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"main\"}'"] = msbtest.NewTestResult(
			true,
			0,
			`{"directory":"/workspace/worktrees/main"}`,
			"",
			nil,
		)
		sb.ShellOut["curl -sf -X POST http://127.0.0.1:4096/experimental/worktree -H 'Content-Type: application/json' -d '{\"name\":\"foo\"}'"] = msbtest.NewTestResult(
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

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run-vm")

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

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "shell", "--dry-run-vm")

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

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run")
	mock.CreatedSandbox = attachFailingWith(mock, errors.New("connection refused"), sandboxmsb.SandboxStatusRunning)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from attach failure; got nil")
	}
	if !strings.Contains(err.Error(), "agent session failed") {
		t.Errorf("expected error containing 'agent session failed'; got: %v", err)
	}
}

// TestShellDoesNotOfferFirstRunMenu pins the P1 ruling: `session.Shell` never
// starts the agent serve daemon or a learning session, so the first-run menu
// must not be offered on `shell` — a learning choice there would record a
// decision with no session behind it and suppress the menu on the next run.
func TestShellDoesNotOfferFirstRunMenu(t *testing.T) {
	initTestProjectDir(t)

	menuCalled := false
	original := firstRunMenu
	firstRunMenu = func(termio.UI, *options.RunOptions, string, string) error {
		menuCalled = true
		return nil
	}
	t.Cleanup(func() { firstRunMenu = original })

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{AttachErr: errors.New("stop after menu check")}, "shell")
	// The attach fails by design; only whether the menu was offered matters.
	_ = root.Execute()

	if menuCalled {
		t.Error("shell must not offer the first-run menu")
	}
}

// TestRunDryRunVMDoesNotOfferFirstRunMenu pins the P1 ruling on the other
// non-attach path: `run --dry-run-vm` prepares everything but returns before the
// learning after-run, so a learning choice would be recorded while the
// observation is dropped. The menu must not be offered there.
func TestRunDryRunVMDoesNotOfferFirstRunMenu(t *testing.T) {
	initTestProjectDir(t)

	menuCalled := false
	original := firstRunMenu
	firstRunMenu = func(termio.UI, *options.RunOptions, string, string) error {
		menuCalled = true
		return nil
	}
	t.Cleanup(func() { firstRunMenu = original })

	captured := captureSessionRun(t)
	root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, cmdRun, "--dry-run-vm")
	if err := root.Execute(); err != nil {
		t.Fatalf("run --dry-run-vm: %v", err)
	}
	if menuCalled {
		t.Error("run --dry-run-vm must not offer the first-run menu")
	}
	if captured().Learn {
		t.Error("run --dry-run-vm must not start a learning session")
	}
}

// attachFailingWith is a sandbox whose attach fails with attachErr and, once it
// has, lets the mock find the project VM in status: the lookup a failed attach
// makes. Before the attach the lookup reports the VM absent, so the run creates
// it as usual.
func attachFailingWith(
	mock *msbtest.MockMsbClient,
	attachErr error,
	status sandboxmsb.SandboxStatus,
) *msbtest.MockSandbox {
	attached := false
	absent := mock.GetSandboxFn
	mock.GetSandboxFn = func(ctx context.Context, name string) (sandboxmsb.SandboxHandle, error) {
		if !attached {
			return absent(ctx, name)
		}
		return &msbtest.MockSandboxHandle{Status_: status}, nil
	}
	return &msbtest.MockSandbox{AttachFn: func(context.Context) (int, error) {
		attached = true
		return 0, attachErr
	}}
}

// TestRunReportsAVMThatStoppedDuringTheAttach pins the run's error when the
// project VM went away under the attach (killed from another terminal): it
// names the stopped VM and the next step instead of the lost connection, and
// the command fails.
func TestRunReportsAVMThatStoppedDuringTheAttach(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run")
	mock.CreatedSandbox = attachFailingWith(mock,
		errors.New("agent connection closed during terminal attach"), sandboxmsb.SandboxStatusStopped)

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "the project VM stopped while the session was attached") {
		t.Fatalf("err = %v, want the VM-stopped message", err)
	}
}

func TestRunShellShellAttachError(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "shell")
	mock.CreatedSandbox = attachFailingWith(mock, errors.New("shell error"), sandboxmsb.SandboxStatusRunning)

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

	mock := &msbtest.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &msbtest.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "x", "--cpus", "2", "--memory", "8G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellRunWithShortFlags(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, _ := setupShellRunMocks(t, mock, &msbtest.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--worktree", "main", "-c", "4", "-m", "16G")

	_ = root.Execute()

	if len(mock.CreatedSandboxCalls) < 1 {
		t.Fatalf("expected at least 1 CreateSandbox call, got %d", len(mock.CreatedSandboxCalls))
	}
}

func TestRunShellCleanExitNoError(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{AttachCode: 0, AttachErr: nil}, "run")

	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error on clean exit, got: %T (%v)", err, err)
	}
}

func TestRunShellWorktreeReusesExisting(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandboxmsb.ShellResult{},
		ShellCalls: &[]string{},
	}
	sb.ShellOut["curl -sf http://127.0.0.1:4096/experimental/worktree"] = msbtest.NewTestResult(
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

	mock := &msbtest.MockMsbClient{}
	root, _ := setupShellRunMocks(
		t,
		mock,
		&msbtest.MockSandbox{AttachErr: errors.New("fail")},
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

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{
		AttachErr:  errors.New("fail"),
		ShellOut:   map[string]sandboxmsb.ShellResult{},
		ShellCalls: &[]string{},
	}
	project := agent.WorktreeProject{ID: "abc", Canonical: "/repo"}
	sb.ShellOut[provider.WorktreeResolveCmd(mounts.WorkspaceMountPath)] = msbtest.NewTestResult(
		true,
		0,
		`{"project":{"id":"abc","canonical":"/repo"}}`,
		"",
		nil,
	)
	sb.ShellOut[provider.WorktreeListCmd(project)] = msbtest.NewTestResult(
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
	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{AttachErr: errors.New("fail")}
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
	sandbox     *msbtest.MockSandbox
	wantErrPart string
}

func runRunShellErrorScenario(t *testing.T, tc runShellScenario) {
	t.Helper()
	initTestProjectDir(t)
	mock := &msbtest.MockMsbClient{}
	if tc.sandbox == nil {
		tc.sandbox = &msbtest.MockSandbox{}
	}
	root, _ := setupRunMocks(t, mock, tc.sandbox, tc.args...)

	doctortest.MockedCheckAll(t, tc.doctorPass)

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

// TestRunShellPreviewSkipsPreflight pins that a --dry-run preview of run and
// shell alike skips the doctor preflight, which may install the microsandbox
// runtime: a failing preflight must not abort the preview.
func TestRunShellPreviewSkipsPreflight(t *testing.T) {
	for _, command := range []string{cmdRun, cmdShell} {
		t.Run(command, func(t *testing.T) {
			initTestProjectDir(t)
			mock := &msbtest.MockMsbClient{}
			root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, command, "--dry-run")
			doctortest.MockedCheckAll(t, false)

			if err := root.Execute(); err != nil {
				t.Fatalf("%s --dry-run with a failing preflight: %v", command, err)
			}
		})
	}
}

// TestRunShellMemoryTwoGiBHalvesTmpSize pins the memory-relative tmp default
// end to end: with --tmp-size unset and --memory 2G, the run must not reject the
// implicit tmp size and must mount a 1 GiB /tmp. The flag's default is the empty
// string so the default stays memory-relative (options.ResolveTmpSizeMiB),
// rather than a fixed literal that collides with a smaller memory.
func TestRunShellMemoryTwoGiBHalvesTmpSize(t *testing.T) {
	initTestProjectDir(t)
	// Pin the boot maximum above 2G so --memory 2G is not clamped by a small host.
	t.Cleanup(hostfigures.OverrideTotalMemoryGiBForTest(func() int { return 8 }))

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{AttachErr: errors.New("fail")},
		"run", "--memory", "2G")

	_ = root.Execute()

	var tmpSizeMiB uint32
	found := false
	for _, call := range mock.CreatedSandboxCalls {
		if mount, ok := call.Spec.Mounts[mounts.TmpMountPath]; ok {
			tmpSizeMiB = mount.SizeMiB
			found = true
		}
	}
	if !found {
		t.Fatalf("no created sandbox mounted %s; creations: %v", mounts.TmpMountPath, mock.CreatedSandboxCalls)
	}
	if tmpSizeMiB != 1024 {
		t.Errorf("/tmp size = %d MiB, want 1024 (half of --memory 2G)", tmpSizeMiB)
	}
}

func TestRunShellRunNonZeroExit(t *testing.T) {
	initTestProjectDir(t)
	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{AttachCode: 5}, "run")
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
		{name: "memory-below-floor", args: []string{"run", "--memory", "64M"}, wantErrPart: "--memory 64M"},
		{name: "tmp-size-at-memory", args: []string{"run", "--memory", "4G", "--tmp-size", "4G"}, wantErrPart: "--tmp-size 4G must be below memory"},
		{name: "disk-size-zero", args: []string{"run", "--disk-size", "0"}, wantErrPart: "--disk-size 0 must be greater than 0"},
		{name: "workspace-quota-zero", args: []string{"run", "--workspace-quota", "0"}, wantErrPart: "--workspace-quota 0 must be greater than 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestProjectDir(t)
			mock := &msbtest.MockMsbClient{}
			root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, tc.args...)

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
func setupAbsentImagePreviewMocks(t *testing.T, mock *msbtest.MockMsbClient) {
	t.Helper()
	withMockAgentVersionResolver(t, func(context.Context, agent.Agent, string) (string, error) {
		t.Fatal("the --dry-run preview must not resolve an agent version")
		return "", nil
	})
	dockertest.WithDockerMock(t, &dockertest.MockDockerClient{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, errors.New("no such image")
		},
		ImageBuildFn: func(context.Context, io.Reader, client.ImageBuildOptions) (client.ImageBuildResult, error) {
			t.Error("the --dry-run preview must not build the runner image")
			return client.ImageBuildResult{}, errors.New("unexpected build")
		},
	})
	mock.CreateVolumeFn = func(context.Context, string, sandboxmsb.VolumeSpec) (sandboxmsb.VolumeHandle, error) {
		t.Error("the --dry-run preview must not create a home volume")
		return nil, errors.New("unexpected CreateVolume")
	}
}

// assertPreviewInfoLines pins the four rendered preview lines for a fresh
// project with an absent image: version, build decision, home volume, session.
// The runner tag embeds the temp project's slug, so it is asserted by shape.
func assertPreviewInfoLines(t *testing.T, ui *termiotest.Mock, shell bool) {
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

// TestRunDryRunLearnIsPurePreview pins that --learn combined with --dry-run
// returns from the read-only preview: no channel starts, no VM is created.
func TestRunDryRunLearnIsPurePreview(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run", "--learn")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, false)
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--learn --dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunDryRunPreviewCLI pins the run-level read-only preview: `run --dry-run`
// on a fresh project renders the four plan lines and builds nothing, creates no
// volume, and never resolves a version.
func TestRunDryRunPreviewCLI(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, false)
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunDryRunRejectsInvalidDNSServers pins F8 at the CLI boundary: an invalid
// `--dns` value fails at config-load, even under `--dry-run`, instead of exiting
// 0 only to fail at VM start. The env/config path already rejects it.
func TestRunDryRunRejectsInvalidDNSServers(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, _ := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run", "--dns", "garbage")

	err := root.Execute()
	if err == nil {
		t.Fatal("`run --dry-run --dns garbage` = nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "invalid DNS server") {
		t.Errorf("error = %v, want it to name the invalid DNS server", err)
	}
}

// TestShellDryRunPreviewCLI pins the shell-level preview: same read-only plan,
// with the shell session as its last line.
func TestShellDryRunPreviewCLI(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "shell", "--dry-run")
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

	origSeams := runSeams
	runSeams.AgentLatestVersion = func(context.Context, agent.Agent) (string, error) {
		t.Error("the --dry-run preview must not run the agent latest-version check")
		return "", nil
	}
	t.Cleanup(func() { runSeams = origSeams })

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run", "--agent-version", "9.9.9")
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

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run", "--serve-only")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertPreviewInfoLines(t, ui, false)
}

// TestRunDryRunAgentPi verifies `run --agent pi` resolves the pi profile,
// prints no experimental notice (pi is a tested agent), and previews without
// starting a VM.
func TestRunDryRunAgentPi(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "pi", "--agent-version", "1.6.0")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 1.6.0" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			t.Errorf("unexpected experimental notice for pi: %q", trimmed)
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned pi version line; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunDryRunAgentCodex verifies `run --agent codex` resolves the codex
// profile and previews without starting a VM.
func TestRunDryRunAgentCodex(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
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

// TestRunForwardsAgentArgs verifies the positional args after `--` reach the
// agent attach as forwarded arguments: dropping opts.Args would swallow them.
func TestRunForwardsAgentArgs(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	sb := &msbtest.MockSandbox{}
	root, _ := setupRunMocks(t, mock, sb, "run", "--", "--model", "sonnet")

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(sb.AttachArgs, " ")
	if !strings.Contains(joined, "'--model' 'sonnet'") {
		t.Errorf("AttachArgs = %q, want the forwarded --model sonnet args", sb.AttachArgs)
	}
}

// TestRunAgentCodexRejectsDaemonOnlyFlags verifies codex, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentCodexRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "codex"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "codex"`) {
				t.Errorf("err = %v, want 'not supported by agent \"codex\"'", err)
			}
		})
	}
}

// TestRunDryRunAgentOhMyPi verifies `run --agent oh-my-pi` resolves the
// oh-my-pi profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentOhMyPi(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "oh-my-pi", "--agent-version", "18.14.0")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 18.14.0" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned oh-my-pi version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for oh-my-pi; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentOhMyPiRejectsDaemonOnlyFlags verifies oh-my-pi, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentOhMyPiRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "oh-my-pi"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "oh-my-pi"`) {
				t.Errorf("err = %v, want 'not supported by agent \"oh-my-pi\"'", err)
			}
		})
	}
}

// TestRunDryRunAgentKimiCode verifies `run --agent kimi-code` resolves the
// kimi-code profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentKimiCode(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "kimi-code", "--agent-version", "2.7.0")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 2.7.0" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned kimi-code version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for kimi-code; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentKimiCodeRejectsDaemonOnlyFlags verifies kimi-code, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentKimiCodeRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "kimi-code"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "kimi-code"`) {
				t.Errorf("err = %v, want 'not supported by agent \"kimi-code\"'", err)
			}
		})
	}
}

// TestRunDryRunAgentQwenCode verifies `run --agent qwen-code` resolves the
// qwen-code profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentQwenCode(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "qwen-code", "--agent-version", "0.28.0")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 0.28.0" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned qwen-code version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for qwen-code; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentQwenCodeRejectsDaemonOnlyFlags verifies qwen-code, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentQwenCodeRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "qwen-code"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "qwen-code"`) {
				t.Errorf("err = %v, want 'not supported by agent \"qwen-code\"'", err)
			}
		})
	}
}

// writeHostOpencodeManifest writes a ~/.config/opencode/package.json declaring
// a dependency into the mocked host home and returns its path.
func writeHostOpencodeManifest(t *testing.T) string {
	t.Helper()
	hostHome, err := configpaths.Get().UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	dir := filepath.Join(hostHome, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	manifest := filepath.Join(dir, "package.json")
	testutil.WritePath(t, manifest, `{"dependencies": {"zod": "4.1.8"}}`)
	return manifest
}

func countWarningsMentioning(warnings []string, substr string) int {
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning, substr) {
			count++
		}
	}
	return count
}

func TestRunWarnsAboutUnprovisionedConfigDirDependencies(t *testing.T) {
	initTestProjectDir(t)
	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run-vm")
	manifest := writeHostOpencodeManifest(t)
	// Provisioning is opt-in; enable it so the manifest is actually copied and
	// the dependency warning fires.
	enableHostConfigProvisioning(t)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := countWarningsMentioning(ui.WarnCalls, manifest); n != 1 {
		t.Errorf("warnings mentioning %s = %d, want 1; got: %v", manifest, n, ui.WarnCalls)
	}
}

// enableHostConfigProvisioning turns on the opt-in host-config drop-in copy in
// the generic user config layer.
func enableHostConfigProvisioning(t *testing.T) {
	t.Helper()
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "provision-host-config: true\n")
}

func TestRunSkipsDependencyWarningWhenAMountSuppliesNodeModules(t *testing.T) {
	initTestProjectDir(t)
	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run-vm")
	manifest := writeHostOpencodeManifest(t)
	testutil.WriteFile(
		t,
		configpaths.Get().UserConfigDir(),
		"config.yaml",
		"provision-host-config: true\nmounts:\n  /home/dev/node_modules:\n    source: "+t.TempDir()+"\n    readonly: true\n",
	)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := countWarningsMentioning(ui.WarnCalls, manifest); n != 0 {
		t.Errorf("warnings mentioning %s = %d, want 0; got: %v", manifest, n, ui.WarnCalls)
	}
}

func TestRunSkipsDependencyWarningWhenHostProvisioningIsDisabled(t *testing.T) {
	initTestProjectDir(t)
	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{}, "run", "--dry-run-vm")
	manifest := writeHostOpencodeManifest(t)
	testutil.WriteFile(t, configpaths.Get().UserConfigDir(), "config.yaml", "provision-host-config: false\n")

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := countWarningsMentioning(ui.WarnCalls, manifest); n != 0 {
		t.Errorf("warnings mentioning %s = %d, want 0; got: %v", manifest, n, ui.WarnCalls)
	}
}

func TestShellCommandInvalidSize(t *testing.T) {
	cmd, _ := setupCommandFixtures(t, "shell", "--tmp-size", "bogus")
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --tmp-size") {
		t.Fatalf("expected invalid --tmp-size error, got %v", err)
	}
}

// startupHookSandbox records every command an attach or a streaming exec ran,
// so a test can tell a startup hook apart from the agent session attach, which
// also goes through AttachWith.
type startupHookSandbox struct {
	*msbtest.MockSandbox

	attachCmds []string
	streamCmds []string
}

func (s *startupHookSandbox) AttachWith(
	ctx context.Context,
	command string,
	args []string,
	req sandboxmsb.AttachRequest,
) (int, error) {
	s.attachCmds = append(s.attachCmds, command)
	return s.MockSandbox.AttachWith(ctx, command, args, req)
}

func (s *startupHookSandbox) ExecStream(
	ctx context.Context,
	command string,
	args []string,
	req sandboxmsb.ExecRequest,
) (sandboxmsb.StreamHandle, error) {
	s.streamCmds = append(s.streamCmds, command)
	return s.MockSandbox.ExecStream(ctx, command, args, req)
}

// TestRunStartupHookHonoursTheInjectedTerminal pins that the run's hook
// interactivity seam reaches the VM stage: with no terminal a hook runs through
// the streaming exec, with one it attaches interactively. It drives `run`, the
// path `--serve-only` shares, because serve-only blocks waiting for input once
// setup completes; the literal `--serve-only </dev/null` sweep is host-only.
func TestRunStartupHookHonoursTheInjectedTerminal(t *testing.T) {
	const hookCommand = "/home/dev/.hello.sh"
	for _, tc := range []struct {
		name        string
		interactive bool
	}{
		{"no terminal streams without input", false},
		{"terminal attaches", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initTestProjectDir(t)

			seams := runSeams
			runSeams.HookInteractive = func() bool { return tc.interactive }
			t.Cleanup(func() { runSeams = seams })

			mock := &msbtest.MockMsbClient{}
			sb := &startupHookSandbox{MockSandbox: &msbtest.MockSandbox{}}
			cmd, _ := setupRunMocks(t, mock, sb, "run")
			writeStartupHookConfig(t)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("run: %v", err)
			}

			if tc.interactive {
				if !slices.Contains(sb.attachCmds, hookCommand) {
					t.Errorf("hook did not attach; attach commands = %v, stream commands = %v",
						sb.attachCmds, sb.streamCmds)
				}
				if slices.Contains(sb.streamCmds, hookCommand) {
					t.Errorf("hook streamed %v, want the interactive attach", sb.streamCmds)
				}
				return
			}
			if !slices.Contains(sb.streamCmds, hookCommand) {
				t.Errorf("hook did not stream; stream commands = %v, attach commands = %v",
					sb.streamCmds, sb.attachCmds)
			}
			if slices.Contains(sb.attachCmds, hookCommand) {
				t.Errorf("hook attached %v, want the streaming exec", sb.attachCmds)
			}
		})
	}
}

// writeStartupHookConfig writes a user-level config that provisions and runs one
// startup hook, resolving its source next to the config file. The source file
// exists so provisioning succeeds and the boot-time hook runs.
func writeStartupHookConfig(t *testing.T) {
	t.Helper()
	userConfigDir := configpaths.Get().UserConfigDir()
	testutil.WriteFile(t, userConfigDir, "hello.sh", "#!/bin/sh\necho hello\n")
	testutil.WriteFile(t, userConfigDir, "config.yaml",
		"home:\n  .hello.sh:\n    source: hello.sh\n    hook: startup\n")
}

// TestRunDryRunAgentGoose verifies `run --agent goose` resolves the
// goose profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentGoose(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "goose", "--agent-version", "1.60.1")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 1.60.1" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned goose version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for goose; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentGooseRejectsDaemonOnlyFlags verifies goose, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentGooseRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "goose"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "goose"`) {
				t.Errorf("err = %v, want 'not supported by agent \"goose\"'", err)
			}
		})
	}
}

// TestRunDryRunAgentGrokBuild verifies `run --agent grok-build` resolves the
// grok-build profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentGrokBuild(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "grok-build", "--agent-version", "1.0.89")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 1.0.89" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned grok-build version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for grok-build; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentGrokBuildRejectsDaemonOnlyFlags verifies grok-build, which runs no
// daemon, rejects --worktree and --serve-only.
func TestRunAgentGrokBuildRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "grok-build"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "grok-build"`) {
				t.Errorf("err = %v, want 'not supported by agent \"grok-build\"'", err)
			}
		})
	}
}

// TestRunDryRunAgentMistralVibe verifies `run --agent mistral-vibe` resolves the
// mistral-vibe profile, notices that it is experimental, and previews without
// starting a VM.
func TestRunDryRunAgentMistralVibe(t *testing.T) {
	initTestProjectDir(t)

	mock := &msbtest.MockMsbClient{}
	root, ui := setupRunMocks(t, mock, &msbtest.MockSandbox{},
		"run", "--dry-run", "--agent", "mistral-vibe", "--agent-version", "3.1.3")
	setupAbsentImagePreviewMocks(t, mock)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var pinnedVersion, experimental bool
	for _, line := range ui.InfoCalls {
		trimmed := strings.TrimSpace(line)
		if trimmed == "dry-run: agent version 3.1.3" {
			pinnedVersion = true
		}
		if strings.Contains(trimmed, "experimental") {
			experimental = true
		}
	}
	if !pinnedVersion {
		t.Errorf("expected pinned mistral-vibe version line; got: %v", ui.InfoCalls)
	}
	if !experimental {
		t.Errorf("expected an experimental notice for mistral-vibe; got: %v", ui.InfoCalls)
	}
	if len(mock.CreatedSandboxCalls) != 0 {
		t.Errorf("--dry-run created %d sandbox(es), want 0", len(mock.CreatedSandboxCalls))
	}
}

// TestRunAgentMistralVibeRejectsDaemonOnlyFlags verifies mistral-vibe, which runs
// no daemon, rejects --worktree and --serve-only.
func TestRunAgentMistralVibeRejectsDaemonOnlyFlags(t *testing.T) {
	for _, flagArgs := range [][]string{{"--worktree", "feat"}, {"--serve-only"}} {
		t.Run(flagArgs[0], func(t *testing.T) {
			initTestProjectDir(t)
			args := append([]string{"run", "--dry-run", "--agent", "mistral-vibe"}, flagArgs...)
			root, _ := setupRunMocks(t, &msbtest.MockMsbClient{}, &msbtest.MockSandbox{}, args...)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), `not supported by agent "mistral-vibe"`) {
				t.Errorf("err = %v, want 'not supported by agent \"mistral-vibe\"'", err)
			}
		})
	}
}
