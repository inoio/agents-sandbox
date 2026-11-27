package journeys

import (
	"net/url"
	"os"
	"strings"
)

// stubOriginEnv overrides the advertised destination origin (scheme://host) the
// provider config points the agent at. The stub always binds host loopback on an
// ephemeral port; only the *advertised* origin changes. Unset (or invalid) uses
// stubOriginDefault.
const stubOriginEnv = "AGENTS_SANDBOX_JOURNEYS_STUB_ORIGIN"

// stubOriginDefault is the advertised destination a run uses with no override:
// microsandbox's host gateway name, which a VM reaches under the `host` network
// profile and which lands on the host's loopback listener. Host-gateway traffic
// does not pass the learn-mode observation proxy, so a turn to it completes but
// is not observed.
const stubOriginDefault = "http://" + stubGatewayHost

// stubGatewayHost is microsandbox's name for the host machine inside a VM.
const stubGatewayHost = "host.microsandbox.internal"

// stubOrigin returns the advertised destination origin, from stubOriginEnv when
// it names a usable http(s) origin, else stubOriginDefault.
func stubOrigin() string {
	//testenv:allow the stub provider reads its own advertised-route override
	return stubOriginFromValue(os.Getenv(stubOriginEnv))
}

// stubAdvertisedHost is the host part of the advertised origin the stub points
// agents at, so a journey scopes its credential assertion to the route the run
// actually used (the default, or a host accept's override). It reads the same
// override stubOrigin does, and an origin with no host yields "".
func stubAdvertisedHost() string {
	parsed, err := url.Parse(stubOrigin())
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

// stubOriginFromValue parses an advertised-origin override: an http(s) URL with
// a host yields scheme://hostname (its port, userinfo, and path are dropped,
// because the listener's own port is always used); anything else yields
// stubOriginDefault. An IPv6 literal host is re-bracketed, so `http://[::1]`
// round-trips rather than re-emitting the invalid `http://::1`. Kept pure so it
// is testable without the host.
func stubOriginFromValue(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return stubOriginDefault
	}
	switch parsed.Scheme {
	case "http", "https":
		return parsed.Scheme + "://" + bracketedHost(parsed.Hostname())
	default:
		return stubOriginDefault
	}
}

// bracketedHost wraps an IPv6 literal host in brackets, so an origin URL is
// valid; every other host is returned unchanged. url.Hostname strips the
// brackets, which must be restored when the host is emitted in a URL.
func bracketedHost(hostname string) string {
	if strings.Contains(hostname, ":") {
		return "[" + hostname + "]"
	}
	return hostname
}
