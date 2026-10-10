package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
)

// fakeBuilder installs a builder.MockBuilder for a test and returns it so the
// test can inspect the recorded BuildRequest calls. resultErr, when non-nil, is
// returned for every Build call.
func fakeBuilder(t *testing.T, result builder.BuildResult, resultErr error) *builder.MockBuilder {
	t.Helper()
	mock := &builder.MockBuilder{
		BuildFn: func(context.Context, builder.BuildRequest) (builder.BuildResult, error) {
			return result, resultErr
		},
	}
	builder.WithMockBuilder(t, mock)
	return mock
}

// msbImageConfig builds a flat microsandbox ImageConfig for mocked inspects.
func msbImageConfig(digest string, labels map[string]string, env []string) *msbSdk.ImageConfig {
	return &msbSdk.ImageConfig{Digest: digest, Labels: labels, Env: env}
}

// newArchiveFile writes a small archive file for a test and returns its path.
func newArchiveFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, []byte("tar-data"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}
