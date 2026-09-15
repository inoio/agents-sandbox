package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Profile is a launcher network egress profile.
type Profile string

const (
	ProfilePublic  Profile = "public"
	ProfilePrivate Profile = "private"
	ProfileHost    Profile = "host"
	ProfileNone    Profile = "none"
)

func (p Profile) String() string { return string(p) }

// ParseProfile parses a profile string, erroring on unknown values.
func ParseProfile(s string) (Profile, error) {
	switch Profile(s) {
	case ProfilePublic, ProfilePrivate, ProfileHost, ProfileNone:
		return Profile(s), nil
	default:
		return "", fmt.Errorf("unknown network profile %q (want public, private, host, or none)", s)
	}
}

// Policy is the resolved launcher network policy. The mapstructure tags let
// viper decode the nested `network:` YAML block (profile, egress-allow,
// egress-deny, dns-servers) directly into this struct.
type Policy struct {
	Profile     Profile  `mapstructure:"profile"`
	EgressAllow []string `mapstructure:"egress-allow"`
	EgressDeny  []string `mapstructure:"egress-deny"`
	DNSServers  []string `mapstructure:"dns-servers"`
}

// Empty reports whether the policy is unset (zero value), meaning the caller
// should fall back to the default public profile.
func (p Policy) Empty() bool { return p.Profile == "" && len(p.DNSServers) == 0 }

// Fingerprint returns a stable SHA-256 hex digest of the policy, for detecting
// changes across runs. It hashes the profile and the sorted allow/deny/dns
// lists, independent of the microsandbox SDK's canonical NetworkConfig shape.
func (p Policy) Fingerprint() string {
	var lines []string
	lines = append(lines, "profile="+string(p.Profile))
	for _, d := range p.DNSServers {
		lines = append(lines, "dns="+d)
	}
	for _, d := range p.EgressAllow {
		lines = append(lines, "allow="+d)
	}
	for _, d := range p.EgressDeny {
		lines = append(lines, "deny="+d)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// NormalizeDNSServers validates and normalizes DNS upstream resolvers. Bare IPs
// (IPv4 or IPv6) get ":53" appended; "host:port" / "ip:port" entries are kept
// as-is. Empty entries, a host without a port, and garbage are rejected.
func NormalizeDNSServers(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, errors.New("empty DNS server entry")
		}
		if net.ParseIP(s) != nil {
			out = append(out, net.JoinHostPort(s, "53"))
			continue
		}
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid DNS server %q: must be an IP (e.g. 1.1.1.1) or host:port (e.g. 1.1.1.1:53)",
				s,
			)
		}
		if host == "" {
			return nil, fmt.Errorf("invalid DNS server %q: missing host", s)
		}
		if port == "" {
			return nil, fmt.Errorf("invalid DNS server %q: missing port", s)
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out, nil
}

// Config converts the policy into a microsandbox NetworkConfig.
//
// The `none` profile is an allowlist-only policy: egress is deny-by-default,
// ingress is allowed, and only the gateway-DNS rule plus the explicit
// egress-allow/egress-deny lists apply. It is not an airgap. A policy with only
// DNSServers set (no profile) defaults to the public profile.
func (p Policy) Config() (*msbSdk.NetworkConfig, error) {
	profile := p.Profile
	if profile == "" {
		profile = ProfilePublic
	}
	var cfg *msbSdk.NetworkConfig
	var err error
	if profile == ProfileNone {
		// Deny-by-default egress, allow ingress, with no profile rules. The
		// gateway-DNS rule is added manually so named allow-listed hosts can
		// resolve.
		cfg, err = msbSdk.NetworkPolicy.FromProfilesChecked()
	} else {
		cfg, err = msbSdk.NetworkPolicy.FromProfilesChecked(msbSdk.NetworkProfile(profile))
	}
	if err != nil {
		return nil, err
	}
	if profile == ProfileNone {
		cfg.Rules = append(cfg.Rules, msbSdk.Rule.AllowDNS())
	}
	if len(p.DNSServers) > 0 {
		nameservers, err := NormalizeDNSServers(p.DNSServers)
		if err != nil {
			return nil, err
		}
		cfg.DNS = &msbSdk.DNSConfig{ //nolint:exhaustruct // rebind/query-timeout settings are out of scope
			Nameservers: nameservers,
		}
	}
	for _, d := range dedupe(p.EgressDeny) {
		cfg.Rules = append(cfg.Rules, egressRule(msbSdk.PolicyActionDeny, d))
	}
	for _, d := range dedupe(p.EgressAllow) {
		cfg.Rules = append(cfg.Rules, egressRule(msbSdk.PolicyActionAllow, d))
	}
	return cfg, nil
}

func egressRule(action msbSdk.PolicyAction, destination string) msbSdk.PolicyRule {
	var r msbSdk.PolicyRule
	r.Action = action
	r.Direction = msbSdk.PolicyDirectionEgress
	r.Destination = destination
	return r
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
