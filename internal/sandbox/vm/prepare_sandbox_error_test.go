package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	sandboximage "github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrepareSandboxImageError covers the image-setup failure branch of
// PrepareSandbox: when the opencode version cannot be resolved, the image
// cannot be built and PrepareSandbox returns an error.
func TestPrepareSandboxImageError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	sandboximage.WithMockAgentVersionResolver(t, func(_ context.Context, _ agent.Agent, _ string) (string, error) {
		return "", errors.New("cannot resolve opencode version")
	})

	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the opencode version cannot be resolved")
	}
}

// TestPrepareSandboxVersionResolutionError covers the branch where resolving
// the opencode version up front fails (before the image is touched): the
// interactive upgrade prompt errors, so PrepareSandbox returns the error
// without attempting to build the image.
func TestPrepareSandboxVersionResolutionError(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := saveUpgradeState(upgradeState{CurrentVersion: "1.0.0"}); err != nil {
		t.Fatalf("saveUpgradeState: %v", err)
	}
	origUpgradeInfo := agentLatestVersion
	agentLatestVersion = func(_ context.Context, _ agent.Agent) (string, error) { return "2.0.0", nil }
	t.Cleanup(func() { agentLatestVersion = origUpgradeInfo })

	ui := &termio.Mock{
		IsInteractiveResult: true,
		SelectFn: func(_ string, _ []termio.Choice, _ string) (string, error) {
			return "", errors.New("selection failed")
		},
	}

	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, ui); err == nil {
		t.Fatal("expected error when the opencode version prompt fails")
	}
}

// TestPrepareSandboxErrorWhenInterceptionCAUnavailable covers the up-front
// failure branch of PrepareSandbox: when the TLS interception CA cannot be
// resolved, the run aborts before any image or VM work.
func TestPrepareSandboxErrorWhenInterceptionCAUnavailable(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	blocked := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	ui := termio.NewTestMock(t)
	if _, err := PrepareSandbox(context.Background(), options.RunOptions{}, &ui); err == nil {
		t.Fatal("expected error when the TLS interception CA cannot be resolved")
	}
}

// TestResolveInterceptionCAErrorWhenFingerprintFails covers the branch where
// the persisted CA certificate cannot be read back for fingerprinting.
func TestResolveInterceptionCAErrorWhenFingerprintFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	opts := options.RunOptions{}
	if err := resolveInterceptionCA(&opts); err != nil {
		t.Fatalf("resolveInterceptionCA: %v", err)
	}
	if opts.Network.TLS == nil || opts.Network.TLS.CACert == "" || opts.Network.TLS.CAFingerprint == "" {
		t.Fatalf("resolveInterceptionCA must wire the CA into the network policy, got %+v", opts.Network)
	}
	certPath := filepath.Join(configpaths.Get().UserStateDir(), "tls", "ca.crt")
	if err := os.Remove(certPath); err != nil {
		t.Fatalf("remove CA cert: %v", err)
	}
	// A directory at the cert path still satisfies the existence check in
	// Load but makes the certificate read (for fingerprinting) fail.
	if err := os.Mkdir(certPath, 0o700); err != nil {
		t.Fatalf("replace CA cert with dir: %v", err)
	}
	if err := resolveInterceptionCA(&opts); err == nil {
		t.Fatal("expected error when the CA certificate cannot be fingerprinted")
	}
}

// TestResolveInterceptionCAWiresCAIntoPolicy verifies the happy path wires the
// resolved CA paths and fingerprint into an existing TLS network policy.
func TestResolveInterceptionCAWiresCAIntoPolicy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	opts := options.RunOptions{Network: network.Policy{TLS: &network.TLSConfig{Bypass: []string{"*.internal.com"}}}}
	if err := resolveInterceptionCA(&opts); err != nil {
		t.Fatalf("resolveInterceptionCA: %v", err)
	}
	if opts.Network.TLS.CACert == "" || opts.Network.TLS.CAKey == "" || opts.Network.TLS.CAFingerprint == "" {
		t.Fatalf("resolveInterceptionCA must fill CA paths and fingerprint, got %+v", opts.Network.TLS)
	}
	if len(opts.Network.TLS.Bypass) != 1 || opts.Network.TLS.Bypass[0] != "*.internal.com" {
		t.Fatalf("resolveInterceptionCA must preserve existing TLS settings, got %+v", opts.Network.TLS)
	}
}
