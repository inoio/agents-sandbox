package opener

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// pathFetcher answers each in-guest fetch by the request path and records the
// requested URLs.
type pathFetcher struct {
	mutex     sync.Mutex
	urls      []string
	responses map[string]string
}

func (f *pathFetcher) fetch(_ context.Context, guestURL string) ([]byte, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.urls = append(f.urls, guestURL)
	for path, response := range f.responses {
		if strings.Contains(guestURL, path+"?") {
			return []byte(response), nil
		}
	}
	return []byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"), nil
}

func (f *pathFetcher) fetched() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string(nil), f.urls...)
}

func getWithoutRedirect(t *testing.T, address string) (*http.Response, string) {
	t.Helper()
	response, err := noRedirectClient.Get(address)
	if err != nil {
		t.Fatalf("GET %s: %v", address, err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response, string(body)
}

// TestForwarderFollowsGuestRedirectToItsOwnPort covers codex's login: its
// callback saves the tokens and redirects to /success on the same loopback
// port, and the login finishes only once that page is fetched. The redirect
// lands on the forwarder's own listener, so it is relayed and the follow-up
// request is replayed in the guest before the forward closes.
func TestForwarderFollowsGuestRedirectToItsOwnPort(t *testing.T) {
	port := freePort(t)
	successLocation := fmt.Sprintf("http://localhost:%d/success?id_token=t", port)
	fetcher := &pathFetcher{responses: map[string]string{
		"/auth/callback": "HTTP/1.1 302 Found\r\nLocation: " + successLocation + "\r\nContent-Length: 0\r\n\r\n",
		"/success":       "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 9\r\n\r\nsigned in",
	}}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/auth/callback"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}

	response, _ := getWithoutRedirect(t, fmt.Sprintf("http://127.0.0.1:%d/auth/callback?code=c&state=s", port))
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != successLocation {
		t.Fatalf("callback: status %d, Location %q, want 302 to %q",
			response.StatusCode, response.Header.Get("Location"), successLocation)
	}

	response, body := getWithoutRedirect(t, successLocation)
	if response.StatusCode != http.StatusOK || body != "signed in" {
		t.Fatalf("success page: status %d, body %q", response.StatusCode, body)
	}
	want := []string{
		fmt.Sprintf("http://127.0.0.1:%d/auth/callback?code=c&state=s", port),
		fmt.Sprintf("http://127.0.0.1:%d/success?id_token=t", port),
	}
	if got := fetcher.fetched(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fetched %q, want %q", got, want)
	}
	waitForPortRelease(t, port)
}

// TestForwarderServesOnlyTheRedirectedFollowUpPath guards that a same-port
// redirect opens exactly its own path: other paths stay local 404s.
func TestForwarderServesOnlyTheRedirectedFollowUpPath(t *testing.T) {
	port := freePort(t)
	fetcher := &pathFetcher{responses: map[string]string{
		"/cb": fmt.Sprintf(
			"HTTP/1.1 302 Found\r\nLocation: http://127.0.0.1:%d/success\r\nContent-Length: 0\r\n\r\n",
			port,
		),
	}}
	forwarder := NewCallbackForwarder(fetcher.fetch)
	if err := forwarder.Forward(t.Context(), LoopbackRedirect{Port: port, Path: "/cb"}); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	getWithoutRedirect(t, fmt.Sprintf("http://127.0.0.1:%d/cb?code=c", port))

	response, _ := getWithoutRedirect(t, fmt.Sprintf("http://127.0.0.1:%d/admin", port))
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unrelated path: status %d, want 404", response.StatusCode)
	}
	if got := fetcher.fetched(); len(got) != 1 {
		t.Fatalf("fetched %q, want only the callback", got)
	}
}

func TestOwnPortFollowUpPath(t *testing.T) {
	for _, tt := range []struct {
		location string
		wantPath string
		wantOK   bool
	}{
		{"http://localhost:1455/success?x=1", "/success", true},
		{"http://127.0.0.1:1455/success", "/success", true},
		{"http://[::1]:1455/", "/", true},
		{"http://localhost:1455", "/", true},
		{"https://localhost:1455/success", "", false},
		{"http://localhost:1456/success", "", false},
		{"http://localhost/success", "", false},
		{"http://example.com:1455/success", "", false},
		{"/success", "", false},
		{"http://[::1", "", false},
	} {
		path, ok := ownPortFollowUpPath(tt.location, 1455)
		if path != tt.wantPath || ok != tt.wantOK {
			t.Errorf("ownPortFollowUpPath(%q) = %q, %v, want %q, %v", tt.location, path, ok, tt.wantPath, tt.wantOK)
		}
	}
}
