package network

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
// egress-deny) directly into this struct.
type Policy struct {
	Profile     Profile  `mapstructure:"profile"`
	EgressAllow []string `mapstructure:"egress-allow"`
	EgressDeny  []string `mapstructure:"egress-deny"`
}

// Empty reports whether the policy is unset (zero value), meaning the caller
// should fall back to the default public profile.
func (p Policy) Empty() bool { return p.Profile == "" }

// Fingerprint returns a stable SHA-256 hex digest of the policy, for detecting
// changes across runs. It hashes the profile and the sorted allow/deny lists,
// independent of the microsandbox SDK's canonical NetworkConfig shape.
func (p Policy) Fingerprint() string {
	var lines []string
	lines = append(lines, "profile="+string(p.Profile))
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

// Config converts the policy into a microsandbox NetworkConfig.
//
// The `none` profile is an allowlist-only policy: egress is deny-by-default,
// ingress is allowed, and only the gateway-DNS rule plus the explicit
// egress-allow/egress-deny lists apply. It is not an airgap.
func (p Policy) Config() (*msbSdk.NetworkConfig, error) {
	var cfg *msbSdk.NetworkConfig
	var err error
	if p.Profile == ProfileNone {
		// Deny-by-default egress, allow ingress, with no profile rules. The
		// gateway-DNS rule is added manually so named allow-listed hosts can
		// resolve.
		cfg, err = msbSdk.NetworkPolicy.FromProfilesChecked()
		cfg.Rules = append(cfg.Rules, msbSdk.Rule.AllowDNS())
	} else {
		cfg, err = msbSdk.NetworkPolicy.FromProfilesChecked(msbSdk.NetworkProfile(p.Profile))
	}
	if err != nil {
		return nil, err
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
