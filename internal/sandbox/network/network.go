package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
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
	DefaultProfile Profile = ProfileNone
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
// egress-deny, dns-servers, tls) directly into this struct.
type Policy struct {
	Profile     Profile    `mapstructure:"profile"`
	EgressAllow []string   `mapstructure:"egress-allow"`
	EgressDeny  []string   `mapstructure:"egress-deny"`
	DNSServers  []string   `mapstructure:"dns-servers"`
	TLS         *TLSConfig `mapstructure:"tls"`
}

// Empty reports whether the policy is unset (zero value).
func (p Policy) Empty() bool {
	return p.Profile == "" && len(p.EgressAllow) == 0 && len(p.EgressDeny) == 0 && len(p.DNSServers) == 0 &&
		p.TLS == nil
}

// TLSConfig configures the transparent HTTPS inspection proxy. The CA cert and
// key paths plus the fingerprint are launcher-managed (resolved from the
// persisted tlsca keypair at VM-preparation time); the remaining fields are
// user-configurable via the `tls:` network block.
type TLSConfig struct {
	// Bypass is a list of domain patterns (supports "*.suffix") to skip MITM.
	Bypass []string `mapstructure:"bypass"`
	// InterceptedPorts lists ports on which TLS is intercepted (default [443]).
	InterceptedPorts []uint16 `mapstructure:"intercepted-ports"`
	// BlockQUIC blocks QUIC on intercepted ports to force TLS fallback.
	BlockQUIC *bool `mapstructure:"block-quic"`
	// VerifyUpstream verifies upstream TLS certificates (default true).
	VerifyUpstream *bool `mapstructure:"verify-upstream"`
	// CACert and CAKey are the host paths to the interception CA, resolved from
	// the persisted tlsca keypair. They are launcher-set, not config-backed.
	CACert string
	CAKey  string
	// CAFingerprint is the SHA-256 of the CA certificate, folded into the
	// policy fingerprint so a CA rotation recreates the VM.
	CAFingerprint string
}

// Effective applies the secure default profile when no profile was selected.
func (p Policy) Effective() Policy {
	if p.Profile == "" {
		p.Profile = DefaultProfile
	}
	return p
}

// Fingerprint returns a stable SHA-256 hex digest of the policy, for detecting
// changes across runs. It hashes the profile and the sorted allow/deny/dns
// lists plus the TLS interception settings, independent of the microsandbox
// SDK's canonical NetworkConfig shape.
func (p Policy) Fingerprint() string {
	p = p.Effective()
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
	if p.TLS != nil {
		lines = append(lines, tlsFingerprintLines(p.TLS)...)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// tlsFingerprintLines renders the TLS interception settings as sorted
// fingerprint lines. The CA identity enters through the launcher-resolved
// fingerprint rather than the (machine-specific) cert/key paths.
func tlsFingerprintLines(t *TLSConfig) []string {
	lines := []string{"tls=on"}
	if t.CAFingerprint != "" {
		lines = append(lines, "tls-ca="+t.CAFingerprint)
	}
	for _, b := range t.Bypass {
		lines = append(lines, "tls-bypass="+b)
	}
	for _, p := range t.InterceptedPorts {
		lines = append(lines, "tls-port="+strconv.FormatUint(uint64(p), 10))
	}
	if t.BlockQUIC != nil {
		lines = append(lines, "tls-block-quic="+strconv.FormatBool(*t.BlockQUIC))
	}
	if t.VerifyUpstream != nil {
		lines = append(lines, "tls-verify-upstream="+strconv.FormatBool(*t.VerifyUpstream))
	}
	sort.Strings(lines)
	return lines
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
// egress-allow/egress-deny lists apply. It is not an airgap. A policy with no
// profile, including one with only DNSServers set, defaults to none.
func (p Policy) Config() (*msbSdk.NetworkConfig, error) {
	profile := p.Effective().Profile
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
	if p.TLS != nil {
		cfg.TLS = &msbSdk.TLSConfig{
			Bypass:                append([]string(nil), p.TLS.Bypass...),
			VerifyUpstream:        p.TLS.VerifyUpstream,
			InterceptedPorts:      append([]uint16(nil), p.TLS.InterceptedPorts...),
			BlockQUIC:             p.TLS.BlockQUIC,
			CACert:                p.TLS.CACert,
			CAKey:                 p.TLS.CAKey,
			UpstreamCACerts:       nil,
			ScopedUpstreamCACerts: nil,
			ScopedVerifyUpstream:  nil,
		}
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
