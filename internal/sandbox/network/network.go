package network

import (
	"fmt"

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

// Config converts the policy into a microsandbox NetworkConfig.
func (p Policy) Config() (*msbSdk.NetworkConfig, error) {
	if p.Profile == ProfileNone {
		return msbSdk.NetworkPolicy.None(), nil
	}
	cfg, err := msbSdk.NetworkPolicy.FromProfilesChecked(toMSBProfile(p.Profile))
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

func toMSBProfile(p Profile) msbSdk.NetworkProfile {
	return msbSdk.NetworkProfile(p)
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
