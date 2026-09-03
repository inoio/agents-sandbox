package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/configpaths"
	"github.com/inoio/opencode-sandbox/internal/git"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
	"github.com/inoio/opencode-sandbox/internal/testutil"
)

// nilResultDockerd models a sandbox whose dockerd readiness probe returns a
// nil (nil, nil) result during polling, forcing the log/no-result branch.
type nilResultDockerd struct {
	msb.Sandbox

	readyCalls int
}

func (s *nilResultDockerd) Shell(
	ctx context.Context,
	command string,
	opts ...msbSdk.ExecOption,
) (msb.ShellResult, error) {
	switch command {
	case dockerdBinaryCheckCmd:
		return msb.NewTestResult(true, 0, "", "", nil), nil
	case dockerdReadyCmd:
		s.readyCalls++
		if s.readyCalls >= 2 {
			return nil, errors.New("probe error")
		}
		return msb.NewTestResult(false, 1, "", "", nil), nil
	case dockerdRestartCmd:
		return msb.NewTestResult(true, 0, "", "", nil), nil
	}
	return s.Sandbox.Shell(ctx, command, opts...)
}

// TestStartDockerdNilProbeResult covers the branch where the dockerd readiness
// probe returns a nil result during polling.
func TestStartDockerdNilProbeResult(t *testing.T) {
	origTimeout, origInterval := dockerdReadyTimeout, dockerdPollInterval
	t.Cleanup(func() {
		dockerdReadyTimeout = origTimeout
		dockerdPollInterval = origInterval
	})
	dockerdReadyTimeout = 50 * time.Millisecond
	dockerdPollInterval = time.Millisecond

	ui := termio.NewTestMock(t)
	sb := &nilResultDockerd{Sandbox: msb.NewMockSandbox(msb.SandboxOpts{})}

	err := startDockerdIfPresent(context.Background(), sb, &ui)
	if err == nil {
		t.Fatal("expected error when dockerd never becomes ready")
	}
	if !contains(joinStrings(ui.VerboseCalls), "dockerd readiness") {
		t.Errorf("expected a readiness verbose message, got %v", ui.VerboseCalls)
	}
}

// TestEnsureProjectVMFlockError covers the branch where acquiring the
// per-project flock fails on the create path.
func TestEnsureProjectVMFlockError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ui := termio.NewTestMock(t)

	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	slug := git.ProjectSlug()
	slugDir := filepath.Join(configpaths.Get().UserStateDir(), slug)
	if err := os.MkdirAll(filepath.Join(slugDir, "ensure-vm.lock"), 0o750); err != nil {
		t.Fatal(err)
	}

	client := &msb.MockMsbClient{}
	client.GetSandboxFn = func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "not found"}
	}
	client.EnsureInstalledFn = func(context.Context) error { return nil }
	msb.WithMsbMock(t, client)

	_, _, err := ensureProjectVM(context.Background(), options.RunOptions{},
		"img:tag", "vol", "/workspace", nil, &ui)
	if err == nil {
		t.Fatal("expected error when acquiring the project flock fails")
	}
	if !contains(err.Error(), "acquire project flock") {
		t.Errorf("expected a flock-acquisition error, got %v", err)
	}
}
