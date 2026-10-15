package image

import (
	"context"
	"os"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestUnitSuiteDeliversOffline guards the hermetic unit seam: TestMain installs
// the offline delivery, so resolving a pull ref must not reach the real spawner
// (which under `go test` re-execs this binary and re-runs the suite). A
// regression here is what turns the unit suite into a fork bomb.
func TestUnitSuiteDeliversOffline(t *testing.T) {
	if os.Getenv(integrationEnv) != "" {
		t.Skip("integration tier drives the real registry seam")
	}
	ref, ok, err := resolveImageRef(context.Background(), "agents-sandbox/runner-p:opencode-latest")
	if err != nil || ok || ref != "" {
		t.Fatalf("resolveImageRef = (%q, %v, %v), want empty/offline", ref, ok, err)
	}
}

func TestEnsureAvailableLoadsArchive(t *testing.T) {
	m := &msb.MockMsbClient{}
	stop, err := EnsureAvailable(
		context.Background(),
		m,
		ImageInfo{Tag: "t", ArchivePath: "/tmp/a.tar"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(m.LoadedImages) != 1 {
		t.Fatalf("loaded = %v", m.LoadedImages)
	}
}

func TestEnsureAvailableHoldsRegistryForPull(t *testing.T) {
	restore := holdRegistry
	defer func() { holdRegistry = restore }()
	held, stopped := false, false
	holdRegistry = func(context.Context) (func(), error) {
		held = true
		return func() { stopped = true }, nil
	}
	m := &msb.MockMsbClient{}
	stop, err := EnsureAvailable(
		context.Background(),
		m,
		ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare"},
		&termio.Mock{},
	)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if !held || !stopped || len(m.LoadedImages) != 0 {
		t.Fatalf("held=%v stopped=%v loads=%v", held, stopped, m.LoadedImages)
	}
}

func TestEnsureAvailableErrorsWhenNeitherArchiveNorRegistry(t *testing.T) {
	m := &msb.MockMsbClient{
		ImageGetFn: func(context.Context, string) error { return context.DeadlineExceeded },
	}
	stop, err := EnsureAvailable(context.Background(), m, ImageInfo{Tag: "bare"}, &termio.Mock{})
	if err == nil {
		stop()
		t.Fatal("expected an error when the image is neither cached nor deliverable")
	}
}

func TestImageRefPrefersRegistryRef(t *testing.T) {
	if got := (ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare"}).ImageRef(); got != "127.0.0.1:5000/bare" {
		t.Fatalf("got %q", got)
	}
	if got := (ImageInfo{Tag: "bare"}).ImageRef(); got != "bare" {
		t.Fatalf("got %q", got)
	}
}

func TestSandboxImageOptionsRefresh(t *testing.T) {
	opts := SandboxImageOptions(
		ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare", RegistryInsecure: true, Refresh: true},
	)
	cfg := &msbSdk.SandboxConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.Image != "127.0.0.1:5000/bare" || !cfg.RegistryInsecure || cfg.PullPolicy != msbSdk.PullPolicyAlways {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestSandboxImageOptionsCacheHitUsesDefaultPullPolicy(t *testing.T) {
	opts := SandboxImageOptions(ImageInfo{Tag: "bare", Ref: "127.0.0.1:5000/bare", RegistryInsecure: true})
	cfg := &msbSdk.SandboxConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.Image != "127.0.0.1:5000/bare" || !cfg.RegistryInsecure || cfg.PullPolicy != msbSdk.PullPolicyDefault {
		t.Fatalf("cfg=%+v", cfg)
	}
}
