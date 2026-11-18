//go:build msb

package channel

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sandboxmsb "github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest/runtime"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// The observation channel against a real VM. The test creates a sandbox whose
// network policy is the channel's overlay (interception forced on, the proxy
// as its egress, the channel CA trusted upstream), configures a secret whose
// placeholder the guest sends, drives one HTTPS request, and asserts the
// scrubbed artefact names the destination and the credential-bearing header
// NAME — never a value.
//
// It is host-only (KVM, a docker daemon, and network) and excluded from the
// default suite by the msb build tag, like the other real-runtime tests. It is
// the spec's open-validation `end-to-end agent turn through the proxy` in its
// smallest form: a real request, not a synthetic one. Run it with:
//
//	CGO_ENABLED=1 go test ./internal/sandbox/channel/ -tags msb -run TestChannel
//
// The T2 chain it exercises: the runtime intercepts the guest's TLS (a secret
// is configured), substitutes the placeholder in flight, and re-originates to
// our proxy, which the runtime trusts because the overlay projects our CA into
// TLSConfig.UpstreamCACerts. Our proxy terminates the TLS, records the
// destination and the header name, and dials the real upstream.
const (
	// channelDestinationHost is the egress-allowlisted host the guest requests.
	channelDestinationHost = "example.com"
	// channelSecretEnv is the secret's env-var name; the guest sends the
	// placeholder through it and the runtime substitutes the real value.
	channelSecretEnv = "LEARN_CHANNEL_TOKEN"
	// channelRealToken is the value the runtime substitutes. It must never
	// reach the artefact.
	channelRealToken = "channel-real-token-never-recorded"
	// channelPlaceholder is what the guest sends and the runtime replaces. It
	// must never reach the artefact either.
	channelPlaceholder = "channel-placeholder-never-recorded"
	// channelCredentialHeader is the request header both the secret's
	// substitution and the recorder key on; only its name may be recorded.
	channelCredentialHeader = "authorization"
	// channelSandboxPrefix names the throwaway sandbox the test creates.
	channelSandboxPrefix = "channel-msb-test-"
)

// channelGuestCommand issues one HTTPS request with the placeholder credential.
// It ends in `; true` so the shell exit status does not depend on wget reaching
// the upstream: the artefact's attribution is recorded before the proxy dials,
// so it is present even when the destination is unreachable.
const channelGuestCommand = `wget -q -O /dev/null -T 20 --header "Authorization: $` +
	channelSecretEnv + `" https://` + channelDestinationHost + `/ ; true`

// realMsbGet is the production microsandbox client factory, captured at package
// init before TestMain installs the fail-fast mock. The msb-tagged test drives
// the real SDK through it: realMsbClient is unexported in package msb, so a test
// outside that package cannot name it. Capturing here keeps the fail-fast net
// for every other test in this package.
var realMsbGet = sandboxmsb.Get

func TestChannelRealSessionThroughProxy(t *testing.T) {
	// TestMain installs the isolated microsandbox home for the whole package, so
	// the developer's real msb state is untouched; it also sweeps any sandbox a
	// failed test left behind. EnsureIsolatedRuntime resolves the runtime into
	// that home.
	runtime.EnsureIsolatedRuntime(t)

	client := installRealMsbClient(t)

	base := network.Policy{
		Profiles:    []network.Profile{network.ProfileNone},
		EgressAllow: []string{channelDestinationHost},
	}
	observer, err := StartObservation("127.0.0.1", base)
	if err != nil {
		t.Fatalf("StartObservation: %v", err)
	}
	t.Cleanup(func() { observer.Stop() })

	overlay := observer.Overlay()
	spec, err := overlay.Spec()
	if err != nil {
		t.Fatalf("overlay Spec: %v", err)
	}
	if spec.Proxy.Address == "" || len(spec.UpstreamCACerts) == 0 {
		t.Fatalf("overlay spec is not an observation overlay: %+v", spec)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sandbox := createChannelSandbox(t, ctx, client, spec)

	// The guest sends the placeholder; the runtime substitutes the real value
	// before re-originating to our proxy.
	output, err := sandbox.Shell(ctx, channelGuestCommand, sandboxmsb.ExecRequest{
		Env: map[string]string{channelSecretEnv: channelPlaceholder},
	})
	if err != nil {
		t.Fatalf("guest request: %v", err)
	}
	t.Logf("guest request (exit %d): %s", output.ExitCode(), strings.TrimSpace(output.Stdout()))

	observation := observer.Stop()

	assertProxyDestination(t, observation, channelDestinationHost)
	assertCredentialHeaderName(t, observation, channelDestinationHost, channelCredentialHeader)
	assertNoValueRecorded(t, observation)
}

// installRealMsbClient swaps the fail-fast client for the production one for the
// duration of the test, so this test can create a real VM while every other test
// in the package keeps the fail-fast mock.
func installRealMsbClient(t *testing.T) sandboxmsb.Client {
	t.Helper()

	previous := sandboxmsb.Get
	sandboxmsb.Get = realMsbGet
	t.Cleanup(func() { sandboxmsb.Get = previous })
	return sandboxmsb.Get()
}

// createChannelSandbox creates the throwaway sandbox under the observation
// overlay with a configured secret, and registers its teardown. The overlay is
// applied at creation: it is a creation-only network policy.
func createChannelSandbox(
	t *testing.T,
	ctx context.Context,
	client sandboxmsb.Client,
	spec sandboxmsb.NetworkSpec,
) sandboxmsb.Sandbox {
	t.Helper()

	headers := true
	secret := sandboxmsb.Secret.Env(channelSecretEnv, channelRealToken, sandboxmsb.SecretEnvOptions{
		Allow:       []string{channelDestinationHost},
		Placeholder: channelPlaceholder,
		Substitution: sandboxmsb.SecretSubstitution{
			Headers:      &headers,
			HeaderFields: []string{channelCredentialHeader},
		},
	})

	name := channelSandboxPrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	sandbox, err := client.CreateSandbox(ctx, name, sandboxmsb.SandboxSpec{
		Image:       "alpine:3.20",
		Network:     spec,
		Secrets:     []sandboxmsb.SecretEntry{secret},
		IdleTimeout: runtime.TestIdleTimeout,
		Detached:    true,
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() { destroyChannelSandbox(t, sandbox, name) })
	return sandbox
}

// destroyChannelSandbox tears the test's VM down, tolerating the destroyed state
// a failed test may already have reached. Cleanup logs rather than fails, so it
// cannot mask the test's own result.
func destroyChannelSandbox(t *testing.T, sandbox sandboxmsb.Sandbox, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runtime.DestroyTimeout)
	defer cancel()
	if err := sandbox.Destroy(ctx, sandboxmsb.LifecycleOptions{}); err != nil && !sandboxmsb.IsNotFound(err) {
		t.Logf("cleanup: destroy VM %s: %v", name, err)
	}
}

// assertProxyDestination asserts the artefact names the requested host as a
// destination our proxy observed, whether it terminated the request or only the
// CONNECT tunnel.
func assertProxyDestination(t *testing.T, observation Observation, host string) {
	t.Helper()

	for _, destination := range observation.Destinations {
		if destination.Host == host && destination.Channel == observedViaProxy {
			return
		}
	}
	t.Errorf("observation has no %q destination from the proxy: %+v", host, observation.Destinations)
}

// assertCredentialHeaderName asserts an attribution for the destination carries
// the credential-bearing header's name.
func assertCredentialHeaderName(t *testing.T, observation Observation, host, headerName string) {
	t.Helper()

	for _, attribution := range observation.Attribution {
		if attribution.Host != host {
			continue
		}
		if slices.Contains(attribution.HeaderNames, headerName) {
			return
		}
	}
	t.Errorf("no attribution for %q carries header %q: %+v", host, headerName, observation.Attribution)
}

// assertNoValueRecorded asserts the artefact carries neither the substituted
// value nor the placeholder nor the secret's env-var name: the recorded
// attribution holds header names only, with no credential identity in S4.
func assertNoValueRecorded(t *testing.T, observation Observation) {
	t.Helper()

	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	for _, secret := range []string{channelRealToken, channelPlaceholder, channelSecretEnv} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the artefact records %q; it must hold header names only: %s", secret, encoded)
		}
	}
}
