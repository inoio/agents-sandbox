package opener

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// freePort returns a loopback port that is free right now.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// recordingFetcher answers every in-guest fetch with response and records the
// requested URLs.
type recordingFetcher struct {
	mutex    sync.Mutex
	urls     []string
	response string
	err      error
}

func (f *recordingFetcher) fetch(_ context.Context, url string) ([]byte, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.urls = append(f.urls, url)
	return []byte(f.response), f.err
}

func (f *recordingFetcher) fetched() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string(nil), f.urls...)
}

// noRedirectClient keeps redirects visible to the test instead of following them.
var noRedirectClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func TestForwarderReplaysCallbackInGuestAndRelaysResponse(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{
		response: "HTTP/1.1 302 Found\r\nLocation: https://platform.claude.com/oauth/code/success\r\nSet-Cookie: a=b\r\nContent-Length: 4\r\n\r\ndone",
	}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	response, err := noRedirectClient.Get(fmt.Sprintf("http://localhost:%d/callback?code=secret&state=s", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()

	if response.StatusCode != http.StatusFound ||
		response.Header.Get("Location") != "https://platform.claude.com/oauth/code/success" {
		t.Fatalf("status %d, headers %v", response.StatusCode, response.Header)
	}
	if string(body) != "done" {
		t.Fatalf("body %q", body)
	}
	if response.Header.Get("Set-Cookie") != "" {
		t.Fatal("guest Set-Cookie must not reach the host's localhost cookies")
	}
	if response.Header.Get("Content-Security-Policy") != "sandbox; default-src 'none'" {
		t.Fatalf("guest page policy must block scripts and subresources: %v", response.Header)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/callback?code=secret&state=s", port)
	if got := fetcher.fetched(); len(got) != 1 || got[0] != want {
		t.Fatalf("fetched %q, want %q", got, want)
	}
	waitForPortRelease(t, port)
}

// TestForwarderRelaysChunkedGuestResponse covers a guest server that streams
// its page: the raw chunked framing (curl --raw) is decoded once, not twice.
func TestForwarderRelaysChunkedGuestResponse(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{
		response: "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nTransfer-Encoding: chunked\r\n\r\n" +
			"6\r\n<p>ok \r\n5\r\nlogin\r\n0\r\n\r\n",
	}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=c", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "<p>ok login" ||
		response.Header.Get("Content-Type") != "text/html" {
		t.Fatalf("status %d, body %q, headers %v", response.StatusCode, body, response.Header)
	}
}

func TestForwarderRefusesPortTakenOnIPv6Loopback(t *testing.T) {
	occupied, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback on this machine")
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/"}); err == nil {
		t.Fatal("expected error when ::1 is taken")
	} else if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("err = %v, want it to wrap EADDRINUSE", err)
	}
	waitForPortRelease(t, port)
}

func TestForwarderAnswersOtherPathsLocally(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	for _, request := range []struct{ method, path string }{{http.MethodGet, "/favicon.ico"}, {http.MethodPost, "/callback"}} {
		httpRequest, _ := http.NewRequest(request.method, fmt.Sprintf("http://127.0.0.1:%d%s", port, request.path), nil)
		response, err := noRedirectClient.Do(httpRequest)
		if err != nil {
			t.Fatalf("%s %s: %v", request.method, request.path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s: status %d, want 404", request.method, request.path, response.StatusCode)
		}
	}
	if got := fetcher.fetched(); len(got) != 0 {
		t.Fatalf("forwarded %q, want nothing", got)
	}
}

func TestForwarderReportsGuestFailureAsBadGateway(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{err: errors.New("curl exit 7")}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/cb?code=x", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || strings.Contains(string(body), "code=x") {
		t.Fatalf("status %d, body %q", response.StatusCode, body)
	}
}

func TestForwarderRefusesPortInUseOnHost(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	port := occupied.Addr().(*net.TCPAddr).Port
	err = forwarder.Forward(context.Background(), LoopbackRedirect{Port: port, Path: "/"})
	if err == nil {
		t.Fatal("expected error for a port in use")
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("err = %v, want it to wrap EADDRINUSE", err)
	}
}

func TestForwarderReusesActiveForwardForSamePort(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	ctx := t.Context()
	redirect := LoopbackRedirect{Port: port, Path: "/callback"}
	if err := forwarder.Forward(ctx, redirect); err != nil {
		t.Fatalf("first Forward: %v", err)
	}
	if err := forwarder.Forward(ctx, redirect); err != nil {
		t.Fatalf("second Forward for a retried login: %v", err)
	}
}

// TestForwarderReusesActiveForwardWithFullExtendBuffer covers the non-blocking
// extend signal when a forward is reused faster than its lifetime goroutine
// drains the buffer: the reuse must not block. A forward whose lifetime
// goroutine has already exited (it stays in active until shutdown finishes) is
// the shape that fills the buffer.
func TestForwarderReusesActiveForwardWithFullExtendBuffer(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	forwarder.active[port] = &activeForward{extend: make(chan struct{}, 1)}
	forwarder.active[port].extend <- struct{}{}

	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward with a full extend buffer: %v", err)
	}
}

// TestForwarderServesReplacedCallbackPath covers a second login on the same
// port with a different path: it must replace the earlier forward's target, not
// keep serving the first path (which would 404 the new callback).
func TestForwarderServesReplacedCallbackPath(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/first"}); err != nil {
		t.Fatalf("first Forward: %v", err)
	}
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/second"}); err != nil {
		t.Fatalf("second Forward: %v", err)
	}

	stale, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/first?code=c", port))
	if err != nil {
		t.Fatalf("GET first: %v", err)
	}
	stale.Body.Close()
	if stale.StatusCode != http.StatusNotFound {
		t.Fatalf("stale path status %d, want 404", stale.StatusCode)
	}

	fresh, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/second?code=c", port))
	if err != nil {
		t.Fatalf("GET second: %v", err)
	}
	body, _ := io.ReadAll(fresh.Body)
	fresh.Body.Close()
	if fresh.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("replaced path status %d, body %q", fresh.StatusCode, body)
	}
	want := fmt.Sprintf("http://127.0.0.1:%d/second?code=c", port)
	if got := fetcher.fetched(); len(got) != 1 || got[0] != want {
		t.Fatalf("fetched %q, want %q", got, want)
	}
	waitForPortRelease(t, port)
}

// TestForwarderKeepsReusedListenerWhenEarlierCallbackReturns covers a second
// login that reuses the listener while the first login's callback is still being
// replayed in the VM: the earlier callback's return must not close the listener
// the new login's browser still has to reach, or that callback would fail
// although the launcher reported success.
func TestForwarderKeepsReusedListenerWhenEarlierCallbackReturns(t *testing.T) {
	port := freePort(t)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mutex sync.Mutex
	calls := 0
	fetcher := func(_ context.Context, _ string) ([]byte, error) {
		mutex.Lock()
		calls++
		call := calls
		mutex.Unlock()
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"), nil
	}
	forwarder := NewCallbackForwarder(fetcher)
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/first"}); err != nil {
		t.Fatalf("first Forward: %v", err)
	}

	firstReturned := make(chan struct{})
	go func() {
		defer close(firstReturned)
		response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/first?code=a", port))
		if err != nil {
			return
		}
		response.Body.Close()
	}()
	<-firstStarted

	// A new login reuses the listener on the same port with a new path while
	// the earlier callback is still occupying the first one.
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/second"}); err != nil {
		t.Fatalf("second Forward: %v", err)
	}
	close(releaseFirst)
	<-firstReturned
	// Give the earlier callback's return time to close the reused listener
	// were it (wrongly) allowed to.
	time.Sleep(150 * time.Millisecond)

	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/second?code=b", port))
	if err != nil {
		t.Fatalf("GET new login callback after the earlier one returned: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf(
			"new login callback status %d, body %q; the earlier callback closed the reused listener",
			response.StatusCode,
			body,
		)
	}
	waitForPortRelease(t, port)
}

// TestForwarderRefreshesLifetimeOnReuse covers a login that replaces an
// abandoned one on the same port: the second login must get the full lifetime,
// not inherit the first's nearly expired deadline (which would close the
// listener while the new login is still underway).
func TestForwarderRefreshesLifetimeOnReuse(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	forwarder.lifetime = 400 * time.Millisecond
	ctx := t.Context()
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/first"}); err != nil {
		t.Fatalf("first Forward: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/second"}); err != nil {
		t.Fatalf("second Forward: %v", err)
	}
	// Past the first login's deadline, but inside the refreshed one.
	time.Sleep(250 * time.Millisecond)
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/second?code=c", port))
	if err != nil {
		t.Fatalf("GET after reuse: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status %d, body %q; the reused forward's deadline was not refreshed", response.StatusCode, body)
	}
	waitForPortRelease(t, port)
}

// TestForwarderBlocksUnsafeGuestRedirects guards the host-browser boundary: a
// guest callback must not redirect the browser to a local host or a
// non-web/relative target, which would reach another service on the host.
func TestForwarderBlocksUnsafeGuestRedirects(t *testing.T) {
	for _, location := range []string{
		"http://127.0.0.1:9999/admin",
		"http://localhost/admin",
		"http://[::1]:9999/",
		"http://2130706433/admin",
		"http://127.1/admin",
		"/relative",
		"javascript:alert(1)",
	} {
		t.Run(location, func(t *testing.T) {
			port := freePort(t)
			fetcher := &recordingFetcher{
				response: "HTTP/1.1 302 Found\r\nLocation: " + location + "\r\nContent-Length: 0\r\n\r\n",
			}
			forwarder := NewCallbackForwarder(fetcher.fetch)
			if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
				t.Fatalf("Forward: %v", err)
			}
			response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/cb?code=c", port))
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200 local page", response.StatusCode)
			}
			if leaked := response.Header.Get("Location"); leaked != "" {
				t.Fatalf("blocked redirect leaked Location %q", leaked)
			}
			if got := response.Header.Get("Content-Security-Policy"); got != guestPagePolicy {
				t.Fatalf("Content-Security-Policy = %q, want %q", got, guestPagePolicy)
			}
			if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if !strings.Contains(string(body), "blocked") {
				t.Fatalf("body %q, want a blocked-redirect notice", body)
			}
		})
	}
}

// TestForwarderBlocksWhenAnyGuestRedirectIsUnsafe covers a duplicated Location
// header: a safe first value must not let an unsafe second one through.
func TestForwarderBlocksWhenAnyGuestRedirectIsUnsafe(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{
		response: "HTTP/1.1 302 Found\r\n" +
			"Location: https://example.com/ok\r\n" +
			"Location: http://127.0.0.1:9/admin\r\n" +
			"Content-Length: 0\r\n\r\n",
	}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/cb?code=c", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" {
		t.Fatalf("status %d, Location %q", response.StatusCode, response.Header.Get("Location"))
	}
	if !strings.Contains(string(body), "blocked") {
		t.Fatalf("body %q, want a blocked-redirect notice", body)
	}
}

func TestIsSafeRedirectLocation(t *testing.T) {
	for location, want := range map[string]bool{
		"https://platform.claude.com/oauth/code/success": true,
		"http://example.com/":                            true,
		"http://127.0.0.1:9999/x":                        false,
		"http://localhost/x":                             false,
		"http://[::1]:1/":                                false,
		"http://2130706433/x":                            false,
		"/relative":                                      false,
		"ftp://example.com/x":                            false,
		"":                                               false,
		"https://[::1":                                   false,
		"https://":                                       false,
	} {
		if got := isSafeRedirectLocation(location); got != want {
			t.Errorf("isSafeRedirectLocation(%q) = %v, want %v", location, got, want)
		}
	}
}

func TestForwarderClosesWhenContextEnds(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	ctx, cancel := context.WithCancel(context.Background())
	if err := forwarder.Forward(ctx, LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	cancel()
	waitForPortRelease(t, port)
}

func TestForwarderClosesAfterLifetime(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	forwarder.lifetime = 20 * time.Millisecond
	if err := forwarder.Forward(context.Background(), LoopbackRedirect{Port: port, Path: "/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	waitForPortRelease(t, port)
}

// waitForPortRelease fails unless port becomes bindable again, which shows the
// forwarder stopped listening.
func waitForPortRelease(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listener.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port %d still in use", port)
}

func TestHostHandlerForwardsLoopbackRedirectThenOpens(t *testing.T) {
	port := freePort(t)
	var opened []string
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	handler := NewHostHandler(func(target string) error {
		opened = append(opened, target)
		return nil
	}, forwarder)
	ctx := t.Context()
	target := fmt.Sprintf(
		"https://claude.ai/oauth/authorize?redirect_uri=http%%3A%%2F%%2Flocalhost%%3A%d%%2Fcallback",
		port,
	)

	if err := handler(ctx, target); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(opened) != 1 || opened[0] != target {
		t.Fatalf("opened %q", opened)
	}
	if listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		listener.Close()
		t.Fatal("callback port is not being forwarded")
	}
}

func TestHostHandlerRefusesLoopbackRedirectWhenForwardFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	handler := NewHostHandler(func(string) error {
		t.Fatal("must not open when the callback cannot be forwarded")
		return nil
	}, NewCallbackForwarder((&recordingFetcher{}).fetch))
	target := fmt.Sprintf("https://x.example/a?redirect_uri=http%%3A%%2F%%2F127.0.0.1%%3A%d%%2Fcb", port)
	err = handler(context.Background(), target)
	if !errors.Is(err, errLoopbackRedirect) {
		t.Fatalf("err = %v, want errLoopbackRedirect", err)
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("err = %v, want it to also carry the bind failure", err)
	}
}

func TestForwarderAnswersBadGatewayForMalformedGuestResponses(t *testing.T) {
	for _, raw := range []string{
		"not http at all",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n",
	} {
		t.Run(raw, func(t *testing.T) {
			port := freePort(t)
			forwarder := NewCallbackForwarder((&recordingFetcher{response: raw}).fetch)
			if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
				t.Fatalf("Forward: %v", err)
			}
			response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/cb", port))
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusBadGateway {
				t.Fatalf("status %d, want 502", response.StatusCode)
			}
		})
	}
}

func TestForwarderRelaysTruncatedGuestBody(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder(
		(&recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\npart"}).fetch,
	)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/cb", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "part" {
		t.Fatalf("status %d, body %q", response.StatusCode, body)
	}
}

// TestRelayResponseSetsSandboxHeaders covers the headers the relay adds to a
// guest page, which keep it from reaching another local service.
func TestRelayResponseSetsSandboxHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	raw := "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 2\r\n\r\nhi"
	if _, err := relayResponse(recorder, []byte(raw), 0); err != nil {
		t.Fatalf("relayResponse: %v", err)
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got != guestPagePolicy {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, guestPagePolicy)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestRelayResponseWrapsGuestErrors covers the parse and read failures carrying
// their cause, so a caller can inspect it.
func TestRelayResponseWrapsGuestErrors(t *testing.T) {
	for _, raw := range []string{
		"not http at all",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n",
	} {
		_, err := relayResponse(httptest.NewRecorder(), []byte(raw), 0)
		if err == nil || errors.Unwrap(err) == nil {
			t.Fatalf("relayResponse(%q) = %v, want a wrapped cause", raw, err)
		}
	}
}

// stubListenTCP makes binding ::1 fail with ipv6Err and binds everything else
// for real.
func stubListenTCP(t *testing.T, ipv6Err error) {
	t.Helper()
	original := listenTCP
	listenTCP = func(ctx context.Context, address string) (net.Listener, error) {
		if strings.HasPrefix(address, "[::1]") {
			return nil, ipv6Err
		}
		return original(ctx, address)
	}
	t.Cleanup(func() { listenTCP = original })
}

func TestForwarderWorksWithoutIPv6Loopback(t *testing.T) {
	stubListenTCP(t, errors.New("cannot assign requested address"))
	port := freePort(t)
	fetcher := &recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/"}); err != nil {
		t.Fatalf("Forward without IPv6: %v", err)
	}
	// The IPv4 listener must still serve: without it the host browser resolves
	// localhost to 127.0.0.1 and reaches nothing.
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/?code=c", port))
	if err != nil {
		t.Fatalf("GET without IPv6: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status %d, body %q", response.StatusCode, body)
	}
}

func TestForwarderRebindsAfterLifetimeCloses(t *testing.T) {
	port := freePort(t)
	fetcher := &recordingFetcher{response: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	forwarder.lifetime = 20 * time.Millisecond
	if err := forwarder.Forward(context.Background(), LoopbackRedirect{Port: port, Path: "/first"}); err != nil {
		t.Fatalf("first Forward: %v", err)
	}
	waitForPortRelease(t, port)

	// The closed forward must be dropped from active and release the mutex, or
	// the next login can neither bind the port nor proceed.
	done := make(chan error, 1)
	go func() {
		done <- forwarder.Forward(context.Background(), LoopbackRedirect{Port: port, Path: "/second"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Forward: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Forward blocked: the closed forward did not release the mutex")
	}
	response, err := noRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%d/second?code=c", port))
	if err != nil {
		t.Fatalf("GET after rebind: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status %d, body %q; the port was not rebound", response.StatusCode, body)
	}
}

func TestForwarderReleasesIPv4WhenIPv6PortIsTaken(t *testing.T) {
	stubListenTCP(t, fmt.Errorf("listen: %w", syscall.EADDRINUSE))
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/"}); err == nil {
		t.Fatal("expected error when ::1 is taken")
	} else if !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("err = %v, want it to wrap EADDRINUSE", err)
	}
	waitForPortRelease(t, port)
}

// TestRetireKeepsAForwardExtendedAfterItsTimerFired covers the race between a
// retried login and an expiring forward: an extension that arrived while the
// lifetime goroutine was already retiring keeps the forward alive.
func TestRetireKeepsAForwardExtendedAfterItsTimerFired(t *testing.T) {
	port := freePort(t)
	forwarder := NewCallbackForwarder((&recordingFetcher{}).fetch)
	forwarder.lifetime = time.Hour
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	forwarder.mutex.Lock()
	forward := forwarder.active[port]
	forwarder.mutex.Unlock()
	forward.extend <- struct{}{}

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	if forwarder.retire(t.Context(), t.Context(), forward, &http.Server{}, port, timer) {
		t.Fatal("retire closed a forward that was extended")
	}
	forwarder.mutex.Lock()
	_, stillActive := forwarder.active[port]
	forwarder.mutex.Unlock()
	if !stillActive {
		t.Fatal("extended forward was dropped from active")
	}
}
