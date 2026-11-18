package channel

import (
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

	observer, err := StartObservation("127.0.0.1", base)
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
	if overlay.Proxy.Address == "" {
		t.Error("overlay proxy address is empty, want the bound proxy address")
	} else if _, port, ok := strings.Cut(overlay.Proxy.Address, ":"); !ok || port == "" || port == "0" {
		t.Errorf("overlay proxy address = %q, want a bound host:port", overlay.Proxy.Address)
	}

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
// no-op that returns the same artefact.
func TestObserverStopIsIdempotent(t *testing.T) {
	observer, err := StartObservation("127.0.0.1", network.Policy{Profiles: []network.Profile{network.ProfileNone}})
	if err != nil {
		t.Fatalf("StartObservation: %v", err)
	}

	first := observer.Stop()
	second := observer.Stop()

	if len(first.Destinations) != 0 {
		t.Errorf("Stop returned %d destinations, want none", len(first.Destinations))
	}
	if len(second.Destinations) != 0 || len(second.Attribution) != 0 {
		t.Errorf("second Stop returned %+v, want an empty artefact", second)
	}
}
