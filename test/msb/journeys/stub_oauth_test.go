package journeys

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// startTestOAuthServer starts the stub and a client that reaches every origin
// on its loopback listener, as the VM reaches the host gateway.
func startTestOAuthServer(t *testing.T) (*StubOAuthServer, *http.Client) {
	t.Helper()
	stub, err := StartStubOAuthServer(t, false)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: loopbackDialer(stub.LoopbackAddr())},
	}
	return stub, client
}

func TestStubOAuthServerRunsTheAuthorizationCodeFlowWithPKCE(t *testing.T) {
	stub, client := startTestOAuthServer(t)
	mcp := stub.MCPURL()

	response := mustPost(t, client, mcp, "", nil)
	if response.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(response.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("an unauthorized MCP call = %d %q, want 401 naming the resource metadata",
			response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}

	const verifier = "journeys-verifier-0123456789-0123456789-0123456789"
	authorize := stub.vmOrigin + "/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {"journeys-client"},
		"redirect_uri":          {"http://127.0.0.1:4242/callback"},
		"state":                 {"the-state"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode()
	back, err := authorizeRedirect(authorize, stub.LoopbackAddr(), nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if back.Host != "127.0.0.1:4242" || back.Query().Get("state") != "the-state" || back.Query().Get("code") == "" {
		t.Fatalf("authorize redirected to %s, want the callback with the state and a code", back)
	}
	code := back.Query().Get("code")

	wrong := mustPost(
		t,
		client,
		stub.vmOrigin+"/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(
			url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {"wrong"}}.Encode(),
		),
	)
	if wrong.StatusCode != http.StatusBadRequest || len(stub.Issued()) != 0 {
		t.Fatalf("a wrong PKCE verifier got %d and tokens %v, want a refusal", wrong.StatusCode, stub.Issued())
	}
	right := mustPost(
		t,
		client,
		stub.vmOrigin+"/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(
			url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}}.Encode(),
		),
	)
	if right.StatusCode != http.StatusOK || len(stub.Issued()) != 1 {
		t.Fatalf("the right verifier got %d and tokens %v, want one token", right.StatusCode, stub.Issued())
	}

	authorized := mustPost(t, client, mcp, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`),
		"Bearer "+stub.Issued()[0])
	body, _ := io.ReadAll(authorized.Body)
	if authorized.StatusCode != http.StatusOK || !strings.Contains(string(body), `"protocolVersion":"2025-06-18"`) ||
		len(stub.AuthorizedMCPCalls()) != 1 {
		t.Errorf("an authorized initialize = %d %s (authorized calls %v), want the server's answer",
			authorized.StatusCode, body, stub.AuthorizedMCPCalls())
	}
	if before := stub.ResetAuthorizedMCPCalls(); before != 1 || len(stub.AuthorizedMCPCalls()) != 0 {
		t.Errorf("reset returned %d and left %v, want 1 and none", before, stub.AuthorizedMCPCalls())
	}
}

func TestStubOAuthServerRefusesTokensItDidNotIssue(t *testing.T) {
	stub, client := startTestOAuthServer(t)
	unknown := mustPost(t, client, stub.MCPURL(), "application/json", strings.NewReader(`{}`), "Bearer forged")
	if unknown.StatusCode != http.StatusUnauthorized || len(stub.AuthorizedMCPCalls()) != 0 {
		t.Errorf("an unissued token got %d, want 401", unknown.StatusCode)
	}
	refresh := mustPost(t, client, stub.Origin()+"/token", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"forged"}}.Encode()))
	if refresh.StatusCode != http.StatusBadRequest || len(stub.Issued()) != 0 {
		t.Errorf("an unissued refresh token got %d and tokens %v, want a refusal", refresh.StatusCode, stub.Issued())
	}
}

func TestStubOAuthServerRejectsUnsafeAuthorizationRedirects(t *testing.T) {
	stub, client := startTestOAuthServer(t)
	redirectURIs := []struct {
		name string
		uri  string
	}{
		{name: "external host", uri: "https://attacker.example/callback"},
		{name: "scheme-relative host", uri: "//attacker.example/callback"},
		{name: "localhost lookalike", uri: "http://localhost.attacker.example:4242/callback"},
		{name: "missing port", uri: "http://127.0.0.1/callback"},
		{name: "userinfo", uri: "http://attacker.example@127.0.0.1:4242/callback"},
		{name: "backslash", uri: `http://attacker.example\@127.0.0.1:4242/callback`},
	}
	for _, testCase := range redirectURIs {
		t.Run(testCase.name, func(t *testing.T) {
			target := stub.Origin() + "/authorize?" + url.Values{
				"redirect_uri":   {testCase.uri},
				"code_challenge": {pkceChallenge("verifier")},
			}.Encode()
			response, err := client.Get(target)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Errorf("authorize status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
			if location := response.Header.Get("Location"); location != "" {
				t.Errorf("unsafe redirect location = %q, want none", location)
			}
		})
	}
}

// mustPost sends a POST and fails the test on a transport error; an optional
// authorization header value comes last.
func mustPost(t *testing.T, client *http.Client, target, contentType string, body io.Reader,
	authorization ...string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for _, value := range authorization {
		request.Header.Set("Authorization", value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", target, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// An HTTPS stub serves on the VM's name for the host with a certificate its
// own test CA issued, which a client trusting that CA accepts.
func TestStubOAuthServerServesHTTPSUnderItsTestCA(t *testing.T) {
	stub, err := StartStubOAuthServer(t, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stub.Origin(), "https://"+stubGatewayHost+":") || len(stub.CAPEM()) == 0 {
		t.Fatalf(
			"origin %q, CA %d bytes; want https on %s with a CA",
			stub.Origin(),
			len(stub.CAPEM()),
			stubGatewayHost,
		)
	}
	authorize := stub.Origin() + "/authorize?" + url.Values{
		"redirect_uri":   {"http://127.0.0.1:4242/callback"},
		"code_challenge": {pkceChallenge("verifier")},
	}.Encode()
	if _, err := authorizeRedirect(authorize, stub.LoopbackAddr(), stub.rootCAs(), 5*time.Second); err != nil {
		t.Errorf("a client trusting the test CA: %v", err)
	}
	if _, err := authorizeRedirect(authorize, stub.LoopbackAddr(), x509.NewCertPool(), 5*time.Second); err == nil {
		t.Error("a client trusting nothing reached the HTTPS stub")
	}
}
