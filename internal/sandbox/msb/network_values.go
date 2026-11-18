package msb

// NetworkProfile is one composable launcher network profile.
type NetworkProfile string

const (
	NetworkProfilePublic  NetworkProfile = "public"
	NetworkProfilePrivate NetworkProfile = "private"
	NetworkProfileHost    NetworkProfile = "host"
)

// InterceptMode mirrors the launcher's TLS-interception mode. It is defined
// locally so no network-policy type from the SDK's package crosses the seam.
type InterceptMode string

const (
	// InterceptAuto lets microsandbox decide, intercepting only when it must,
	// e.g. to substitute a secret placeholder. The empty value means auto.
	InterceptAuto InterceptMode = "auto"
	InterceptOn   InterceptMode = "on"
	InterceptOff  InterceptMode = "off"
)

// ProxyProtocol mirrors the launcher's outbound-proxy protocol. It is defined
// locally so no network-policy type from the SDK's package crosses the seam.
type ProxyProtocol string

const (
	ProxyHTTPConnect ProxyProtocol = "http-connect"
	ProxySOCKS5      ProxyProtocol = "socks5"
	ProxySOCKS4      ProxyProtocol = "socks4"
)

// ProxyProtocolsHint names the supported outbound-proxy protocols for the
// protocol-error message. It is the single source shared by the launcher's
// parser (internal/sandbox/network) and this seam's translator, so the two
// never drift.
const ProxyProtocolsHint = "http-connect, socks5, or socks4"

// ProxySpec is the launcher's outbound-proxy input. The zero value means no
// proxy. Credentials are optional and carried only by http-connect and socks5.
type ProxySpec struct {
	Protocol    ProxyProtocol
	Address     string
	Username    string
	PasswordEnv string
}

// Empty reports whether no proxy is configured (zero value).
func (p ProxySpec) Empty() bool {
	return p.Protocol == "" && p.Address == "" && p.Username == "" && p.PasswordEnv == ""
}

// NetworkSpec is the launcher's network policy input. Profiles holds only the
// composable profiles; the launcher-only `none` profile yields no profile and
// therefore no gateway-DNS rule.
//
// InterceptTLS selects the sandbox's TLS-interception mode, TLSBypass lists
// hosts microsandbox must not intercept, and DisableStrict drops strict
// hostname enforcement.
//
// Proxy is the outbound proxy the runtime dials host-side; the zero value means
// no proxy.
//
//exhaustruct:ignore
type NetworkSpec struct {
	Profiles      []NetworkProfile
	EgressAllow   []string
	EgressDeny    []string
	DNSServers    []string // normalized
	InterceptTLS  InterceptMode
	TLSBypass     []string
	DisableStrict bool
	Proxy         ProxySpec
}
