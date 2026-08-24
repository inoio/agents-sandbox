package vm

import (
	"context"
	"errors"
	"testing"

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

	sandboximage.WithMockOpenCodeVersionResolver(t, func(_ context.Context, _ string) (string, error) {
		return "", errors.New("cannot resolve opencode version")
	})

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the opencode version cannot be resolved")
	}
}
