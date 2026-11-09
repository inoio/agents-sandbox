package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// statePresent reports whether the key has a readable state file, failing the
// test on any read error other than a missing file.
func statePresent(t *testing.T, k state.Key) bool {
	t.Helper()
	_, err := state.ReadState(k)
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrStateNotFound):
		return false
	default:
		t.Fatalf("ReadState(%v): %v", k, err)
		return false
	}
}

// nilResultDockerd models a sandbox whose dockerd readiness probe returns a
// nil (nil, nil) result during polling.
type nilResultDockerd struct {
	msb.Sandbox

	readyCalls int
}

func (s *nilResultDockerd) Shell(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	switch command {
	case dockerdBinaryCheckCmd:
		return msbtest.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		if s.readyCalls >= 2 {
			return nil, nil //nolint:nilnil // models an SDK nil-result probe
		}
		return msbtest.NewTestResult(false, 1, "", "", nil), nil
	}
	return s.Sandbox.Shell(ctx, command, req)
}

// fakeAgent implements only the core Agent interface with no capabilities, so
// the daemon/worktree paths must no-op for it.
type fakeAgent struct{}

func (fakeAgent) Name() string { return "fake" }

func (fakeAgent) Experimental() bool { return false }

func (fakeAgent) ConfigDirName() string { return "fake" }

func (fakeAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{}
}

func unhealthy() mockShellResp { return mockShellResp{stdout: "", exitCode: 1} }

func healthy() mockShellResp { return mockShellResp{stdout: `{"healthy":true}`, exitCode: 0} }

// assertDetachedHelperLaunch fails the test unless the recorded detached calls
// are exactly one launch of binary under the helper marker; the marker shape is
// what the kill path matches on, so the callers (dockerd, the daemon) are pinned
// to it.
func assertDetachedHelperLaunch(t *testing.T, calls []msbtest.MockDetachedCall, marker, binary string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("detached calls = %+v, want one", calls)
	}
	if calls[0].Cmd != "env" {
		t.Errorf("launch binary = %q, want %q", calls[0].Cmd, "env")
	}
	args := calls[0].Args
	if len(args) < 2 || args[0] != "AGENTS_SANDBOX_DETACHED_JOB="+marker || args[1] != binary {
		t.Errorf("launch args = %v, want the %s marker then %q", args, marker, binary)
	}
}

type mockShellResp struct {
	stdout   string
	exitCode int
	err      error
}

// fakeDaemonProvider is a minimal DaemonProvider driving the health probe
// helper directly.
type fakeDaemonProvider struct {
	healthCmd string
	parse     func(string) (bool, error)
}

func (fakeDaemonProvider) DaemonCommand(bool) agent.DaemonCommand { return agent.DaemonCommand{} }

func (f fakeDaemonProvider) DaemonHealthCmd() string { return f.healthCmd }

func (f fakeDaemonProvider) DaemonHealthParse(stdout string) (bool, error) {
	return f.parse(stdout)
}

func (fakeDaemonProvider) DaemonAuthHint() string { return "" }

// orderRecordingJob is a MockJob whose Kill appends the given event to a shared
// call-order log, so a test can place the stale kill in the restart sequence
// rather than only observing that it happened.
type orderRecordingJob struct {
	*msbtest.MockJob

	onKill func()
}

func (j orderRecordingJob) Kill(ctx context.Context) error {
	j.onKill()
	return j.MockJob.Kill(ctx)
}

// dockerdStartSandbox drives startDockerdIfPresent down its start path (the
// pre-restart readiness probe fails, the post-restart probe succeeds) and
// records every shell command. ExecDetached is delegated to the embedded
// MockSandbox.
type dockerdStartSandbox struct {
	msb.Sandbox

	calls      []string
	readyCalls int
}

func (s *dockerdStartSandbox) Shell(
	_ context.Context,
	command string,
	_ msb.ExecRequest,
) (msb.ShellResult, error) {
	s.calls = append(s.calls, command)
	switch command {
	case dockerdBinaryCheckCmd:
		return msbtest.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		return msbtest.NewTestResult(s.readyCalls > 1, 0, "", "", nil), nil
	}
	return msbtest.NewTestResult(true, 0, "", "", nil), nil
}

// existsErrorFS is a SandboxFS whose Exists fails while every other operation is
// delegated to the embedded test FS, so a test can exercise readGuestDaemonConfig's
// inspect-failure branch independently of Read.
type existsErrorFS struct {
	*msbtest.TestFS

	err error
}

func (f existsErrorFS) Exists(context.Context, string) (bool, error) {
	return false, f.err
}

// countingSandbox is an msbtest.MockSandbox that records how many detached jobs it
// launched and models dockerd becoming ready after a number of readiness
// probes, so tests can assert which branches startDockerdIfPresent took.
type countingSandbox struct {
	*msbtest.MockSandbox

	execCalls               int
	execErr                 error
	cleanupErr              error
	readyCalls              int
	readyCallsBeforeHealthy int
	execArgs                *[]string
}

func (s *countingSandbox) Shell(_ context.Context, command string, _ msb.ExecRequest) (msb.ShellResult, error) {
	switch command {
	case dockerdBinaryCheckCmd:
		return msbtest.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		if s.readyCalls > s.readyCallsBeforeHealthy {
			return msbtest.NewTestResult(true, 0, "", "", nil), nil
		}
		return msbtest.NewTestResult(false, 1, "", "", nil), nil
	case dockerdCleanupCmd:
		if s.cleanupErr != nil {
			return msbtest.NewTestResult(false, 1, "", "", nil), s.cleanupErr
		}
	}
	return msbtest.NewTestResult(true, 0, "", "", nil), nil
}

func (s *countingSandbox) ExecDetached(
	ctx context.Context,
	cmd string,
	args []string,
	spec msb.JobSpec,
) (msb.Job, error) {
	s.execCalls++
	if s.execErr != nil {
		return nil, s.execErr
	}
	if s.execArgs == nil {
		s.execArgs = &args
	} else {
		*s.execArgs = args
	}
	return s.MockSandbox.ExecDetached(ctx, cmd, args, spec)
}

// userRecordingSandbox is an msb.MockSandbox that records every shell command
// with the user it ran as, so a test can pin which setup steps run as root and
// which as the engine's user.
type userRecordingSandbox struct {
	*msb.MockSandbox

	commands []string
	users    []string
}

func (s *userRecordingSandbox) Shell(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	s.commands = append(s.commands, command)
	s.users = append(s.users, req.User)
	return s.MockSandbox.Shell(ctx, command, req)
}

// commandErrorSandbox is an msb.MockSandbox whose Shell fails with err for one
// command and delegates every other command.
type commandErrorSandbox struct {
	*msb.MockSandbox

	failing string
	err     error
}

func (s *commandErrorSandbox) Shell(ctx context.Context, command string, req msb.ExecRequest) (msb.ShellResult, error) {
	if command == s.failing {
		return nil, s.err
	}
	return s.MockSandbox.Shell(ctx, command, req)
}

// seedHomeState writes hs as the key's HomeState through the recovery
// transaction, overwriting any existing document. Tests only.
func seedHomeState(k state.Key, hs state.HomeState) error {
	return state.RecoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		*cur = hs
		return true, nil
	})
}

// branchWorktreeAgent is a fake whose daemon keeps a branch on a new worktree:
// it implements DetachedWorktreeReporter but reports false, so the detached-HEAD
// note must stay empty.
type branchWorktreeAgent struct{ fakeAgent }

func (branchWorktreeAgent) WorktreeCreatesDetachedHead() bool { return false }

// failingCommandSandbox fails one shell command with err and lets every other
// command through to the embedded mock, so a test can fail one step of a
// multi-command flow.
type failingCommandSandbox struct {
	*msbtest.MockSandbox

	command string
	err     error
}

func (s failingCommandSandbox) Shell(
	ctx context.Context, command string, req msb.ExecRequest,
) (msb.ShellResult, error) {
	if command == s.command {
		return nil, s.err
	}
	return s.MockSandbox.Shell(ctx, command, req)
}

var _ agent.Agent = branchWorktreeAgent{}

// recordingWorktreeAgent records the project and spec ResolveTarget hands to the
// provider, so the seam contract is pinned without a real agent.
type recordingWorktreeAgent struct {
	fakeAgent

	resolveTarget string
	resolveCalls  int
	badProject    bool
	badList       bool
	badDir        bool
	listProject   agent.WorktreeProject
	createSpec    agent.WorktreeSpec
	createProject agent.WorktreeProject
}

func (r *recordingWorktreeAgent) WorktreeResolveCmd(target string) string {
	r.resolveTarget = target
	r.resolveCalls++
	return "resolve"
}

func (r *recordingWorktreeAgent) WorktreeParseProject(string) (agent.WorktreeProject, bool) {
	if r.badProject {
		return agent.WorktreeProject{}, false
	}
	return agent.WorktreeProject{ID: "abc", Canonical: "/repo"}, true
}

func (r *recordingWorktreeAgent) WorktreeListCmd(project agent.WorktreeProject) string {
	r.listProject = project
	return "list"
}

func (r *recordingWorktreeAgent) WorktreeParseList(string, agent.WorktreeProject) ([]string, bool) {
	if r.badList {
		return nil, false
	}
	return []string{}, true
}

func (r *recordingWorktreeAgent) WorktreeCreateCmd(spec agent.WorktreeSpec, project agent.WorktreeProject) string {
	r.createSpec = spec
	r.createProject = project
	return "create"
}

func (r *recordingWorktreeAgent) WorktreeParseDir(string) (string, bool) {
	if r.badDir {
		return "", false
	}
	return "/workspace/wt/feat-x", true
}
