package journeys

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStubRecordsCredentialsWithoutRetainingTheirValues(t *testing.T) {
	for _, header := range []string{"Authorization", "X-Api-Key"} {
		t.Run(header, func(t *testing.T) {
			provider := startTestStub(t)
			const credential = "synthetic-auth-evidence-only"
			request, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				stubLoopbackURL(t, provider)+"/v1/models",
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set(header, credential)
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			recorded := provider.Requests()[0]
			if !recorded.HasCredential(strings.ToLower(header), credential) {
				t.Fatal("the request did not record the credential")
			}
			for _, value := range []string{"", "wrong-key", "${STUB_API_KEY}"} {
				if recorded.HasCredential(header, value) {
					t.Error("a missing, wrong, or placeholder credential matched")
				}
			}
			if recorded.HasCredential("Cookie", credential) || recorded.TLS {
				t.Error("recorded an unrelated header or TLS on a plain HTTP request")
			}
			if strings.Contains(fmt.Sprintf("%+v", recorded), credential) {
				t.Error("request diagnostics retain the credential value")
			}
		})
	}
}

func TestTLSStubServesWithItsOwnCAAndRecordsTLS(t *testing.T) {
	provider, err := StartTLSStubProvider(t)
	if err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(provider.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if address.Scheme != "https" || address.Hostname() != stubGatewayHost {
		t.Fatalf("TLS stub advertises %q, want HTTPS on the host gateway", provider.Addr())
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(provider.CAPEM()) {
		t.Fatal("TLS stub supplied no CA")
	}
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", address.Port()))
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	for _, failure := range []string{"untrusted CA", "wrong hostname"} {
		t.Run(failure, func(t *testing.T) {
			untrusted := transport.Clone()
			if failure == "untrusted CA" {
				untrusted.TLSClientConfig.RootCAs = x509.NewCertPool()
			} else {
				untrusted.TLSClientConfig.ServerName = "wrong-host.invalid"
			}
			t.Cleanup(untrusted.CloseIdleConnections)
			client := &http.Client{Timeout: 5 * time.Second, Transport: untrusted}
			response, requestErr := client.Get(provider.Addr() + "/v1/models")
			if requestErr == nil {
				_ = response.Body.Close()
				t.Fatal("an untrusted TLS connection succeeded")
			}
			if len(provider.Requests()) != 0 {
				t.Fatal("a failed TLS handshake recorded an HTTP request")
			}
		})
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, provider.Addr()+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer synthetic-tls-key")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("TLS stub status = %d, want 200", response.StatusCode)
	}
	recorded := provider.Requests()[0]
	if !recorded.TLS || !recorded.HasCredential("Authorization", "Bearer synthetic-tls-key") {
		t.Fatalf("TLS request lacks transport or credential evidence: %+v", recorded)
	}
	provider.Stop()
	assertStubUnreachable(t, "http://127.0.0.1:"+address.Port()+"/v1/models")
}

func TestStubDoesNotAcceptMissingOrAmbiguousCredentials(t *testing.T) {
	for _, header := range []string{"Authorization", "X-Api-Key"} {
		for _, values := range [][]string{nil, {""}, {"key", "other"}, {"key", "key"}} {
			t.Run(fmt.Sprintf("%s/%d-values/%s", header, len(values), strings.Join(values, "-")), func(t *testing.T) {
				provider := startTestStub(t)
				request, err := http.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					stubLoopbackURL(t, provider)+"/v1/models",
					nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range values {
					request.Header.Add(header, value)
				}
				client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if provider.Requests()[0].HasCredential(header, "key") {
					t.Fatal("an absent, empty, or duplicate credential matched")
				}
			})
		}
	}
}

func TestStubCAIsPrivateToEachTLSListener(t *testing.T) {
	plain := startTestStub(t)
	if plain.CAPEM() != nil {
		t.Fatal("an HTTP listener supplied a CA")
	}
	first, err := StartTLSStubProvider(t)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StartTLSStubProvider(t)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.CAPEM()) == string(second.CAPEM()) || first.Addr() == second.Addr() {
		t.Fatal("TLS listeners share a CA or an address")
	}
	certificate := first.CAPEM()
	certificate[0] = '!'
	if first.CAPEM()[0] == '!' {
		t.Fatal("CAPEM exposed mutable listener state")
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

	config := opencode2Harness.Stub.HomeFiles(provider.Addr())
	if len(config) != 1 {
		t.Fatalf("StubConfig has %d entries, want 1: %v", len(config), config)
	}
	content, ok := config[opencodeStubConfigTarget]
	if !ok {
		t.Fatalf("StubConfig has no target %q: %v", opencodeStubConfigTarget, config)
	}
	if got, want := providerBaseURL(t, content), provider.Addr()+"/v1"; got != want {
		t.Errorf("provider baseURL = %q, want %q", got, want)
	}

	models := assertStubReachable(t, provider.Addr()+"/v1/models")
	if !strings.Contains(models, `"object":"list"`) || !strings.Contains(models, `"id":"`+stubProviderModel+`"`) {
		t.Errorf("GET /v1/models body = %q, want the model list naming %q", models, stubProviderModel)
	}

	provider.Stop()
	provider.Stop() // idempotent
	assertStubUnreachable(t, provider.Addr()+"/v1/models")
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
	ctx := &Context{TB: t, Agent: "opencode2", Harness: opencode2Harness, Home: home}

	if err := StepSeedStub().Run(ctx); err != nil {
		t.Fatalf("StepSeedStub: %v", err)
	}
	if ctx.Stub == nil {
		t.Fatal("StepSeedStub left no provider on the context")
	}

	seedPath := filepath.Join(home, "seed", opencodeStubConfigTarget)
	content, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read the provisioned seed file %s: %v", seedPath, err)
	}
	if got, want := providerBaseURL(t, string(content)), ctx.Stub.Addr()+"/v1"; got != want {
		t.Errorf("provisioned baseURL = %q, want %q", got, want)
	}

	assertHomeManifestMaps(t, home, opencodeStubConfigTarget, seedPath)
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
// only that it bound, asserts the stub's JSON content type, and returns the
// response body.
func assertStubReachable(t *testing.T, target string) string {
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
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("GET %s Content-Type = %q, want application/json", target, contentType)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", target, err)
	}
	return string(body)
}

// assertStubUnreachable asserts the stub no longer answers, so Stop closed the
// listener rather than only flipping a flag.
func assertStubUnreachable(t *testing.T, target string) {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Get(target)
	if err == nil {
		_ = response.Body.Close()
		t.Fatalf("GET %s succeeded after Stop, want a refused connection", target)
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

func TestStubAnswersAnUnknownPathWithNotFound(t *testing.T) {
	provider := startTestStub(t)

	response := postChat(t, provider, `{"model":"stub","tools":[{}]}`)
	_ = response.Body.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		stubLoopbackURL(t, provider)+"/v1/other",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/other: %v", err)
	}
	_ = unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", unknown.StatusCode)
	}
	if got := provider.Replies(); len(got) != 1 {
		t.Errorf("replies = %v, want only the chat completion's", got)
	}
}

func TestStubAnswersAnUndecodableTurnWithBadRequest(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(ToolCall{Name: "write", Arguments: `{}`})

	response := postChat(t, provider, "\x28\xb5\x2f\xfd compressed")

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
	requests := provider.Requests()
	if len(requests) != 1 || requests[0].DecodeError == "" || servedTurn(requests) {
		t.Errorf("requests = %+v, want one recorded with its decode error and not a turn", requests)
	}
}

func TestTurnProtocolForRoutesEachProtocolsTurnPath(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/chat/completions": stubCompletionPath,
		"/v1/messages":         stubMessagesPath,
		"/v1/responses":        stubResponsesPath,
	} {
		protocol, ok := turnProtocolFor(path)
		if !ok || protocol.suffix != want {
			t.Errorf("turnProtocolFor(%q) = %q, %v; want %q", path, protocol.suffix, ok, want)
		}
	}
	for _, path := range []string{"/v1/messages/count_tokens", "/v1/models", "/v1/other"} {
		if _, ok := turnProtocolFor(path); ok {
			t.Errorf("turnProtocolFor(%q) found a turn protocol", path)
		}
	}
}

func TestStepSeedStubReportsASeedFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home-is-a-file")
	if err := os.WriteFile(home, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{TB: t, Agent: "opencode2", Harness: opencode2Harness, Home: home}

	err := StepSeedStub().Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "seed the home") {
		t.Fatalf("StepSeedStub = %v, want a seed-the-home error", err)
	}
}
