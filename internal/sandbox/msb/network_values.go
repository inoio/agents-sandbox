package msb

// NetworkProfile is one composable launcher network profile.
type NetworkProfile string

const (
	NetworkProfilePublic  NetworkProfile = "public"
	NetworkProfilePrivate NetworkProfile = "private"
	NetworkProfileHost    NetworkProfile = "host"
)

// NetworkSpec is the launcher's network policy input. Profiles holds only the
// composable profiles; the launcher-only `none` profile yields no profile and
// therefore no gateway-DNS rule.
//
//exhaustruct:ignore
type NetworkSpec struct {
	Profiles    []NetworkProfile
	EgressAllow []string
	EgressDeny  []string
	DNSServers  []string // normalized
}
