package journeys

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
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

// TestStartStubProviderSeedsAProviderConfig pins the stub's contract: it binds a
// real host-side listener, advertises the (overridden) origin on the listener's
// ephemeral port, and hands back an opencode snippet whose
// provider.<id>.options.baseURL points at that address and whose file name the
// opencode2 merge reads. The assertion is semantic (host, scheme, baseURL), not
// a golden of the document.
func TestStartStubProviderSeedsAProviderConfig(t *testing.T) {
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
	for name := range config {
		if name != stubSnippetName {
			t.Errorf("snippet name = %q, want %q", name, stubSnippetName)
		}
		if !agent.MergesSnippetName("opencode2", name) {
			t.Errorf("opencode2's merge does not read snippet %q", name)
		}
	}

	baseURL := providerBaseURL(t, config[stubSnippetName])
	if got, want := baseURL, provider.Addr()+"/v1"; got != want {
		t.Errorf("provider baseURL = %q, want %q", got, want)
	}

	assertStubReachable(t, provider.Addr()+"/v1/models")

	provider.Stop()
	provider.Stop() // idempotent
}

// providerBaseURL extracts provider.<id>.options.baseURL from a seeded snippet.
func providerBaseURL(t *testing.T, snippet string) string {
	t.Helper()

	var document struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(snippet), &document); err != nil {
		t.Fatalf("decode provider config %q: %v", snippet, err)
	}
	entry, ok := document.Provider[stubProviderID]
	if !ok {
		t.Fatalf("provider config %q has no provider %q", snippet, stubProviderID)
	}
	if entry.Options.BaseURL == "" {
		t.Fatalf("provider config %q has no baseURL", snippet)
	}
	return entry.Options.BaseURL
}

// assertStubReachable issues one request to the stub through a proxy-free client,
// so the test proves the listener really answers rather than only that it bound.
func assertStubReachable(t *testing.T, target string) {
	t.Helper()

	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
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
