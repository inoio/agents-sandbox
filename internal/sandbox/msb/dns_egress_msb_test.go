//go:build msb

package msb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// TestNetworkPolicyDNSResolution boots a VM per launcher network policy and
// checks which names its DNS proxy resolves (#233). Under `none`, only names an
// egress-allow rule matches may resolve; any other name must fail, or the guest
// could exfiltrate data in query names. The composable profiles keep resolving
// arbitrary names.
//
// Every name a case expects refused exists upstream, which the `public` case
// proves in the same run. A refusal therefore has to come from microsandbox's
// proxy: a leaked query would be answered, not rejected.
func TestNetworkPolicyDNSResolution(t *testing.T) {
	ensureIsolatedRuntime(t)
	none := []network.Profile{network.ProfileNone}
	cases := []struct {
		name        string
		policy      network.Policy
		resolves    []string
		notResolves []string
	}{
		{
			name:        "none-without-allow-list",
			policy:      network.Policy{Profiles: none},
			notResolves: []string{"example.com", "example.org"},
		},
		{
			name:        "none-with-ip-allow-list",
			policy:      network.Policy{Profiles: none, EgressAllow: []string{"1.1.1.1"}},
			notResolves: []string{"example.com", "example.org"},
		},
		{
			name:        "none-with-hostname-allow-list",
			policy:      network.Policy{Profiles: none, EgressAllow: []string{"example.com"}},
			resolves:    []string{"example.com"},
			notResolves: []string{"www.example.com", "example.org"},
		},
		{
			name:        "none-with-suffix-allow-list",
			policy:      network.Policy{Profiles: none, EgressAllow: []string{".example.com"}},
			resolves:    []string{"example.com", "www.example.com"},
			notResolves: []string{"example.org"},
		},
		{
			name:     "none-with-host-allow-list",
			policy:   network.Policy{Profiles: none, EgressAllow: []string{"host"}},
			resolves: []string{"example.com", "example.org"},
		},
		{
			name:     "public",
			policy:   network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
			resolves: []string{"example.com", "www.example.com", "example.org"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sandbox := createSandboxWithPolicy(t, "agents-sandbox-dns-egress-"+tc.name, tc.policy)
			for _, name := range tc.resolves {
				if result := lookup(t, sandbox, name); !result.Success() {
					t.Errorf("%s: lookup of %q failed, want it to resolve: %s", tc.name, name, result.Stdout())
				}
			}
			for _, name := range tc.notResolves {
				if result := lookup(t, sandbox, name); !isRefused(result) {
					t.Errorf("%s: lookup of %q was not refused with NXDOMAIN: %s", tc.name, name, result.Stdout())
				}
			}
		})
	}
}

func createSandboxWithPolicy(t *testing.T, sandboxName string, policy network.Policy) *msbSdk.Sandbox {
	t.Helper()
	networkConfig, err := policy.Config()
	if err != nil {
		t.Fatalf("Policy.Config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sandbox, err := msbSdk.CreateSandbox(
		ctx,
		sandboxName,
		msbSdk.WithImage("alpine:3.20"),
		msbSdk.WithReplace(),
		msbSdk.WithDetached(),
		msbSdk.WithNetwork(networkConfig),
	)
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_ = sandbox.Kill(cleanupCtx)
		if handle, handleErr := msbSdk.GetSandbox(cleanupCtx, sandboxName); handleErr == nil {
			_ = handle.Remove(cleanupCtx)
		}
	})
	return sandbox
}

// lookup runs nslookup for name in the guest.
func lookup(t *testing.T, sandbox *msbSdk.Sandbox, name string) *msbSdk.ExecOutput {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := sandbox.Shell(ctx, fmt.Sprintf("nslookup -timeout=2 -retry=1 %s 2>&1", name))
	if err != nil {
		t.Fatalf("nslookup %s: %v", name, err)
	}
	t.Logf("nslookup %s (exit %d): %s", name, output.ExitCode(), strings.TrimSpace(output.Stdout()))
	return output
}

// isRefused reports whether the resolver answered NXDOMAIN, as microsandbox's
// proxy does for a name the policy does not allow. A timeout or other failure
// does not count: it would hide a query that left the VM.
func isRefused(output *msbSdk.ExecOutput) bool {
	return !output.Success() && strings.Contains(output.Stdout(), "NXDOMAIN")
}
