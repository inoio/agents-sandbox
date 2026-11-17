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

// NetworkSpec is the launcher's network policy input. Profiles holds only the
// composable profiles; the launcher-only `none` profile yields no profile and
// therefore no gateway-DNS rule.
//
// InterceptTLS selects the sandbox's TLS-interception mode, TLSBypass lists
// hosts microsandbox must not intercept, and DisableStrict drops strict
// hostname enforcement.
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
}
