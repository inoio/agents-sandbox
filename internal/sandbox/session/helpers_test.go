package session

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/learn"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/prepare"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// stubHostOpener swaps the host-opener seams. The watch stub blocks until its
// context is cancelled, like the real watcher, and returns report.
func stubHostOpener(t *testing.T, volumePath string, report opener.Report, watchErr error) *[]string {
	t.Helper()
	var watchedPaths []string
	originalResolve, originalWatch := resolveHomeVolumePath, watchHostOpener
	resolveHomeVolumePath = func(_ context.Context, volumeName string) (string, error) {
		if volumeName != "home-vol" {
			t.Errorf("resolved volume %q, want home-vol", volumeName)
		}
		return volumePath, nil
	}
	watchHostOpener = func(ctx context.Context, homeVolumePath string, _ msb.SandboxExecutor) (opener.Report, error) {
		watchedPaths = append(watchedPaths, homeVolumePath)
		<-ctx.Done()
		return report, watchErr
	}
	t.Cleanup(func() { resolveHomeVolumePath, watchHostOpener = originalResolve, originalWatch })
	return &watchedPaths
}

// opencodeResolved returns the registered opencode profile with its
// capabilities resolved, which implements SessionStatusProvider (the v1
// session-status endpoints the quiescence wait polls).
func opencodeResolved(t *testing.T) agent.Resolved {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return agent.Resolve(a)
}

// noSessionStatusAgent implements agent.Agent without SessionStatusProvider,
// modeling daemons whose servers expose no v1 session-status endpoints.
type noSessionStatusAgent struct{}

func (noSessionStatusAgent) Name() string { return "no-session-status" }

func (noSessionStatusAgent) Experimental() bool { return false }

func (noSessionStatusAgent) ConfigDirName() string { return "nosessionstatus" }

func (noSessionStatusAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// errWithResultSandbox returns output alongside a Shell error, modeling a client
// that produced a partial result before failing. A plain MockSandbox cannot: it
// returns either a result or an error, never both.
type errWithResultSandbox struct {
	*msbtest.MockSandbox

	out msb.ShellResult
	err error
}

func (s errWithResultSandbox) Shell(
	context.Context,
	string,
	msb.ExecRequest,
) (msb.ShellResult, error) {
	return s.out, s.err
}

// canceledContextSandbox models the real SDK's Shell, which aborts on a canceled
// context (exec.go/ffi.go: `case <-ctx.Done(): return ctx.Err()`). A plain
// MockSandbox ignores ctx, so it cannot reproduce the serve-only exit path where
// the reaper's context is already canceled.
type canceledContextSandbox struct {
	msb.Sandbox
}

func (s canceledContextSandbox) Shell(
	ctx context.Context,
	command string,
	req msb.ExecRequest,
) (msb.ShellResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Sandbox.Shell(ctx, command, req)
}

// nonAttachAgent implements agent.Agent but not agent.AttachRunner, so
// buildAttachCommand must return "" for it.
type nonAttachAgent struct{}

func (nonAttachAgent) Name() string { return "nonattach" }

func (nonAttachAgent) Experimental() bool { return false }

func (nonAttachAgent) ConfigDirName() string { return "nonattach" }

func (nonAttachAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

type fakePrepared struct {
	sb                msb.Sandbox
	target            string
	serveHostPort     int
	servePortDeferred bool
	guestCWD          string
	key               state.Key
	homeVolume        string
	limits            prepare.AppliedLimits
}

func (f *fakePrepared) Cleanup() {}

func (f *fakePrepared) Sandbox() msb.Sandbox { return f.sb }

func (f *fakePrepared) Target() string { return f.target }

func (f *fakePrepared) ServeHostPort() int { return f.serveHostPort }

func (f *fakePrepared) ServePortDeferred() bool { return f.servePortDeferred }

func (f *fakePrepared) GuestCWD() string { return f.guestCWD }

func (f *fakePrepared) Key() state.Key { return f.key }

func (f *fakePrepared) HomeVolume() string { return f.homeVolume }

func (f *fakePrepared) AppliedLimits() prepare.AppliedLimits { return f.limits }

// fakeLearnRun is a learnSession double that records each lifecycle call's name,
// so a test can assert the order session.Run drives it in without a real
// observation channel or home volume. baselineErr and finishErr drive the
// failure branches; finishInput captures what the after-run was handed.
type fakeLearnRun struct {
	overlay     network.Policy
	passive     bool
	stopped     bool
	finished    bool
	preserved   bool
	baselineErr error
	finishErr   error
	preserveErr error
	finishInput learn.FinishInput
	events      *[]string
}

func (f *fakeLearnRun) Overlay() network.Policy { return f.overlay }

func (f *fakeLearnRun) Passive() bool { return f.passive }

func (f *fakeLearnRun) Stop() channel.Observation {
	f.record("stop")
	f.stopped = true
	return channel.Observation{}
}

func (f *fakeLearnRun) CaptureBaseline(context.Context, state.Key, string) (string, error) {
	f.record("baseline")
	if f.baselineErr != nil {
		return "", f.baselineErr
	}
	return "/home/vol", nil
}

func (f *fakeLearnRun) Finish(_ context.Context, in learn.FinishInput) error {
	f.record("finish")
	f.finished = true
	f.finishInput = in
	return f.finishErr
}

func (f *fakeLearnRun) PreserveForReplay(termio.Logger) error {
	f.record("preserve")
	f.preserved = true
	return f.preserveErr
}

func (f *fakeLearnRun) record(event string) {
	if f.events != nil {
		*f.events = append(*f.events, event)
	}
}
