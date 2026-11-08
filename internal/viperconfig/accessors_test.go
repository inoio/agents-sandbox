package viperconfig

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestResolverSlug pins the config-write target: a built resolver reports the
// slug it was built for, and a nil resolver reports an empty slug so the caller
// falls back to its own.
func TestResolverSlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	r, err := NewResolver(nil, configpaths.Get(), "widget-bbb")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := r.Slug(); got != "widget-bbb" {
		t.Errorf("Slug = %q, want widget-bbb", got)
	}
	var nilResolver *Resolver
	if got := nilResolver.Slug(); got != "" {
		t.Errorf("nil resolver Slug = %q, want empty", got)
	}
}
