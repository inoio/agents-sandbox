package channel

import (
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// StartObservation binds a proxy and hands back the Observer a run consults. The
// overlay it returns is what makes the session observable: interception forced
// on, the sandbox pointed at our proxy, and the channel CA projected as a path
// the runtime trusts upstream (R1: UpstreamCACerts takes paths, not PEM).
func TestStartObservation(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	const bindHost = "127.0.0.1"
	observer, err := StartObservation(bindHost, base, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("StartObservation: %v", err)
	}
	defer observer.Stop()

	overlay := observer.Overlay()

	if overlay.InterceptTLS != network.InterceptOn {
		t.Errorf("overlay InterceptTLS = %q, want %q", overlay.InterceptTLS, network.InterceptOn)
	}
	if overlay.Proxy.Protocol != network.ProxyHTTPConnect {
		t.Errorf("overlay proxy protocol = %q, want %q", overlay.Proxy.Protocol, network.ProxyHTTPConnect)
	}
	assertProxyAddress(t, overlay.Proxy.Address, bindHost)

	certs := overlay.UpstreamCACerts()
	if len(certs) == 0 {
		t.Fatal("overlay carries no upstream CA paths, want the channel CA file")
	}
	for _, path := range certs {
		if !strings.Contains(path, ".pem") {
			t.Errorf("UpstreamCACerts entry %q does not look like a CA bundle path", path)
		}
	}

	if overlay.Fingerprint() == base.Fingerprint() {
		t.Error("the overlay must change the fingerprint, so the VM is recreated for a learning session")
	}
}

// StartObservation forwards network.observe.skip-upstream-verify to the proxy's
// origin transport: with it set, a self-signed upstream round-trips through the
// channel; without it, the same upstream is rejected.
func TestStartObservationForwardsSkipUpstreamVerify(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cases := []struct {
		name               string
		skipUpstreamVerify bool
		wantServed         bool
	}{
		{name: "verify by default", skipUpstreamVerify: false, wantServed: false},
		{name: "skip when configured", skipUpstreamVerify: true, wantServed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observer, err := StartObservation("127.0.0.1", network.Policy{
				Profiles: []network.Profile{network.ProfileNone},
				Observe:  network.ObserveConfig{SkipUpstreamVerify: tc.skipUpstreamVerify},
			}, time.Unix(1, 0))
			if err != nil {
				t.Fatalf("StartObservation: %v", err)
			}
			defer observer.Stop()

			served := requestThroughObserver(t, observer, upstream.URL)
			if served != tc.wantServed {
				t.Errorf("upstream served = %v, want %v", served, tc.wantServed)
			}
		})
	}
}

// requestThroughObserver issues one GET for target through the observer's proxy
// with a client that trusts the channel CA, and reports whether the upstream
// answered with 200.
func requestThroughObserver(t *testing.T, obs Observer, target string) bool {
	t.Helper()

	concrete, ok := obs.(observer)
	if !ok {
		t.Fatalf("obs = %T, want the concrete observer", obs)
	}
	resp, err := proxyClient(t, concrete.handle).Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Stop tears the proxy down and returns the artefact, and a second call is a
// no-op that returns the same artefact. The default-verify origin transport
// rejects the self-signed upstream, so the artefact carries a proxy log; the
// identical-return assertion therefore covers the log path, which a plain-HTTP
// request (no warning) would leave untested.
func TestObserverStopIsIdempotent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	observer, err := StartObservation(
		"127.0.0.1",
		network.Policy{Profiles: []network.Profile{network.ProfileNone}},
		time.Unix(1, 0),
	)
	if err != nil {
		t.Fatalf("StartObservation: %v", err)
	}

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	if requestThroughObserver(t, observer, upstream.URL) {
		t.Fatal("the self-signed upstream was served, want the default verification to reject it")
	}

	first := observer.Stop()
	if len(first.Destinations) == 0 {
		t.Fatalf("Stop returned no destinations, want the recorded request: %+v", first)
	}
	if first.ProxyLog == "" || first.ProxyWarnings == 0 {
		t.Fatalf("Stop returned no proxy log, want the rejected upstream's warning: %+v", first)
	}
	second := observer.Stop()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second Stop returned a different artefact:\n first %+v\nsecond %+v", first, second)
	}
	if second.ProxyLog == "" {
		t.Errorf("second Stop cleared ProxyLog: %+v", second)
	}
}

// StartObservation propagates a Start failure to the caller instead of returning
// an Observer around a proxy that never bound.
func TestStartObservationPropagatesStartFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	observer, err := StartObservation(
		"[::1]",
		network.Policy{Profiles: []network.Profile{network.ProfileNone}},
		time.Unix(1, 0),
	)
	if err == nil {
		observer.Stop()
		t.Fatal("StartObservation with an unparseable bind host returned no error")
	}
}

// assertProxyAddress asserts the overlay's proxy address is a bound host:port on
// the expected bind host.
func assertProxyAddress(t *testing.T, address, bindHost string) {
	t.Helper()

	if address == "" {
		t.Error("overlay proxy address is empty, want the bound proxy address")
		return
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Errorf("split overlay proxy address %q: %v", address, err)
		return
	}
	if host != bindHost {
		t.Errorf("overlay proxy host = %q, want the bind host %q", host, bindHost)
	}
	if port == "" || port == "0" {
		t.Errorf("overlay proxy address = %q, want a bound port", address)
	}
}
