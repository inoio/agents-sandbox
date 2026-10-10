package builder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/opencontainers/go-digest"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

const (
	builderReadyTimeout = 30 * time.Second
	builderDialBackoff  = 250 * time.Millisecond
)

// buildkitClient is the host-side seam over the BuildKit client, so the build
// pipeline can be tested without a live builder VM.
type buildkitClient interface {
	ListWorkers(ctx context.Context) error
	Solve(
		ctx context.Context,
		def *llb.Definition,
		opt client.SolveOpt,
		status chan *client.SolveStatus,
	) (*client.SolveResponse, error)
	ResolveImageConfig(
		ctx context.Context,
		ref string,
		opt sourceresolver.Opt,
	) (string, digest.Digest, []byte, error)
	Close() error
}

// newBuildkitClient dials a builder and proves readiness with a ListWorkers
// round trip. It is a variable so tests can substitute a fake.
var newBuildkitClient = realNewBuildkitClient

func realNewBuildkitClient(ctx context.Context, addr string) (buildkitClient, error) {
	c, err := client.New(ctx, addr)
	if err != nil {
		return nil, err
	}
	if _, err := c.ListWorkers(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &realBuildkitClient{client: c}, nil
}

type realBuildkitClient struct {
	client *client.Client
}

func (c *realBuildkitClient) ListWorkers(ctx context.Context) error {
	_, err := c.client.ListWorkers(ctx)
	return err
}

func (c *realBuildkitClient) Solve(
	ctx context.Context,
	def *llb.Definition,
	opt client.SolveOpt,
	status chan *client.SolveStatus,
) (*client.SolveResponse, error) {
	return c.client.Solve(ctx, def, opt, status)
}

// ResolveImageConfig is unsupported on the bare client; the gateway resolver
// created inside Build performs base-image resolution.
func (c *realBuildkitClient) ResolveImageConfig(
	context.Context, string, sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	return "", "", nil, errors.New("ResolveImageConfig is not supported on the bare buildkit client")
}

func (c *realBuildkitClient) Close() error {
	return c.client.Close()
}

// defaultReady retries dialing the builder until it answers or the readiness
// budget elapses, closing each probed connection.
func (v *VM) defaultReady(ctx context.Context, hostPort int) error {
	addr := fmt.Sprintf("tcp://127.0.0.1:%d", hostPort)
	deadline := time.Now().Add(builderReadyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probe, err := newBuildkitClient(ctx, addr)
		if err == nil {
			_ = probe.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for buildkitd at %s: %w", addr, ctx.Err())
		case <-time.After(builderDialBackoff):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no connection attempt succeeded")
	}
	return fmt.Errorf("wait for buildkitd at %s within %s: %w", addr, builderReadyTimeout, lastErr)
}

type BuildRequest struct {
	ContextDir string
	Tag        string
	BaseRef    string
	BuildArgs  map[string]string
	NoCache    bool
	Platform   string
	Progress   func(line string)
}

type BuildResult struct {
	ArchivePath string
	Digest      string
	Env         []string
	BaseDigest  string
}

type Builder interface {
	Build(ctx context.Context, req BuildRequest) (BuildResult, error)
}

var Get = func() Builder {
	return newClient()
}

type Client struct {
	Msb msb.Client
	vm  *VM
}

func newClient() *Client {
	vm := &VM{Msb: msb.Get(), Ready: nil}
	vm.Ready = vm.defaultReady
	return &Client{Msb: vm.Msb, vm: vm}
}

func (c *Client) Build(context.Context, BuildRequest) (BuildResult, error) {
	return BuildResult{}, errors.New("buildkit builder: Build not implemented")
}
