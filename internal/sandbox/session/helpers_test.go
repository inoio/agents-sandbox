package session

import (
	"context"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/opener"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
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
	watchHostOpener = func(ctx context.Context, homeVolumePath string, _ msb.Sandbox) (opener.Report, error) {
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
	*msb.MockSandbox

	out msb.ShellResult
	err error
}

func (s errWithResultSandbox) Shell(
	context.Context,
	string,
	...msbSdk.ExecOption,
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
	opts ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Sandbox.Shell(ctx, command, opts...)
}

// nonAttachAgent implements agent.Agent but not agent.AttachRunner, so
// buildAttachCommand must return "" for it.
type nonAttachAgent struct{}

func (nonAttachAgent) Name() string { return "nonattach" }

func (nonAttachAgent) Experimental() bool { return false }

func (nonAttachAgent) ConfigDirName() string { return "nonattach" }

func (nonAttachAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

type fakePrepared struct {
	sb            msb.Sandbox
	target        string
	serveHostPort int
	guestCWD      string
	key           state.Key
	homeVolume    string
}

func (f *fakePrepared) Cleanup() {}

func (f *fakePrepared) Sandbox() msb.Sandbox { return f.sb }

func (f *fakePrepared) Target() string { return f.target }

func (f *fakePrepared) ServeHostPort() int { return f.serveHostPort }

func (f *fakePrepared) GuestCWD() string { return f.guestCWD }

func (f *fakePrepared) Key() state.Key { return f.key }

func (f *fakePrepared) HomeVolume() string { return f.homeVolume }
