package image

import (
	"context"
	"testing"

	"github.com/inoio/opencode-sandbox/internal/opencode"
)

// LatestOpenCodeVersion returns the newest opencode release string as resolved
// by the opencode package (GitHub releases/latest).
func LatestOpenCodeVersion(ctx context.Context) (string, error) {
	return opencode.LatestVersion(ctx)
}

// resolveOpenCodeVersion returns the requested version when non-empty,
// otherwise it resolves the latest opencode release. Tests stub this var.
var resolveOpenCodeVersion = func(ctx context.Context, requested string) (string, error) { //nolint:gochecknoglobals // test seam
	if requested != "" {
		return requested, nil
	}
	return LatestOpenCodeVersion(ctx)
}

// WithMockOpenCodeVersion redirects the image package's opencode version
// resolver to return v for any request, restoring the real resolver (which may
// hit the GitHub releases endpoint) when the test ends.
func WithMockOpenCodeVersion(t *testing.T, v string) {
	t.Helper()
	orig := resolveOpenCodeVersion
	resolveOpenCodeVersion = func(_ context.Context, _ string) (string, error) { return v, nil }
	t.Cleanup(func() { resolveOpenCodeVersion = orig })
}
