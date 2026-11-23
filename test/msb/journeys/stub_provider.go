package journeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StubProvider is a host-side destination a journey's agent sends to, so the
// learn-mode terminating proxy observes a credential->destination pair without
// any real egress. It is the opencode2 provider config that points the agent at
// the listener plus the listener itself.
type StubProvider interface {
	// Addr is the advertised origin the VM should target, scheme://host:port,
	// carrying the listener's own ephemeral port. The agent's provider baseURL
	// is Addr()+"/v1".
	Addr() string
	// ProviderConfig is the VM-home-relative config manifest the launcher
	// provisions into the VM (driver.SeedLauncherConfig's target->content map).
	// Its one target is a config file opencode reads, so the file is written
	// through the launcher's `home:` manifest rather than the agent's launcher
	// config dir (which firstrun counts as a managed layer) or the host config
	// (which the first-run host seed reads).
	ProviderConfig() map[string]string
	// Requests returns the requests the stub served so far, in arrival order,
	// so a journey can assert that a turn reached it.
	Requests() []StubRequest
	// Stop closes the listener. It is idempotent.
	Stop()
}

// StubRequest is one request the stub served.
type StubRequest struct {
	Method string
	Path   string
	// Model is the request body's model, empty for a request without one.
	Model string
}

// stubOriginEnv overrides the advertised destination origin (scheme://host) the
// provider config points the agent at. The stub always binds host loopback on an
// ephemeral port; only the *advertised* origin changes. Unset (or invalid) uses
// stubOriginDefault.
const stubOriginEnv = "AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN"

// stubOriginDefault is the advertised destination a run uses with no override:
// microsandbox's host gateway name, which a VM reaches under the `host` network
// profile and which lands on the host's loopback listener. Host-gateway traffic
// does not pass the learn-mode observation proxy, so a turn to it completes but
// is not observed.
const stubOriginDefault = "http://" + stubGatewayHost

// stubGatewayHost is microsandbox's name for the host machine inside a VM.
const stubGatewayHost = "host.microsandbox.internal"

// stubAgentConfigTarget is the VM-home-relative config file the provider
// document is provisioned to. It is opencode's middle-precedence config file
// (config.json < opencode.json < opencode.jsonc), so the agent reads it. It is
// deliberately not a launcher snippet name: a file under the launcher's agent
// config dir would make firstrun count a managed layer and suppress the menu.
const stubAgentConfigTarget = ".config/opencode/opencode.json"

// The synthetic provider the seeded config declares: an OpenAI-compatible
// provider whose baseURL is the stub, plus the model selection that uses it.
const (
	stubProviderID    = "stub"
	stubProviderModel = "stub"
	// stubTitleModel is the provider's small model, which opencode uses for
	// side requests such as the session title. Keeping it apart from
	// stubProviderModel lets a journey tell its turn from those requests.
	stubTitleModel = "stub-title"
	// stubProviderNPM is the SDK package opencode loads for an
	// OpenAI-compatible provider, so the provider is real enough for the agent
	// to issue a request at the baseURL.
	stubProviderNPM = "@ai-sdk/openai-compatible"
	// stubProviderAPIKey is the provider's inline credential. The read-back
	// classifies a literal options.apiKey as a static secret and derives its
	// env-var name as derivedEnvVarName(stubProviderID); a journey asserts the
	// saved env.secret.yaml scopes that name to the provider's baseURL host. It
	// is a dummy value: the stub never checks it.
	stubProviderAPIKey = "sk-stub-not-a-real-key" //nolint:gosec // a dummy stub credential, never a real secret
	// stubCredentialEnvVar is the env-var name the read-back derives for
	// stubProviderAPIKey: <PROVIDERID>_API_KEY, i.e. derivedEnvVarName("stub").
	// Kept here beside the provider so a lifecycle change to the id cannot leave
	// a journey asserting a name the read-back no longer derives.
	stubCredentialEnvVar = "STUB_API_KEY" //nolint:gosec // an env-var name, not a secret value
)

// stubReadHeaderTimeout is a defensive ceiling on reading a request's headers;
// the value is not a contract and a local stub answers well within it.
const stubReadHeaderTimeout = 30 * time.Second

// StartStubProvider binds a host-side HTTP listener on an ephemeral loopback
// port and returns the StubProvider whose ProviderConfig points an agent at it.
// The advertised origin is stubOriginEnv (default stubOriginDefault); the port
// is always the listener's. The listener is closed by tb.Cleanup as well as by
// Stop.
//
// The stub serves plain HTTP so the agent's request traverses the terminating
// proxy with no CA or skip-upstream-verify wiring; the proxy records the
// destination by its advertised host. A host accept that switches the override
// to https must also enable network.observe.skip-upstream-verify, because the
// stub's certificate is not trusted.
func StartStubProvider(tb driver.TB) (StubProvider, error) {
	tb.Helper()

	//testenv:allow the stub is a real host-side listener the terminating proxy dials
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start stub provider listener: %w", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("resolve stub provider port: %w", err)
	}

	provider := &stubProvider{
		listener: listener,
		addr:     stubOrigin() + ":" + port,
	}
	provider.server = &http.Server{
		Handler: provider,
		// A defensive ceiling on reading a request's headers; the value is
		// not a contract.
		ReadHeaderTimeout: stubReadHeaderTimeout,
	}
	tb.Cleanup(provider.Stop)
	go func() { _ = provider.server.Serve(listener) }()
	return provider, nil
}

// stubProvider is the StubProvider a StartStubProvider call returns.
//
//exhaustruct:ignore
type stubProvider struct {
	server   *http.Server
	listener net.Listener
	addr     string
	once     sync.Once

	mu       sync.Mutex
	requests []StubRequest
}

// Addr returns the advertised origin, scheme://host:port.
func (p *stubProvider) Addr() string { return p.addr }

// ProviderConfig returns the one VM-home-relative config file pointing at the
// stub, for the launcher's `home:` manifest.
func (p *stubProvider) ProviderConfig() map[string]string {
	return map[string]string{stubAgentConfigTarget: p.providerDocument()}
}

// providerDocument renders the opencode provider config that points the agent at
// the stub.
func (p *stubProvider) providerDocument() string {
	return stubProviderDocument(p.addr)
}

// stubProviderDocument renders the opencode provider config that points an agent
// at addr: an OpenAI-compatible provider whose options.baseURL is addr+"/v1", an
// inline API key for the read-back to classify, and the top-level model that
// selects it. It is pure so a journey can render the same document from the
// context at run time — UC4 seeds it as the agent's own host config, whose
// filename differs from the stub's provisioned target.
func stubProviderDocument(addr string) string {
	document := map[string]any{
		"provider": map[string]any{
			stubProviderID: map[string]any{
				"npm":     stubProviderNPM,
				"options": map[string]any{"baseURL": addr + "/v1", "apiKey": stubProviderAPIKey},
				"models":  map[string]any{stubProviderModel: map[string]any{}, stubTitleModel: map[string]any{}},
			},
		},
		"model":       stubProviderID + "/" + stubProviderModel,
		"small_model": stubProviderID + "/" + stubTitleModel,
	}
	data, err := json.Marshal(document)
	if err != nil {
		// The document is all marshalable literals, so this is unreachable.
		return "{}"
	}
	return string(data)
}

// Stop closes the listener. It is idempotent, so tb.Cleanup may run it after a
// test already did.
func (p *stubProvider) Stop() {
	p.once.Do(func() { _ = p.server.Close() })
}

// stubOrigin returns the advertised destination origin, from stubOriginEnv when
// it names a usable http(s) origin, else stubOriginDefault.
func stubOrigin() string {
	//testenv:allow the stub provider reads its own advertised-route override
	return stubOriginFromValue(os.Getenv(stubOriginEnv))
}

// stubAdvertisedHost is the host part of the advertised origin the stub points
// agents at, so a journey scopes its credential assertion to the route the run
// actually used (the default, or a host accept's override). It reads the same
// override stubOrigin does, and an origin with no host yields "".
func stubAdvertisedHost() string {
	parsed, err := url.Parse(stubOrigin())
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

// stubOriginFromValue parses an advertised-origin override: an http(s) URL with
// a host yields scheme://hostname (its port, userinfo, and path are dropped,
// because the listener's own port is always used); anything else yields
// stubOriginDefault. An IPv6 literal host is re-bracketed, so `http://[::1]`
// round-trips rather than re-emitting the invalid `http://::1`. Kept pure so it
// is testable without the host.
func stubOriginFromValue(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return stubOriginDefault
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Scheme + "://" + bracketedHost(parsed.Hostname())
	default:
		return stubOriginDefault
	}
}

// bracketedHost wraps an IPv6 literal host in brackets, so an origin URL is
// valid; every other host is returned unchanged. url.Hostname strips the
// brackets, which must be restored when the host is emitted in a URL.
func bracketedHost(hostname string) string {
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}

// ServeHTTP records the request and answers it: the model list for a
// /models request, a chat completion (see serveChatCompletion) for anything else.
// A body that does not decode is answered as a non-streamed main-model request.
func (p *stubProvider) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	var completion chatRequest
	_ = json.NewDecoder(request.Body).Decode(&completion)
	p.record(StubRequest{Method: request.Method, Path: request.URL.Path, Model: completion.Model})
	if strings.HasSuffix(request.URL.Path, "/models") {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"object":"list","data":[{"id":"` + stubProviderModel + `","object":"model"}]}`))
		return
	}
	serveChatCompletion(response, completion)
}

func (p *stubProvider) record(request StubRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
}

// Requests returns a copy of the requests served so far.
func (p *stubProvider) Requests() []StubRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]StubRequest(nil), p.requests...)
}
