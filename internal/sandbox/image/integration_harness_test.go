//go:build integration

package image

import (
	"context"
	"os"

	"github.com/inoio/agents-sandbox/internal/sandbox/builder"
)

// backendDocker names the Docker-backed builder harness. The integration tests
// select it through AGENTS_SANDBOX_BUILDER.
const backendDocker = "docker"

// activeBackend reports the builder backend under test, defaulting to the
// production microsandbox (msb) backend.
func activeBackend() string {
	if backend := os.Getenv("AGENTS_SANDBOX_BUILDER"); backend != "" {
		return backend
	}
	return "msb"
}

func init() { integrationHarnessSetup = setupIntegrationHarness }

// setupIntegrationHarness cleans up the Docker harness before and after the
// suite when tests run against the Docker backend; otherwise it is a no-op.
func setupIntegrationHarness() func() {
	if os.Getenv(integrationEnvVar) == "" || activeBackend() != backendDocker {
		return func() {}
	}
	ctx := context.Background()
	_ = builder.ResetDockerHarness(ctx)
	return func() { _ = builder.ResetDockerHarness(ctx) }
}
