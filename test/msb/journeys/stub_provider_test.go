package journeys

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// TestStubOriginFromValue pins the advertised-route override: a valid
// http(s) origin is kept (port and any path dropped), and anything else falls
// back to the documented placeholder, so an unset or malformed override can
// never expose the stub on a surprising scheme or host.
func TestStubOriginFromValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "unset", in: "", want: stubOriginDefault},
		{name: "blank", in: "   ", want: stubOriginDefault},
		{name: "loopback http", in: "http://127.0.0.1", want: "http://127.0.0.1"},
		{name: "loopback https", in: "https://127.0.0.1", want: "https://127.0.0.1"},
		{name: "host http", in: "http://stub.internal", want: "http://stub.internal"},
		{name: "port dropped", in: "http://127.0.0.1:8080", want: "http://127.0.0.1"},
		{name: "userinfo and path dropped", in: "https://user:pass@host/path", want: "https://host"},
		{name: "unsupported scheme", in: "ftp://host", want: stubOriginDefault},
		{name: "no host", in: "https://", want: stubOriginDefault},
		{name: "not a url", in: "not a url", want: stubOriginDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stubOriginFromValue(tc.in); got != tc.want {
				t.Errorf("stubOriginFromValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStubProviderDocumentCarriesAnInlineCredential pins the credential a
// learn-mode journey observes: the provider document's options carry a literal
// apiKey, which the read-back classifies as a static credential scoped by the
// derived env-var name <PROVIDERID>_API_KEY (stubCredentialEnvVar) and the
// entry's baseURL host. Without it the UC1 secret assertion has nothing to find.
func TestStubProviderDocumentCarriesAnInlineCredential(t *testing.T) {
	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(provider.Stop)

	content := provider.ProviderConfig()[stubAgentConfigTarget]
	var document struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("decode provider config %q: %v", content, err)
	}
	if document.Provider[stubProviderID].Options.APIKey == "" {
		t.Fatalf("provider %q carries no inline apiKey: %q", stubProviderID, content)
	}
}

// TestStubProviderDocumentClassifiesAsTheAssertedCredential pins the journey's
// premise without a VM: the read-back classifies the stub provider document's
// inline apiKey as a static credential named exactly what UC1 asserts
// (uc1Scope), scoped to the advertised host. It ties the stub's document to the
// assertion, so a change to the provider id or the options shape fails here
// rather than only on a host accept.
func TestStubProviderDocumentClassifiesAsTheAssertedCredential(t *testing.T) {
	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(provider.Stop)

	content := provider.ProviderConfig()[stubAgentConfigTarget]
	credentials := readback.InlineCredentialsIn(stubAgentConfigTarget, []byte(content))
	var found bool
	for _, credential := range credentials {
		if credential.Kind != readback.CredentialStatic || credential.EnvVar != uc1Scope.EnvVar {
			continue
		}
		found = true
		if !slices.Equal(credential.Hosts, uc1Scope.Hosts) {
			t.Errorf("credential hosts = %v, want %v", credential.Hosts, uc1Scope.Hosts)
		}
	}
	if !found {
		t.Fatalf("InlineCredentialsIn found no static credential %q in %q", uc1Scope.EnvVar, content)
	}
}

// TestStubAdvertisedHostTracksTheOrigin pins that the host a journey scopes its
// credential assertion to is the advertised origin's host (the default, or a
// host accept's override), so the asserted host and env.secret.yaml's scoping
// host cannot disagree.
func TestStubAdvertisedHostTracksTheOrigin(t *testing.T) {
	t.Setenv(stubOriginEnv, "https://stub.example:8443/path")
	if got, want := stubAdvertisedHost(), "stub.example"; got != want {
		t.Errorf("stubAdvertisedHost() = %q, want %q", got, want)
	}
}

// TestStartStubProviderBindsAndAdvertises pins the stub's own contract: it binds
// a real host-side listener, advertises the (overridden) origin on the
// listener's ephemeral port, and hands back the VM-home-relative config file
// whose provider.<id>.options.baseURL points at that address. The assertion is
// semantic (host, scheme, baseURL), not a golden of the document.
func TestStartStubProviderBindsAndAdvertises(t *testing.T) {
	const origin = "http://127.0.0.1"
	t.Setenv(stubOriginEnv, origin)

	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(provider.Stop)

	addr, err := url.Parse(provider.Addr())
	if err != nil {
		t.Fatalf("parse Addr %q: %v", provider.Addr(), err)
	}
	if addr.Scheme != "http" {
		t.Errorf("Addr scheme = %q, want http", addr.Scheme)
	}
	if addr.Hostname() != "127.0.0.1" {
		t.Errorf("Addr host = %q, want 127.0.0.1", addr.Hostname())
	}
	if addr.Port() == "" {
		t.Errorf("Addr %q carries no ephemeral port", provider.Addr())
	}

	config := provider.ProviderConfig()
	if len(config) != 1 {
		t.Fatalf("ProviderConfig has %d entries, want 1: %v", len(config), config)
	}
	content, ok := config[stubAgentConfigTarget]
	if !ok {
		t.Fatalf("ProviderConfig has no target %q: %v", stubAgentConfigTarget, config)
	}
	if got, want := providerBaseURL(t, content), provider.Addr()+"/v1"; got != want {
		t.Errorf("provider baseURL = %q, want %q", got, want)
	}

	assertStubReachable(t, provider.Addr()+"/v1/models")

	provider.Stop()
	provider.Stop() // idempotent
}

// TestStepSeedStubProvisionsThroughTheHomeManifest pins the fix for the
// first-run regression: StepSeedStub must get the provider config into the VM
// without writing a file under the launcher's agent config dir (which
// firstrun.managedConfigPresent counts as a managed layer) or under the isolated
// HOME's host config (which firstrun.HostFiles reads). It provisions through the
// launcher's `home:` manifest instead, so it writes a seed file and maps the VM
// target to it, and creates no managed agent-config layer.
func TestStepSeedStubProvisionsThroughTheHomeManifest(t *testing.T) {
	home := t.TempDir()
	ctx := &Context{TB: t, Agent: "opencode2", Home: home}

	if err := StepSeedStub().Run(ctx); err != nil {
		t.Fatalf("StepSeedStub: %v", err)
	}
	if ctx.Stub == nil {
		t.Fatal("StepSeedStub left no provider on the context")
	}

	seedPath := filepath.Join(home, "seed", stubAgentConfigTarget)
	content, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read the provisioned seed file %s: %v", seedPath, err)
	}
	if got, want := providerBaseURL(t, string(content)), ctx.Stub.Addr()+"/v1"; got != want {
		t.Errorf("provisioned baseURL = %q, want %q", got, want)
	}

	assertHomeManifestMaps(t, home, stubAgentConfigTarget, seedPath)
	assertNoManagedAgentConfigLayer(t, home)
}

// assertHomeManifestMaps checks the isolated launcher config declares a `home:`
// manifest entry mapping the VM-home-relative target to source.
func assertHomeManifestMaps(t *testing.T, home, target, source string) {
	t.Helper()

	launcherConfig := filepath.Join(home, "config", "agents-sandbox", "config.json")
	data, err := os.ReadFile(launcherConfig)
	if err != nil {
		t.Fatalf("read %s: %v", launcherConfig, err)
	}
	var config struct {
		Home map[string]string `json:"home"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode %s: %v", launcherConfig, err)
	}
	if got := config.Home[target]; got != source {
		t.Errorf("home manifest[%q] = %q, want %q", target, got, source)
	}
}

// assertNoManagedAgentConfigLayer fails when the isolated launcher config holds
// a regular file in any of the journey agent's config dirs, which would make
// firstrun treat the run as already configured and suppress the menu.
func assertNoManagedAgentConfigLayer(t *testing.T, home string) {
	t.Helper()

	for _, name := range []string{"opencode2", "opencode"} {
		dir := filepath.Join(home, "config", "agents-sandbox", name)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) > 0 {
			t.Errorf("StepSeedStub wrote a managed agent-config layer in %s: %v", dir, entries)
		}
	}
}

// providerBaseURL extracts provider.<id>.options.baseURL from a seeded
// provider document.
func providerBaseURL(t *testing.T, content string) string {
	t.Helper()

	var document struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("decode provider config %q: %v", content, err)
	}
	entry, ok := document.Provider[stubProviderID]
	if !ok {
		t.Fatalf("provider config %q has no provider %q", content, stubProviderID)
	}
	if entry.Options.BaseURL == "" {
		t.Fatalf("provider config %q has no baseURL", content)
	}
	return entry.Options.BaseURL
}

// assertStubReachable issues one request to the stub through a proxy-free,
// bounded client, so the test proves the listener really answers rather than
// only that it bound.
func assertStubReachable(t *testing.T, target string) {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Errorf("GET %s status = %d, want 200", target, response.StatusCode)
	}
}

// TestStartStubProviderAppendsTheListenerPort pins that the advertised address
// always carries the listener's own ephemeral port, whatever the override's
// origin, so two providers never collide on one address.
func TestStartStubProviderAppendsTheListenerPort(t *testing.T) {
	t.Setenv(stubOriginEnv, "https://127.0.0.1")

	first, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(first.Stop)
	second, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(second.Stop)

	if !strings.HasPrefix(first.Addr(), "https://127.0.0.1:") {
		t.Errorf("first Addr = %q, want an https://127.0.0.1:<port> address", first.Addr())
	}
	if first.Addr() == second.Addr() {
		t.Errorf("both providers advertise %q, want distinct ports", first.Addr())
	}
}
