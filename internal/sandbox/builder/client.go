package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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

// ResolveImageConfig resolves base-image metadata through a BuildKit gateway
// session, the BuildKit-native replacement for a pre-build `docker inspect`.
func (c *realBuildkitClient) ResolveImageConfig(
	ctx context.Context,
	ref string,
	opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
	var (
		resolvedRef  string
		resolvedDgst digest.Digest
		resolvedCfg  []byte
	)
	_, err := c.client.Build(
		ctx,
		client.SolveOpt{Session: registryAuthSession()},
		"",
		func(ctx context.Context, gw gwclient.Client) (*gwclient.Result, error) {
			var err error
			resolvedRef, resolvedDgst, resolvedCfg, err = gw.ResolveImageConfig(ctx, ref, opt)
			if err != nil {
				return nil, err
			}
			return gwclient.NewResult(), nil
		},
		nil,
	)
	if err != nil {
		return "", "", nil, err
	}
	return resolvedRef, resolvedDgst, resolvedCfg, nil
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

func (c *Client) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	hostPort, err := c.vm.Ensure(ctx)
	if err != nil {
		return BuildResult{}, fmt.Errorf("ensure builder VM: %w", err)
	}

	bc, err := newBuildkitClient(ctx, fmt.Sprintf("tcp://127.0.0.1:%d", hostPort))
	if err != nil {
		return BuildResult{}, fmt.Errorf("connect to builder at %d: %w", hostPort, err)
	}
	defer func() { _ = bc.Close() }()

	baseDigest, err := resolveBaseDigest(ctx, bc, req.BaseRef, req.Platform)
	if err != nil {
		return BuildResult{}, fmt.Errorf("resolve base image %s: %w", req.BaseRef, err)
	}

	archive, err := os.CreateTemp("", "agents-sandbox-image-*.tar")
	if err != nil {
		return BuildResult{}, fmt.Errorf("create image archive: %w", err)
	}
	archivePath := archive.Name()
	buildSucceeded := false
	defer func() {
		if !buildSucceeded {
			_ = os.Remove(archivePath)
		}
	}()

	attributes := map[string]string{"platform": req.Platform}
	for key, value := range req.BuildArgs {
		attributes["build-arg:"+key] = value
	}
	attributes["build-arg:BASE_IMAGE"] = baseDigest
	if req.NoCache {
		attributes["no-cache"] = ""
	}

	statusCh := make(chan *client.SolveStatus)
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		for status := range statusCh {
			forwardProgress(status, req.Progress)
		}
	}()

	resp, solveErr := bc.Solve(ctx, nil, client.SolveOpt{
		Frontend:      "dockerfile.v0",
		LocalDirs:     map[string]string{"context": req.ContextDir, "dockerfile": req.ContextDir},
		FrontendAttrs: attributes,
		Exports: []client.ExportEntry{{
			Type: "oci",
			Output: func(map[string]string) (io.WriteCloser, error) {
				return nopWriteCloser{Writer: archive}, nil
			},
		}},
		Session: registryAuthSession(),
	}, statusCh)
	// Solve owns closing statusCh, which ends the progress loop.
	<-progressDone

	closeErr := archive.Close()
	if solveErr != nil {
		return BuildResult{}, fmt.Errorf("solve image build: %w", solveErr)
	}
	if closeErr != nil {
		return BuildResult{}, fmt.Errorf("close image archive: %w", closeErr)
	}

	var metadata struct {
		Config struct {
			Env []string `json:"Env"`
		} `json:"config"`
	}
	if configJSON := resp.ExporterResponse["containerimage.config"]; configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &metadata); err != nil {
			return BuildResult{}, fmt.Errorf("parse image config: %w", err)
		}
	}

	buildSucceeded = true
	return BuildResult{
		ArchivePath: archivePath,
		Digest:      resp.ExporterResponse["containerimage.digest"],
		Env:         metadata.Config.Env,
		BaseDigest:  baseDigest,
	}, nil
}
