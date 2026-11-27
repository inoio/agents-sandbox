package journeys

import (
	"testing"
)

// TestStubOriginFromValue pins the advertised-route override: a valid
// http(s) origin is kept (port and any path dropped), and anything else falls
// back to the documented placeholder, so an unset or malformed override can
// never expose the stub on a surprising scheme or host.
func TestStubOriginFromValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "unset", in: "", want: stubOriginDefault},
		{name: "blank", in: "   ", want: stubOriginDefault},
		{name: "loopback http", in: "http://127.0.0.1", want: "http://127.0.0.1"},
		{name: "loopback https", in: "https://127.0.0.1", want: "https://127.0.0.1"},
		{name: "host http", in: "http://stub.internal", want: "http://stub.internal"},
		{name: "port dropped", in: "http://127.0.0.1:8080", want: "http://127.0.0.1"},
		{name: "userinfo and path dropped", in: "https://user:pass@host/path", want: "https://host"},
		{name: "ipv6 literal", in: "http://[::1]", want: "http://[::1]"},
		{name: "ipv6 literal with port", in: "https://[2001:db8::1]:8443", want: "https://[2001:db8::1]"},
		{name: "unsupported scheme", in: "ftp://host", want: stubOriginDefault},
		{name: "no host", in: "https://", want: stubOriginDefault},
		{name: "not a url", in: "not a url", want: stubOriginDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stubOriginFromValue(tc.in); got != tc.want {
				t.Errorf("stubOriginFromValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStubAdvertisedHostTracksTheOrigin pins that the host a journey scopes its
// credential assertion to is the advertised origin's host (the default, or a
// host accept's override), so the asserted host and env.secret.yaml's scoping
// host cannot disagree.
func TestStubAdvertisedHostTracksTheOrigin(t *testing.T) {
	t.Setenv(stubOriginEnv, "https://stub.example:8443/path")
	if got, want := stubAdvertisedHost(), "stub.example"; got != want {
		t.Errorf("stubAdvertisedHost() = %q, want %q", got, want)
	}
}
