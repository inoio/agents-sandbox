package prepare

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

func TestProvisionHostConfig(t *testing.T) {
	truePtr := true
	falsePtr := false
	cases := []struct {
		name string
		opts options.RunOptions
		want bool
	}{
		{name: "nil enables by default", opts: options.RunOptions{Agent: "opencode"}, want: true},
		{name: "explicit true", opts: options.RunOptions{ProvisionHostConfig: &truePtr}, want: true},
		{name: "explicit false", opts: options.RunOptions{ProvisionHostConfig: &falsePtr}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := provisionHostConfig(tc.opts); got != tc.want {
				t.Errorf("provisionHostConfig(%+v) = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}

// TestCurrentAgentSourceLoadError covers the branch where the project state
// cannot be read: an empty source is returned.
func TestCurrentAgentSourceLoadError(t *testing.T) {
	withFailingStateDir(t)
	k := state.Key{Slug: "proj", Agent: "opencode"}
	if got := currentAgentSource(k); got != "" {
		t.Errorf("currentAgentSource() = %q, want empty on load error", got)
	}
}

// TestAgentLatestVersionDefaultNoChecker covers the default agentLatestVersion
// branch for an agent that is not an UpgradeChecker: it returns an empty version
// without error.
func TestAgentLatestVersionDefaultNoChecker(t *testing.T) {
	got, err := agentLatestVersion(context.Background(), &fakeAgent{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("agentLatestVersion for a non-checker = %q, want empty", got)
	}
}

// TestPrepareSandboxUnknownAgent covers the unknown-agent branch of
// PrepareSandbox.
func TestPrepareSandboxUnknownAgent(t *testing.T) {
	tmpRepo := testutil.InitRepo(t)
	t.Chdir(tmpRepo)
	ui := termio.NewTestMock(t)

	if _, err := PrepareSandbox(
		context.Background(),
		resolvedRunOptions(t, options.RunOptions{Agent: "no-such-agent"}),
		false,
		&ui,
	); err == nil {
		t.Fatal("expected error for an unknown agent")
	}
}
