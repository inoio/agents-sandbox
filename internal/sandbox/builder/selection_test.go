package builder

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestSelectionDefaultsToMsb(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_BUILDER", "")

	if _, ok := Get().(*Client); !ok {
		t.Fatalf("Get() = %T; want *Client", Get())
	}
}

func TestSelectionMobyFromEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")

	if _, ok := Get().(*mobyBuilder); !ok {
		t.Fatalf("Get() = %T; want *mobyBuilder", Get())
	}
}

func TestSelectionUnknownFallsBackToMsb(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_BUILDER", "podman")

	if _, ok := Get().(*Client); !ok {
		t.Fatalf("Get() = %T; want *Client", Get())
	}
}
