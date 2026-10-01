package image

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/tlsca"
)

func agentOpencode(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	return a
}

// resolveTestCACert returns the TLS interception CA certificate PEM persisted
// by the active (test-isolated) config paths, matching what caCertForBuild
// resolves inside EnsureImage.
func resolveTestCACert(t *testing.T) []byte {
	t.Helper()
	ca, err := tlsca.Ensure()
	if err != nil {
		t.Fatalf("ensure TLS CA: %v", err)
	}
	cert, err := ca.Cert()
	if err != nil {
		t.Fatalf("read TLS CA cert: %v", err)
	}
	return cert
}
