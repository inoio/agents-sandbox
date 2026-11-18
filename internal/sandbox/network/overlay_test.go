package network

import (
	"slices"
	"testing"
)

// The observation overlay is in-memory only: it carries the paths to the CA
// bundles the runtime must trust on top of a policy, so a learning session's
// captured traffic can be verified. It is not a config key, and it must reach
// the fingerprint (the VM is recreated when it changes) and the emitted spec.
func TestObservationOverlayChangesFingerprint(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfileNone}}
	ov := base.WithUpstreamCACerts("/ca.pem")
	ov.Proxy = Proxy{Protocol: ProxyHTTPConnect, Address: "127.0.0.1:1234"}
	if base.Fingerprint() == ov.Fingerprint() {
		t.Fatal("the overlay must change the fingerprint")
	}
}

// The upstream-CA fingerprint line is order-independent like every other list
// field: the same set of CA paths fingerprints alike whatever order the overlay
// carried them in.
func TestUpstreamCACertsFingerprintIgnoresOrder(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfileNone}}
	first := base.WithUpstreamCACerts("/a.pem", "/b.pem")
	second := base.WithUpstreamCACerts("/b.pem", "/a.pem")
	if first.Fingerprint() != second.Fingerprint() {
		t.Errorf("fingerprints differ for one CA set in a different order: %q vs %q",
			first.Fingerprint(), second.Fingerprint())
	}
}

// WithUpstreamCACerts forces interception on: the observation needs the
// runtime to intercept, so an overlay on an `off` (or auto) policy must read as
// `on` afterwards.
func TestWithUpstreamCACertsForcesInterceptionOn(t *testing.T) {
	for _, mode := range []InterceptMode{InterceptAuto, InterceptOff, InterceptOn, ""} {
		t.Run(string(mode), func(t *testing.T) {
			got := Policy{InterceptTLS: mode}.WithUpstreamCACerts("/ca.pem")
			if got.InterceptTLS != InterceptOn {
				t.Errorf("InterceptTLS = %q, want %q", got.InterceptTLS, InterceptOn)
			}
		})
	}
}

// The accessor returns a copy: mutating the returned slice must not change the
// policy's own paths.
func TestUpstreamCACertsReturnsACopy(t *testing.T) {
	policy := Policy{}.WithUpstreamCACerts("/ca.pem", "/other.pem")

	got := policy.UpstreamCACerts()
	if want := []string{"/ca.pem", "/other.pem"}; !slices.Equal(got, want) {
		t.Fatalf("UpstreamCACerts() = %v, want %v", got, want)
	}
	got[0] = "/mutated.pem"
	if again := policy.UpstreamCACerts(); again[0] != "/ca.pem" {
		t.Errorf("mutating the returned slice changed the policy: %v", again)
	}
}

// A zero policy carries no paths, so the accessor is safe to call on one.
func TestUpstreamCACertsUnset(t *testing.T) {
	if got := (Policy{}).UpstreamCACerts(); got != nil {
		t.Errorf("UpstreamCACerts() = %v, want nil", got)
	}
}

// The overlay is passed through to the emitted spec so the runtime trusts the
// channel's CA.
func TestSpecCarriesUpstreamCACerts(t *testing.T) {
	spec, err := Policy{Profiles: []Profile{ProfileNone}}.WithUpstreamCACerts("/ca.pem", "/other.pem").Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if want := []string{"/ca.pem", "/other.pem"}; !slices.Equal(spec.UpstreamCACerts, want) {
		t.Errorf("spec.UpstreamCACerts = %v, want %v", spec.UpstreamCACerts, want)
	}
}

// The overlay keeps Empty() honest: a policy carrying only the paths is not the
// zero value, consistent with every other field.
func TestPolicyWithUpstreamCACertsIsNotEmpty(t *testing.T) {
	if (Policy{}).WithUpstreamCACerts("/ca.pem").Empty() {
		t.Error("a policy carrying upstream CA paths should not be empty")
	}
}
