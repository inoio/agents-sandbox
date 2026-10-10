package builder

import (
	"context"
	"testing"
)

// MockBuilder is a test double for Builder. BuildFn customizes the result;
// nil returns an empty BuildResult. Every request is recorded in Calls.
type MockBuilder struct {
	BuildFn func(ctx context.Context, req BuildRequest) (BuildResult, error)
	Calls   []BuildRequest
}

// Compile-time check.
var _ Builder = (*MockBuilder)(nil)

// Build implements Builder, recording the request and delegating to BuildFn.
func (m *MockBuilder) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	m.Calls = append(m.Calls, req)
	if m.BuildFn != nil {
		return m.BuildFn(ctx, req)
	}
	return BuildResult{}, nil
}

// WithMockBuilder swaps the global Get factory with mock, restoring it when the
// test ends.
func WithMockBuilder(t *testing.T, mock Builder) {
	t.Helper()
	orig := Get
	Get = func() Builder { return mock }
	t.Cleanup(func() { Get = orig })
}
