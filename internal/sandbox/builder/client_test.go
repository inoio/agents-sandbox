package builder

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/opencontainers/go-digest"
)

type closeCountingClient struct {
	closeCalls *atomic.Int32
}

func (c *closeCountingClient) ListWorkers(context.Context) error { return nil }

func (c *closeCountingClient) Solve(
	context.Context, *llb.Definition, client.SolveOpt, chan *client.SolveStatus,
) (*client.SolveResponse, error) {
	return &client.SolveResponse{}, nil
}

func (c *closeCountingClient) ResolveImageConfig(
	context.Context, string, sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("unsupported")
}

func (c *closeCountingClient) Close() error {
	c.closeCalls.Add(1)
	return nil
}

func TestDefaultReadyRetriesThenSucceeds(t *testing.T) {
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })

	var dials atomic.Int32
	var closeCalls atomic.Int32
	newBuildkitClient = func(_ context.Context, _ string) (buildkitClient, error) {
		if dials.Add(1) <= 2 {
			return nil, errors.New("connection refused")
		}
		return &closeCountingClient{closeCalls: &closeCalls}, nil
	}

	vm := &VM{}
	if err := vm.defaultReady(context.Background(), 1234); err != nil {
		t.Fatalf("defaultReady returned error: %v", err)
	}
	if got := dials.Load(); got != 3 {
		t.Errorf("dial attempts = %d, want 3", got)
	}
	if got := closeCalls.Load(); got != 1 {
		t.Errorf("close calls = %d, want 1", got)
	}
}

func TestDefaultReadyTimesOut(t *testing.T) {
	restore := newBuildkitClient
	t.Cleanup(func() { newBuildkitClient = restore })

	newBuildkitClient = func(_ context.Context, _ string) (buildkitClient, error) {
		return nil, errors.New("connection refused")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	vm := &VM{}
	err := vm.defaultReady(ctx, 1234)
	if err == nil {
		t.Fatal("defaultReady returned nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1234") {
		t.Errorf("error %q does not mention the address", err)
	}
}

func TestRegistryAuthSessionNonNil(t *testing.T) {
	attachable := registryAuthSession()
	if len(attachable) != 1 {
		t.Fatalf("registryAuthSession returned %d attachables, want 1", len(attachable))
	}
	if attachable[0] == nil {
		t.Fatal("registryAuthSession returned a nil attachable")
	}
}

func TestNewClientWiresReadySeam(t *testing.T) {
	c := newClient()
	if c == nil {
		t.Fatal("newClient returned nil")
	}
	mb, ok := c.backend.(*msbBackend)
	if !ok {
		t.Fatalf("newClient backend = %T, want *msbBackend", c.backend)
	}
	if mb.vm == nil {
		t.Fatal("newClient did not construct a VM")
	}
	if mb.vm.Ready == nil {
		t.Fatal("newClient left VM.Ready nil; readiness would be silently skipped")
	}
	if mb.vm.Msb == nil {
		t.Fatal("newClient did not wire an msb.Client")
	}
}

func TestGetReturnsBuilder(t *testing.T) {
	if Get() == nil {
		t.Fatal("Get returned nil Builder")
	}
}
