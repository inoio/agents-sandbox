package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
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
// emitted with egress-deny ahead of the profile allow rules, a change from the
// pre-#231 order that an existing VM has not baked in. Including it makes the
// fingerprint differ from the one persisted before that fix, so the VM is
// recreated once to pick up the corrected order; the marker reaches state after
// the recreate, so it fires only once. Policies whose emitted rules are
// identical before and after (no deny entries, or the terminal `none` profile)
// get no marker and keep their historical fingerprint.
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
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// denyPrecedesProfileAllow reports whether the policy takes advantage of the
// egress-deny-before-profile-allow emission order (#231): it has at least one
// deny entry and at least one profile that contributes allow rules. When either
// is absent the emitted rules are identical to the pre-fix order, so the policy
// needs no migration marker.
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
// `public`, `private`, and `host` compose: the SDK unions their allow rules
// (deduplicated, canonical order) and adds one narrow gateway-DNS rule. The
// `none` profile is launcher-only and selects no profile rules — deny-by-default
// egress, allow ingress — so the gateway-DNS rule is added manually. It is an
// allowlist-only policy, not an airgap, and cannot be combined with another
// profile. A policy with no profile, including one with only DNSServers set,
// defaults to public.
func (p Policy) Config() (*msbSdk.NetworkConfig, error) {
	profiles := dedupeProfiles(p.Effective().Profiles)
	if err := checkProfileCombination(profiles); err != nil {
		return nil, err
	}
	msbProfiles := make([]msbSdk.NetworkProfile, 0, len(profiles))
	for _, prof := range profiles {
		if prof == ProfileNone {
			continue
		}
		msbProfiles = append(msbProfiles, msbSdk.NetworkProfile(prof))
	}
	cfg, err := msbSdk.NetworkPolicy.FromProfilesChecked(msbProfiles...)
	if err != nil {
		return nil, err
	}
	// FromProfilesChecked returns the gateway-DNS rule first, then the profile
	// allow rules. Drop that DNS rule and re-emit our own at the front so the
	// deny carve-outs can sit between them (#231); `none` selects no profile,
	// so the SDK contributes no rules at all.
	profileRules := cfg.Rules
	if len(msbProfiles) > 0 {
		profileRules = cfg.Rules[1:]
	}
	if len(p.DNSServers) > 0 {
		nameservers, err := NormalizeDNSServers(p.DNSServers)
		if err != nil {
			return nil, err
		}
		cfg.DNS = &msbSdk.DNSConfig{
			Nameservers: nameservers,
		}
	}
	// microsandbox evaluates rules first-match-wins in list order, so the
	// user's egress-deny carve-outs must come before the profile allow rules;
	// otherwise a profile's broad allow shadows the carve-out and the deny
	// never fires (#231). The order is: gateway DNS, deny, profile allows,
	// explicit allows.
	denyRules := make([]msbSdk.PolicyRule, 0, len(p.EgressDeny))
	for _, d := range dedupe(p.EgressDeny) {
		denyRules = append(denyRules, egressRule(msbSdk.PolicyActionDeny, d))
	}
	allowRules := make([]msbSdk.PolicyRule, 0, len(p.EgressAllow))
	for _, d := range dedupe(p.EgressAllow) {
		allowRules = append(allowRules, egressRule(msbSdk.PolicyActionAllow, d))
	}
	cfg.Rules = slices.Concat(
		[]msbSdk.PolicyRule{msbSdk.Rule.AllowDNS()},
		denyRules,
		profileRules,
		allowRules,
	)
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
