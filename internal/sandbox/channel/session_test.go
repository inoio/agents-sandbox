package channel

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/network"
)

// StartObservation binds a proxy and hands back the Observer a run consults. The
// overlay it returns is what makes the session observable: interception forced
// on, the sandbox pointed at our proxy, and the channel CA projected as a path
// the runtime trusts upstream (R1: UpstreamCACerts takes paths, not PEM).
func TestStartObservation(t *testing.T) {
	base := network.Policy{Profiles: []network.Profile{network.ProfileNone}}

	const bindHost = "127.0.0.1"
	observer, err := StartObservation(bindHost, base)
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

// Stop tears the proxy down and returns the artefact, and a second call is a
// no-op that returns the same artefact. A recorded request drives a non-empty
// artefact, so the identical-return assertion is not vacuous.
func TestObserverStopIsIdempotent(t *testing.T) {
	observer, err := StartObservation("127.0.0.1", network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("StartObservation: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxyURL, err := url.Parse("http://" + observer.Overlay().Proxy.Address)
	if err != nil {
		t.Fatalf("parse proxy address: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(upstream.URL + "/v1/chat")
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	resp.Body.Close()

	first := observer.Stop()
	if len(first.Destinations) == 0 {
		t.Fatalf("Stop returned no destinations, want the recorded request: %+v", first)
	}
	second := observer.Stop()
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second Stop returned a different artefact:\n first %+v\nsecond %+v", first, second)
	}
}

// StartObservation propagates a Start failure to the caller instead of returning
// an Observer around a proxy that never bound.
func TestStartObservationPropagatesStartFailure(t *testing.T) {
	observer, err := StartObservation("[::1]", network.Policy{Profiles: []network.Profile{network.ProfileNone}})
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
