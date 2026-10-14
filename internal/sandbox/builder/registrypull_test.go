package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder/cacheregistry"
)

func TestImagePullRefReturnsRegistryRefForMsb(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "msb")
	restore := ensureCacheRegistry
	defer func() { ensureCacheRegistry = restore }()
	ensureCacheRegistry = func(context.Context) (cacheregistry.Endpoint, error) {
		//exhaustruct:ignore // only the registry authority matters here
		return cacheregistry.Endpoint{Host: "host.microsandbox.internal", Port: 5000, Token: "x"}, nil
	}
	ref, ok, err := ImagePullRef(context.Background(), "agents-sandbox/runner-p:opencode-latest")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(ref, "host.microsandbox.internal:5000/") {
		t.Fatalf("ref = %q", ref)
	}
}

func TestImagePullRefIsNoopForMoby(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")
	ref, ok, err := ImagePullRef(context.Background(), "agents-sandbox/runner-p:opencode-latest")
	if err != nil || ok || ref != "" {
		t.Fatalf("ref=%q ok=%v err=%v", ref, ok, err)
	}
}

func TestHoldRegistryIsNoopForMoby(t *testing.T) {
	t.Setenv("AGENTS_SANDBOX_BUILDER", "moby")
	stop, err := HoldRegistry(context.Background())
	if err != nil || stop == nil {
		t.Fatalf("stop==nil:%v err=%v", stop == nil, err)
	}
	stop()
}
