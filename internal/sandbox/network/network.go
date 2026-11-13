package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// Profile is a launcher network egress profile.
type Profile string

const (
	ProfilePublic  Profile = "public"
	ProfilePrivate Profile = "private"
	ProfileHost    Profile = "host"
	ProfileNone    Profile = "none"
	DefaultProfile Profile = ProfilePublic
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

// ParseProfiles parses a list of profile names, erroring on an unknown value or
// when `none` is combined with another profile.
func ParseProfiles(in []string) ([]Profile, error) {
	profiles := make([]Profile, 0, len(in))
	for _, s := range in {
		p, err := ParseProfile(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	// Dedupe before checking so repeating the same profile (e.g. `--network
	// none --network none`) is not mistaken for combining `none` with another.
	profiles = dedupeProfiles(profiles)
	if err := checkProfileCombination(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// checkProfileCombination rejects mixing the terminal `none` profile with any
// other, mirroring microsandbox's own treatment of none/all as whole-policy
// values rather than composable profiles.
func checkProfileCombination(profiles []Profile) error {
	if len(profiles) <= 1 {
		return nil
	}
	if slices.Contains(profiles, ProfileNone) {
		return errors.New(`network profile "none" cannot be combined with other profiles`)
	}
	return nil
}

// Policy is the resolved launcher network policy. The mapstructure tags let
// viper decode the nested `network:` YAML block (profile, egress-allow,
// egress-deny, dns-servers) directly into this struct. The `profile` key
// accepts a single name, a comma-separated string, or a YAML list; viperconfig's
// string-to-slice decode hook normalizes the scalar forms into Profiles.
type Policy struct {
	Profiles []Profile `mapstructure:"profile"`
	// EgressAllow and EgressDeny have no env var and no CLI flag: they are set
	// in the config file only (see docs/configuration/networking.md). The
	// env:"-" tag keeps viperconfig's derived env list from binding them.
	EgressAllow []string `mapstructure:"egress-allow" env:"-"`
	EgressDeny  []string `mapstructure:"egress-deny"  env:"-"`
	DNSServers  []string `mapstructure:"dns-servers"`
}

// Empty reports whether the policy is unset (zero value).
func (p Policy) Empty() bool {
	return len(p.Profiles) == 0 && len(p.EgressAllow) == 0 && len(p.EgressDeny) == 0 && len(p.DNSServers) == 0
}

// Effective applies the default profile when no profile was selected.
func (p Policy) Effective() Policy {
	if len(p.Profiles) == 0 {
		p.Profiles = []Profile{DefaultProfile}
	}
	return p
}

// Fingerprint returns a stable SHA-256 hex digest of the policy, for detecting
// changes across runs. It hashes the profile and the sorted allow/deny/dns
// lists, independent of the microsandbox SDK's canonical NetworkConfig shape.
//
// A deny-bearing policy that composes a profile also contributes a rule-order
// marker. The marker is not policy content: it records that the policy is
// emitted with egress-deny ahead of the profile allow rules. A VM created before
// that emission order holds the older digest in network_state.hash, so the
// marker makes the fingerprint differ and the VM is recreated once to pick up
// the corrected order; the marker reaches state after the recreate, so it fires
// only once. Policies whose emitted rules are identical either way (no deny
// entries, or the terminal `none` profile) get no marker and keep their digest
// stable.
//
// A `none` policy likewise contributes a marker for omitting the gateway-DNS
// rule, so a VM created with the rule is recreated once without it.
func (p Policy) Fingerprint() string {
	p = p.Effective()
	var lines []string
	for _, prof := range dedupeProfiles(p.Profiles) {
		lines = append(lines, "profile="+prof.String())
	}
	for _, d := range p.DNSServers {
		lines = append(lines, "dns="+d)
	}
	for _, d := range p.EgressAllow {
		lines = append(lines, "allow="+d)
	}
	for _, d := range p.EgressDeny {
		lines = append(lines, "deny="+d)
	}
	if p.denyPrecedesProfileAllow() {
		lines = append(lines, "rule-order=deny-before-profile")
	}
	if !p.allowsGatewayDNS() {
		lines = append(lines, "gateway-dns=omitted")
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Describe returns the effective policy as display lines named after the config
// keys: the deduplicated profiles, then the egress-allow, egress-deny, and DNS
// server entries, each group sorted. Unlike Fingerprint it carries no emission
// markers, only what the user configured.
func (p Policy) Describe() []string {
	p = p.Effective()
	var lines []string
	lines = append(lines, prefixedSorted("profile: ", profileNames(dedupeProfiles(p.Profiles)))...)
	lines = append(lines, prefixedSorted("egress-allow: ", p.EgressAllow)...)
	lines = append(lines, prefixedSorted("egress-deny: ", p.EgressDeny)...)
	lines = append(lines, prefixedSorted("dns-server: ", p.DNSServers)...)
	return lines
}

func profileNames(profiles []Profile) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.String())
	}
	return names
}

func prefixedSorted(prefix string, values []string) []string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, prefix+value)
	}
	sort.Strings(lines)
	return lines
}

// denyPrecedesProfileAllow reports whether the policy emits egress-deny rules
// ahead of the profile allow rules: it has at least one deny entry and at least
// one profile that contributes allow rules. When either is absent the emitted
// rules are identical either way, so the policy needs no rule-order marker.
func (p Policy) denyPrecedesProfileAllow() bool {
	if len(p.EgressDeny) == 0 {
		return false
	}
	for _, prof := range dedupeProfiles(p.Profiles) {
		if prof != ProfileNone {
			return true
		}
	}
	return false
}

// allowsGatewayDNS reports whether the policy emits the gateway-DNS rule. With
// that rule, microsandbox's DNS proxy forwards any name upstream, so a guest
// could exfiltrate data in query names without a single allow entry. The
// composable profiles allow broad egress anyway and keep it; `none` drops it,
// and the proxy then resolves only names an egress-allow rule matches.
func (p Policy) allowsGatewayDNS() bool {
	return !slices.Contains(p.Effective().Profiles, ProfileNone)
}

// NormalizeDNSServers validates and normalizes DNS upstream resolvers. Bare IPs
// (IPv4 or IPv6) get ":53" appended; "host:port" / "ip:port" entries are kept
// as-is. Empty entries, a host without a port, a non-numeric or out-of-range
// port, and garbage are rejected.
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
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid DNS server %q: invalid port %q", s, port)
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out, nil
}

// Validate reports whether the policy is well-formed: known, combinable
// profiles and DNS servers that normalize. It is the one validation both the
// config-load path (the --network/--dns flags and the env/config decode) and the
// VM creation path run, so an invalid policy is rejected before any VM work.
func (p Policy) Validate() error {
	_, err := p.Spec()
	return err
}

// Spec converts the policy into a launcher network spec.
//
// `public`, `private`, and `host` compose: the launcher-owned adapter unions
// their allow rules (deduplicated, canonical order) and adds one narrow
// gateway-DNS rule. The `none` profile is launcher-only and selects no profile
// rules — deny-by-default egress, allow ingress — and no gateway-DNS rule, so
// only names matched by egress-allow resolve. It is an allowlist-only policy,
// not an airgap, and cannot be combined with another profile. A policy with no
// profile, including one with only DNSServers set, defaults to public.
//
// The egress lists are deduplicated and the DNS servers normalized here, so the
// spec is the single validated input the adapter composes rules from.
func (p Policy) Spec() (msb.NetworkSpec, error) {
	profiles := dedupeProfiles(p.Effective().Profiles)
	if err := checkProfileCombination(profiles); err != nil {
		return msb.NetworkSpec{}, err
	}
	msbProfiles := make([]msb.NetworkProfile, 0, len(profiles))
	for _, prof := range profiles {
		switch prof {
		case ProfileNone:
			continue
		case ProfilePublic:
			msbProfiles = append(msbProfiles, msb.NetworkProfilePublic)
		case ProfilePrivate:
			msbProfiles = append(msbProfiles, msb.NetworkProfilePrivate)
		case ProfileHost:
			msbProfiles = append(msbProfiles, msb.NetworkProfileHost)
		default:
			return msb.NetworkSpec{}, fmt.Errorf("unknown network profile %q", prof)
		}
	}
	nameservers, err := NormalizeDNSServers(p.DNSServers)
	if err != nil {
		return msb.NetworkSpec{}, err
	}
	return msb.NetworkSpec{
		Profiles:    msbProfiles,
		EgressAllow: dedupe(p.EgressAllow),
		EgressDeny:  dedupe(p.EgressDeny),
		DNSServers:  nameservers,
	}, nil
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

// dedupeProfiles drops repeated profile names so the fingerprint and the SDK
// input ignore duplicates.
func dedupeProfiles(in []Profile) []Profile {
	seen := make(map[Profile]bool, len(in))
	out := make([]Profile, 0, len(in))
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
