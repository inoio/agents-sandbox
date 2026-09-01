package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/configpaths"
	sandboximage "github.com/inoio/opencode-sandbox/internal/sandbox/image"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/termio"
)

// TestPrepareSandboxImageError covers the image-setup failure branch of
// PrepareSandbox: when the opencode version cannot be resolved, the image
// cannot be built and PrepareSandbox returns an error.
func TestPrepareSandboxImageError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("cannot resolve opencode version")
	})

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the opencode version cannot be resolved")
	}
}

// TestPrepareSandboxVersionResolutionError covers the branch where resolving
// the opencode version up front fails (before the image is touched): the
// interactive upgrade prompt errors, so PrepareSandbox returns the error
// without attempting to build the image.
func TestPrepareSandboxVersionResolutionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, ui); err == nil {
		t.Fatal("expected error when the opencode version prompt fails")
	}
}
