package opener

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// defaultForwardLifetime bounds how long a callback port stays open on the host
// when the login is never completed.
// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="the lifetime is a policy bound, not a contract: every test sets CallbackForwarder.lifetime directly, so the default's exact value is unobservable"
const defaultForwardLifetime = 5 * time.Minute

// guestCallbackTimeout bounds the replayed callback. Some agents answer only
// after their token exchange, so this is generous.
// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT,ARITHMETIC_BASE reason="the timeout only bounds a slow guest; tests drive the fetch seam directly, so the value is unobservable"
const guestCallbackTimeout = 2 * time.Minute

// GuestFetcher performs an HTTP GET for url inside the VM and returns the raw
// response as `curl -i` prints it: status line, headers, blank line, body.
// Errors must not include url: it carries the authorization code.
type GuestFetcher func(ctx context.Context, url string) ([]byte, error)

// activeForward is one login's forward on the host: the callback it serves. A
// later login on the same port reuses the listener, updates redirect, bumps
// generation, and signals extend, so a new callback path is served without
// rebinding the port and the lifetime restarts for the new login.
//
// generation names the login that currently owns the listener. A callback
// handler captures it when the browser reaches the callback, so a login that
// already answered cannot close a listener an in-flight earlier callback's
// return would otherwise take down after a new login reused it.
//
// followUpPath is the one extra path the forward serves after the guest
// answered the callback with a redirect to its own port (codex sends the
// browser to /success and only finishes its login once that page is fetched).
//
//exhaustruct:ignore
type activeForward struct {
	redirect     LoopbackRedirect
	generation   uint64
	followUpPath string
	extend       chan struct{}
}

// CallbackForwarder completes loopback OAuth logins started inside the VM.
// When an authorization URL names a callback on a loopback port inside the VM,
// the forwarder listens on the same port on the host's loopback, so the
// browser's final redirect lands on the host. It replays that single callback
// request inside the VM and relays the agent's answer back to the browser.
// This needs neither guest egress nor a published port, and the port can be
// random because it is read from the URL.
//
//exhaustruct:ignore
type CallbackForwarder struct {
	fetchInGuest GuestFetcher
	lifetime     time.Duration
	mutex        sync.Mutex
	active       map[int]*activeForward
}

// NewCallbackForwarder returns a forwarder that replays callbacks with fetch.
func NewCallbackForwarder(fetch GuestFetcher) *CallbackForwarder {
	return &CallbackForwarder{fetchInGuest: fetch, lifetime: defaultForwardLifetime, active: map[int]*activeForward{}}
}

// Forward starts listening for redirect's callback on the host. It returns an
// error when the port cannot be bound on the host's IPv4 loopback, in which
// case the login cannot complete through the host browser. A retried login
// reuses a forward still active for the same port and path; a new login on the
// same port with a different path replaces the earlier forward's target, so the
// new callback path is served, and restarts the lifetime so the new login gets
// the full window. Reuse also takes ownership of the listener, so a callback
// the earlier login is still answering cannot close it. The listener closes
// after the owning login's callback was answered, after the lifetime, or when
// ctx ends.
func (f *CallbackForwarder) Forward(ctx context.Context, redirect LoopbackRedirect) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if existing, ok := f.active[redirect.Port]; ok {
		existing.redirect = redirect
		// gomutants:disable-next-line INCREMENT_DECREMENT reason="generation only has to change on reuse; equality with any previously captured value is already broken whichever way it moves, so the direction is unobservable"
		existing.generation++
		select {
		case existing.extend <- struct{}{}:
		default:
		}
		return nil
	}
	listeners, err := listenLoopback(ctx, redirect.Port)
	if err != nil {
		return err
	}
	done, stop := context.WithCancel(ctx)
	forward := &activeForward{
		redirect: redirect,
		// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT reason="the buffer only avoids dropping an idempotent extend signal while the lifetime goroutine restarts its select; a capacity of 0 still delivers when the goroutine is parked, and a larger buffer changes nothing observable"
		extend: make(chan struct{}, 1),
	}
	server := &http.Server{
		Handler: f.callbackHandler(forward, stop),
		// gomutants:disable-next-line INTEGER_DECREMENT,INTEGER_INCREMENT,ARITHMETIC_BASE reason="a per-connection header timeout, not a contract; no test observes it"
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	f.active[redirect.Port] = forward
	for _, listener := range listeners {
		go func() { _ = server.Serve(listener) }()
	}
	go f.forwardLifetime(ctx, done, forward, server, redirect.Port)
	return nil
}

// forwardLifetime closes a forward after its lifetime, once the callback was
// answered (done fires), or when ctx ends. A reused forward pushes to extend,
// which restarts the lifetime so a login that replaced an abandoned one is not
// cut off by the earlier login's deadline.
func (f *CallbackForwarder) forwardLifetime(
	ctx context.Context,
	done context.Context,
	forward *activeForward,
	server *http.Server,
	port int,
) {
	timer := time.NewTimer(f.lifetime)
	defer timer.Stop()
	for {
		select {
		case <-done.Done():
		case <-timer.C:
		case <-forward.extend:
			timer.Reset(f.lifetime)
			continue
		}
		if f.retire(ctx, done, forward, server, port, timer) {
			return
		}
	}
}

// retire closes an expired or answered forward. It runs under the mutex, so a
// concurrent Forward for the same port either extended this forward first
// (retire then restarts timer and reports false, so the forward lives on) or waits until the
// port is free and binds it afresh — it can never reuse a forward whose
// listeners are already closed.
func (f *CallbackForwarder) retire(
	ctx context.Context,
	done context.Context,
	forward *activeForward,
	server *http.Server,
	port int,
	timer *time.Timer,
) bool {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if done.Err() == nil {
		select {
		case <-forward.extend:
			timer.Reset(f.lifetime)
			return false
		default:
		}
	}
	delete(f.active, port)
	shutdownContext, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownContext)
	return true
}

// listenTCP is a test seam; production binds with a default net.ListenConfig.
var listenTCP = func(ctx context.Context, address string) (net.Listener, error) {
	var listenConfig net.ListenConfig
	return listenConfig.Listen(ctx, "tcp", address)
}

// listenLoopback binds port on 127.0.0.1 and on ::1, because browsers may
// resolve localhost to either. A host without IPv6 loopback is fine; a port
// taken on either address is not.
func listenLoopback(ctx context.Context, port int) ([]net.Listener, error) {
	portText := strconv.Itoa(port)
	ipv4, err := listenTCP(ctx, net.JoinHostPort("127.0.0.1", portText))
	if err != nil {
		return nil, fmt.Errorf("port %d is not free on the host: %w", port, err)
	}
	ipv6, err := listenTCP(ctx, net.JoinHostPort("::1", portText))
	switch {
	case err == nil:
		return []net.Listener{ipv4, ipv6}, nil
	case errors.Is(err, syscall.EADDRINUSE):
		// Another host service owns [::1]:port, and a browser resolving
		// localhost to ::1 would hand it the authorization code.
		_ = ipv4.Close()
		return nil, fmt.Errorf("port %d is not free on the host's IPv6 loopback: %w", port, err)
	default:
		// No IPv6 loopback on this host: browsers then use 127.0.0.1.
		return []net.Listener{ipv4}, nil
	}
}

// callbackHandler replays a GET on the callback path inside the VM and then
// ends the forward. The redirect is read per request so a later login on the
// same port can replace it. Other requests (a favicon, a probe) are answered
// locally and never reach the VM.
func (f *CallbackForwarder) callbackHandler(forward *activeForward, done context.CancelFunc) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		f.mutex.Lock()
		redirect := forward.redirect
		login := forward.generation
		followUpPath := forward.followUpPath
		f.mutex.Unlock()
		if request.Method != http.MethodGet || !servesPath(request.URL.Path, redirect.Path, followUpPath) {
			http.NotFound(writer, request)
			return
		}
		guestURL := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(redirect.Port)) + request.URL.RequestURI()
		fetchContext, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), guestCallbackTimeout)
		defer cancel()
		raw, err := f.fetchInGuest(fetchContext, guestURL)
		nextPath := ""
		if err == nil {
			nextPath, err = relayResponse(writer, raw, redirect.Port)
		}
		if err != nil {
			http.Error(
				writer,
				"agents-sandbox could not pass the login back to the agent in the VM",
				http.StatusBadGateway,
			)
		}
		if nextPath != "" {
			f.awaitFollowUp(forward, login, nextPath)
			return
		}
		f.endForward(forward, login, done)
	})
}

// servesPath reports whether the forward answers path: the callback path, or
// the follow-up path a same-port guest redirect opened.
func servesPath(path, callbackPath, followUpPath string) bool {
	return path == callbackPath || (followUpPath != "" && path == followUpPath)
}

// awaitFollowUp keeps the forward open for the path the guest redirected the
// browser to on its own port. The lifetime still bounds the wait.
func (f *CallbackForwarder) awaitFollowUp(forward *activeForward, login uint64, path string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if forward.generation == login {
		forward.followUpPath = path
	}
}

// endForward closes a forward once the login that owns it answered the browser
// callback. A new login may have reused the listener while this callback was
// still being replayed in the VM; then the forward now belongs to that login,
// and this earlier callback must not close the listener the browser still needs
// for the new login's callback.
func (f *CallbackForwarder) endForward(forward *activeForward, login uint64, done context.CancelFunc) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if forward.generation == login {
		done()
	}
}

// relayedHeaders are the only guest response headers passed to the browser:
// enough to render the agent's confirmation page or follow its redirect.
// Everything else is dropped, notably Set-Cookie and Clear-Site-Data, which
// would let a VM process plant or wipe the cookies of every localhost service
// on the host (cookies are not separated by port).
var relayedHeaders = []string{"Content-Type", "Content-Encoding", "Content-Language", "Location", "Cache-Control"}

// blockedRedirectNotice replaces a redirect the guest aimed at the host's own
// loopback. The guest's Location is dropped: followed, it would reach another
// service listening on the host, which the loopback restriction exists to stop.
const blockedRedirectNotice = "agents-sandbox blocked a redirect to a local address on your computer. " +
	"You can close this window."

// guestPagePolicy is the Content-Security-Policy served with every page relayed
// from the guest. `sandbox` gives the page an opaque origin and no scripts.
// `default-src 'none'` also blocks subresources, so guest HTML cannot make the
// host browser request an address (an <img src>, say) that would reach another
// local service — the redirect check covers the Location header, not resource
// loads.
const guestPagePolicy = "sandbox; default-src 'none'"

// isSafeRedirectLocation reports whether the guest's redirect target is one the
// host browser may follow. Only an absolute http(s) URL to a host other than
// the host's own loopback is allowed: a loopback target would reach another
// local service, and a relative or non-web target has no safe destination here.
func isSafeRedirectLocation(location string) bool {
	redirect, err := url.Parse(location)
	if err != nil || !isWebScheme(redirect.Scheme) || redirect.Hostname() == "" {
		return false
	}
	host := redirect.Hostname()
	return !isLoopbackHost(host) && !isAmbiguousNumericHost(host)
}

// ownPortFollowUpPath reports whether location redirects the browser back to
// the forward's own listener (plain http, a loopback host, ownPort) and returns
// the path it requests. That redirect reaches no other host service: the
// forwarder itself answers it by replaying the request in the guest.
func ownPortFollowUpPath(location string, ownPort int) (string, bool) {
	redirect, err := url.Parse(location)
	if err != nil || !strings.EqualFold(redirect.Scheme, "http") || !isLoopbackHost(redirect.Hostname()) {
		return "", false
	}
	if redirect.Port() != strconv.Itoa(ownPort) {
		return "", false
	}
	if redirect.Path == "" {
		return "/", true
	}
	return redirect.Path, true
}

// relayResponse writes the guest's raw HTTP response to writer. The page is
// served with a sandbox content security policy that gives it an opaque origin,
// blocks scripts, and blocks subresources, so guest-supplied HTML can neither
// read nor write anything that belongs to localhost on the host and cannot make
// the browser request a host-local address. A redirect to a loopback or non-web
// target is replaced with a local notice instead of being relayed, except a
// redirect back to ownPort: it returns that redirect's path so the forward
// serves the follow-up request.
func relayResponse(writer http.ResponseWriter, raw []byte, ownPort int) (string, error) {
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	if err != nil {
		return "", fmt.Errorf("parse guest response: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("read guest response: %w", err)
	}
	followUpPath := ""
	for _, location := range response.Header.Values("Location") {
		if path, ok := ownPortFollowUpPath(location, ownPort); ok {
			followUpPath = path
			continue
		}
		if !isSafeRedirectLocation(location) {
			writeBlockedRedirect(writer)
			return "", nil
		}
	}
	for _, name := range relayedHeaders {
		for _, value := range response.Header.Values(name) {
			writer.Header().Add(name, value)
		}
	}
	writer.Header().Set("Content-Security-Policy", guestPagePolicy)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(body)
	return followUpPath, nil
}

// writeBlockedRedirect answers a guest redirect the host refuses to follow.
func writeBlockedRedirect(writer http.ResponseWriter) {
	// gomutants:disable-next-line STATEMENT_REMOVE reason="the notice is plain text, so net/http's sniffing sets the identical Content-Type when this line is removed"
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Security-Policy", guestPagePolicy)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	// gomutants:disable-next-line STATEMENT_REMOVE reason="net/http writes 200 on the first body write, so the explicit status is unobservable"
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, blockedRedirectNotice)
}
