package journeys

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// hostOpenCommand is the command the launcher's host opener runs on goos
// (open on macOS, xdg-open elsewhere); the fake browser takes its place on
// PATH, so the journey never opens a real browser.
func hostOpenCommand(goos string) string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// loginCallbackCode is the authorization code the fake browser returns with.
// No provider issued it, so the harness's token exchange fails; the journey
// asserts the hand-over, not the login.
const loginCallbackCode = "journeys-not-a-real-code"

// Timeouts for the login hand-over. The link waits on the VM start; the
// callback on the harness's token exchange, which the forwarder bounds at two
// minutes.
const (
	loginLinkTimeout     = time.Minute
	loginCallbackTimeout = 150 * time.Second
	loginCallbackDial    = 10 * time.Second
)

// HostBrowser is a fake host link handler: an executable named like the host
// opener, first on the journey launcher's PATH, that records each link it is
// asked to open instead of opening it.
type HostBrowser struct {
	dir string
	log string
	// taken is how many recorded links a step has already taken.
	taken int
}

// newHostBrowser writes the fake handler into dir.
func newHostBrowser(dir string) (*HostBrowser, error) {
	browser := &HostBrowser{dir: dir, log: filepath.Join(dir, "opened.log")}
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + shellQuote(browser.log) + "\n"
	if err := os.WriteFile(filepath.Join(dir, hostOpenCommand(runtime.GOOS)), []byte(script), 0o755); err != nil {
		return nil, fmt.Errorf("write the fake host browser: %w", err)
	}
	return browser, nil
}

// FirstOnPath returns env with the fake handler's dir first on PATH.
func (b *HostBrowser) FirstOnPath(env []string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			entry = "PATH=" + b.dir + string(os.PathListSeparator) + value
			found = true
		}
		out = append(out, entry)
	}
	if !found {
		out = append(out, "PATH="+b.dir)
	}
	return out
}

// Opened returns the links the handler was asked to open, in order.
func (b *HostBrowser) Opened() ([]string, error) {
	file, err := os.Open(b.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var links []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			links = append(links, line)
		}
	}
	return links, scanner.Err()
}

// LoginLink is a harness's authorization link and the loopback callback in
// the VM it names.
type LoginLink struct {
	Link     string
	Callback *url.URL
	State    string
}

// parseLoginLink reads link as an http(s) authorization URL whose redirect_uri
// is a loopback http callback with a port.
func parseLoginLink(link string) (*LoginLink, error) {
	parsed, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("the opened link %q is not a URL: %w", link, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("the opened link %q is not a web authorization URL", link)
	}
	query := parsed.Query()
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || callback.Scheme != "http" || callback.Port() == "" || !isLoopbackHost(callback.Hostname()) {
		return nil, fmt.Errorf("the opened link %q names no loopback redirect_uri with a port", link)
	}
	return &LoginLink{Link: link, Callback: callback, State: query.Get("state")}, nil
}

// isLoopbackHost reports whether host names the loopback interface.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CallbackAnswer is what the host's callback forwarder answered the browser.
type CallbackAnswer struct {
	Status int
	Header http.Header
	Body   string
}

// authorizeRedirect opens link with the browser's request, dialing loopback for
// every host (the stub's authorize endpoint is the link's only host) and
// trusting roots for an HTTPS stub (nil: the system's), and returns where the
// authorization server sends the browser back.
func authorizeRedirect(link, loopback string, roots *x509.CertPool, timeout time.Duration) (*url.URL, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:           nil,
			DialContext:     loopbackDialer(loopback),
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		},
	}
	//nolint:noctx // the client's timeout bounds the request
	response, err := client.Get(link)
	if err != nil {
		return nil, fmt.Errorf("open the authorization link: %w", err)
	}
	defer response.Body.Close()
	location, err := response.Location()
	if err != nil {
		return nil, fmt.Errorf("the authorization server answered %d without a redirect: %w", response.StatusCode, err)
	}
	return location, nil
}

// linkOriginProblem reports a link whose origin is not origin, the stub
// authorization server's, or nil: the browser signs in only there.
func linkOriginProblem(link, origin string) error {
	parsed, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("the opened link %q is not a URL: %w", link, err)
	}
	if got := parsed.Scheme + "://" + parsed.Host; got != origin {
		return fmt.Errorf("the opened link %q is on %s, not the stub authorization server's %s", link, got, origin)
	}
	return nil
}

// loopbackDialer dials loopback whatever address a request names: the
// browser's resolution of the VM's name for the host to the host itself.
func loopbackDialer(loopback string) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, network, loopback)
	}
}

// onHostLoopback is target with its host replaced by the host's IPv4 loopback,
// keeping the port, path, and query: where a browser lands for a localhost
// callback.
func onHostLoopback(target *url.URL) string {
	landed := *target
	landed.Host = net.JoinHostPort("127.0.0.1", target.Port())
	return landed.String()
}

// callbackURL is the browser's return address for login: the callback on the
// host's IPv4 loopback with the link's state, loginCallbackCode, and any extra
// provider-specific parameters. The original callback's query is preserved.
func callbackURL(login *LoginLink, parameters map[string]string) string {
	query := login.Callback.Query()
	for key, value := range parameters {
		query.Set(key, value)
	}
	query.Set("code", loginCallbackCode)
	if login.State != "" {
		query.Set("state", login.State)
	}
	target := *login.Callback
	target.Host = net.JoinHostPort("127.0.0.1", login.Callback.Port())
	target.RawQuery = query.Encode()
	return target.String()
}

// returnToCallback requests target without following redirects, retrying a
// refused connection briefly in case the forward is still being set up.
func returnToCallback(target string, timeout time.Duration) (*CallbackAnswer, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var answer *CallbackAnswer
	var dialErr error
	err := pollUntil(loginCallbackDial, stubPollInterval, func() (bool, error) {
		//nolint:noctx // the client's timeout bounds the request
		response, err := client.Get(target)
		if err != nil {
			dialErr = err
			return false, nil
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return false, fmt.Errorf("read the callback answer: %w", err)
		}
		answer = &CallbackAnswer{Status: response.StatusCode, Header: response.Header, Body: string(body)}
		return true, nil
	}, func() error {
		return fmt.Errorf("the host forwards no callback on %s: %w", target, dialErr)
	})
	return answer, err
}

// callbackAnswerProblem reports why answer is not the harness's own page
// (showing marker) relayed under the host boundary, or nil. An answer the
// forwarder writes itself (a 404, a failed replay, a blocked redirect) lacks
// the harness's marker.
func callbackAnswerProblem(answer *CallbackAnswer, marker string) error {
	if marker == "" {
		return errors.New("the adapter declares no LoginCallbackMarker")
	}
	if !strings.Contains(answer.Body, marker) {
		return fmt.Errorf(
			"the forwarder answered %d without the harness's page (%q): %.300q",
			answer.Status,
			marker,
			answer.Body,
		)
	}
	if policy := answer.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "sandbox") {
		return fmt.Errorf("the relayed answer carries no sandboxing Content-Security-Policy (%q)", policy)
	}
	if cookies := answer.Header.Values("Set-Cookie"); len(cookies) > 0 {
		return fmt.Errorf("the relayed answer sets cookies on the host: %q", cookies)
	}
	return nil
}
