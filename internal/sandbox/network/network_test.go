package network

import (
	"slices"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestConfigNoneAllowlistBase(t *testing.T) {
	cfg, err := (Policy{Profiles: []Profile{ProfileNone}}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	// none is allowlist-only: deny egress by default, allow ingress.
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("none must deny egress by default, got %v", cfg.DefaultEgress)
	}
	if cfg.DefaultIngress != msbSdk.PolicyActionAllow {
		t.Fatalf("none must allow ingress, got %v", cfg.DefaultIngress)
	}
	// Only the gateway-DNS rule is present when no lists are given.
	if len(cfg.Rules) != 1 {
		t.Fatalf("none with no lists should have only the gateway-DNS rule, got %d rules", len(cfg.Rules))
	}
	if cfg.Rules[0].Destination != "host" {
		t.Fatalf("gateway-DNS rule destination = %q, want host", cfg.Rules[0].Destination)
	}
}

func TestConfigUnsetDefaultsToPublic(t *testing.T) {
	cfg, err := (Policy{}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	// The public profile keeps the deny default action and adds an allow rule
	// for the public destination group.
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("unset policy must keep a deny default action, got %v", cfg.DefaultEgress)
	}
	if cfg.DefaultIngress != msbSdk.PolicyActionAllow {
		t.Fatalf("unset policy must allow ingress, got %v", cfg.DefaultIngress)
	}
	if !allowsPublic(cfg) {
		t.Fatalf("unset policy should allow the public group, got %+v", cfg.Rules)
	}
}

// allowsPublic reports whether the config carries an allow rule for the public
// destination group.
func allowsPublic(cfg *msbSdk.NetworkConfig) bool {
	return allowsProfile(cfg, "public")
}

// allowsProfile reports whether the config carries an egress allow rule for the
// named destination group.
func allowsProfile(cfg *msbSdk.NetworkConfig, group string) bool {
	for _, r := range cfg.Rules {
		if r.Direction == msbSdk.PolicyDirectionEgress &&
			r.Action == msbSdk.PolicyActionAllow &&
			r.Destination == group {
			return true
		}
	}
	return false
}

func TestConfigCombinesProfiles(t *testing.T) {
	cfg, err := (Policy{Profiles: []Profile{ProfilePublic, ProfileHost}}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	for _, group := range []string{"public", "host"} {
		if !allowsProfile(cfg, group) {
			t.Errorf("combined policy should allow the %s group, got %+v", group, cfg.Rules)
		}
	}
}

func TestConfigRejectsNoneCombinedWithOther(t *testing.T) {
	if _, err := (Policy{Profiles: []Profile{ProfileNone, ProfilePublic}}).Config(); err == nil {
		t.Fatal("Config must reject none combined with another profile")
	}
}

func TestParseProfiles(t *testing.T) {
	got, err := ParseProfiles([]string{"public", "host"})
	if err != nil {
		t.Fatalf("ParseProfiles: %v", err)
	}
	if len(got) != 2 || got[0] != ProfilePublic || got[1] != ProfileHost {
		t.Fatalf("ParseProfiles = %v, want [public host]", got)
	}

	if _, err := ParseProfiles([]string{"bogus"}); err == nil {
		t.Fatal("ParseProfiles(bogus) should error")
	}
	if _, err := ParseProfiles([]string{"none", "public"}); err == nil {
		t.Fatal("ParseProfiles(none, public) should error")
	}
}

func TestParseProfilesRepeatedNone(t *testing.T) {
	got, err := ParseProfiles([]string{"none", "none"})
	if err != nil {
		t.Fatalf("ParseProfiles(none, none): %v", err)
	}
	if len(got) != 1 || got[0] != ProfileNone {
		t.Fatalf("ParseProfiles(none, none) = %v, want [none]", got)
	}

	if _, err := ParseProfiles([]string{"none", "none", "public"}); err == nil {
		t.Fatal("ParseProfiles(none, none, public) should error")
	}
}

func TestConfigNoneSingleHostAllowlist(t *testing.T) {
	cfg, err := (Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"api.example.com"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("none must stay deny-by-default, got %v", cfg.DefaultEgress)
	}
	var allowed int
	for _, r := range cfg.Rules {
		if r.Direction == msbSdk.PolicyDirectionEgress && r.Action == msbSdk.PolicyActionAllow {
			allowed++
		}
	}
	// Gateway-DNS + the single explicit allow = 2 allow rules; nothing else.
	if allowed != 2 {
		t.Fatalf("expected exactly gateway-DNS + 1 explicit allow rule, got %d allow rules", allowed)
	}
}

func TestConfigPublicProfileRules(t *testing.T) {
	cfg, err := (Policy{Profiles: []Profile{ProfilePublic}}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("FromProfilesChecked defaults egress to deny, got %v", cfg.DefaultEgress)
	}
	if len(cfg.Rules) == 0 {
		t.Fatal("public profile should produce gateway DNS + public allow rules")
	}
}

func TestConfigDenyBeforeAllow(t *testing.T) {
	cfg, err := (Policy{
		Profiles:    []Profile{ProfilePublic},
		EgressAllow: []string{"123.123.0.0/16"},
		EgressDeny:  []string{"123.123.123.0/24"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	var denyIdx, explicitAllowIdx, profileAllowIdx int
	foundDeny, foundExplicitAllow, foundProfileAllow := false, false, false
	for i, r := range cfg.Rules {
		if r.Direction != msbSdk.PolicyDirectionEgress {
			continue
		}
		switch {
		case r.Destination == "123.123.123.0/24" && r.Action == msbSdk.PolicyActionDeny:
			denyIdx, foundDeny = i, true
		case r.Destination == "123.123.0.0/16" && r.Action == msbSdk.PolicyActionAllow:
			explicitAllowIdx, foundExplicitAllow = i, true
		case r.Destination == "public" && r.Action == msbSdk.PolicyActionAllow:
			profileAllowIdx, foundProfileAllow = i, true
		}
	}
	if !foundDeny || !foundExplicitAllow || !foundProfileAllow {
		t.Fatalf("expected deny, explicit allow, and profile allow rules; found deny=%v explicit=%v profile=%v",
			foundDeny, foundExplicitAllow, foundProfileAllow)
	}
	if denyIdx >= explicitAllowIdx {
		t.Fatalf("deny rule (idx %d) must precede explicit allow rule (idx %d)", denyIdx, explicitAllowIdx)
	}
	// First-match-wins: the profile's broad allow must not shadow the carve-out.
	if denyIdx >= profileAllowIdx {
		t.Fatalf("deny rule (idx %d) must precede profile allow rule (idx %d)", denyIdx, profileAllowIdx)
	}
}

func TestConfigDenyPrecedesProfileAllow(t *testing.T) {
	const denyDestination = "10.0.0.0/8"
	cases := []struct {
		name     string
		profiles []Profile
	}{
		{name: "public", profiles: []Profile{ProfilePublic}},
		{name: "private", profiles: []Profile{ProfilePrivate}},
		{name: "host", profiles: []Profile{ProfileHost}},
		{name: "unset defaults to public"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := (Policy{Profiles: tc.profiles, EgressDeny: []string{denyDestination}}).Config()
			if err != nil {
				t.Fatalf("Config: %v", err)
			}
			denyIdx := -1
			var profileAllowIdxs []int
			for i, r := range cfg.Rules {
				if r.Direction != msbSdk.PolicyDirectionEgress {
					continue
				}
				if r.Action == msbSdk.PolicyActionDeny && r.Destination == denyDestination {
					denyIdx = i
				}
				// Profile allow rules carry a destination group name as the
				// whole destination; the gateway-DNS allow rule is the one
				// with a port.
				if r.Action == msbSdk.PolicyActionAllow && r.Port == "" && isProfileDestination(r.Destination) {
					profileAllowIdxs = append(profileAllowIdxs, i)
				}
			}
			if denyIdx < 0 {
				t.Fatalf("egress-deny rule not found in %+v", cfg.Rules)
			}
			if len(profileAllowIdxs) == 0 {
				t.Fatalf("expected at least one profile allow rule, got %+v", cfg.Rules)
			}
			for _, idx := range profileAllowIdxs {
				if denyIdx >= idx {
					t.Fatalf(
						"deny rule (idx %d) must precede profile allow rule (idx %d): %+v",
						denyIdx,
						idx,
						cfg.Rules,
					)
				}
			}
		})
	}
}

func TestConfigRuleOrder(t *testing.T) {
	type rule struct {
		action      msbSdk.PolicyAction
		destination string
		port        string
	}
	// The exact list order is the contract: microsandbox matches the first
	// applicable rule, so the gateway-DNS rule leads, then the user deny
	// carve-outs, then the profile allow rules, then the explicit allows (#231).
	cases := []struct {
		name   string
		policy Policy
		want   []rule
	}{
		{
			name: "profile with deny and allow",
			policy: Policy{
				Profiles:    []Profile{ProfilePublic},
				EgressDeny:  []string{"10.0.0.0/8"},
				EgressAllow: []string{"api.example.com"},
			},
			want: []rule{
				{msbSdk.PolicyActionAllow, "host", "53"},
				{msbSdk.PolicyActionDeny, "10.0.0.0/8", ""},
				{msbSdk.PolicyActionAllow, "public", ""},
				{msbSdk.PolicyActionAllow, "api.example.com", ""},
			},
		},
		{
			name: "none with deny and allow",
			policy: Policy{
				Profiles:    []Profile{ProfileNone},
				EgressDeny:  []string{"10.0.0.0/8"},
				EgressAllow: []string{"api.example.com"},
			},
			want: []rule{
				{msbSdk.PolicyActionAllow, "host", "53"},
				{msbSdk.PolicyActionDeny, "10.0.0.0/8", ""},
				{msbSdk.PolicyActionAllow, "api.example.com", ""},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := tc.policy.Config()
			if err != nil {
				t.Fatalf("Config: %v", err)
			}
			got := make([]rule, 0, len(cfg.Rules))
			for _, r := range cfg.Rules {
				got = append(got, rule{r.Action, r.Destination, r.Port})
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("rule order = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestConfigNoneAppliesLists(t *testing.T) {
	cfg, err := (Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"1.1.1.1"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	found := false
	for _, r := range cfg.Rules {
		if r.Direction == msbSdk.PolicyDirectionEgress && r.Destination == "1.1.1.1" &&
			r.Action == msbSdk.PolicyActionAllow {
			found = true
		}
	}
	if !found {
		t.Fatalf("none must apply the explicit allow list, got %+v", cfg.Rules)
	}
}

func TestConfigDeDuplicatesEntries(t *testing.T) {
	cfg, err := (Policy{
		Profiles:    []Profile{ProfilePublic},
		EgressAllow: []string{"api.github.com", "api.github.com"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	count := 0
	for _, r := range cfg.Rules {
		if r.Destination == "api.github.com" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected de-duplicated allow entry, got %d", count)
	}
}

func TestParseProfile(t *testing.T) {
	if p, err := ParseProfile("none"); err != nil || p != ProfileNone {
		t.Fatalf("ParseProfile(none) = %v, %v", p, err)
	}
	if _, err := ParseProfile("bogus"); err == nil {
		t.Fatal("ParseProfile(bogus) should error")
	}
}

func TestConfigDeDuplicatesEmptyEntries(t *testing.T) {
	cfg, err := (Policy{
		Profiles:    []Profile{ProfilePublic},
		EgressAllow: []string{"", "api.github.com", ""},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	count := 0
	for _, r := range cfg.Rules {
		if r.Destination == "api.github.com" {
			count++
		}
		if r.Destination == "" {
			t.Fatalf("Config must drop empty destinations, got rule with empty destination")
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one rule for api.github.com, got %d", count)
	}
}

func TestConfigUnknownProfileErrors(t *testing.T) {
	if _, err := (Policy{Profiles: []Profile{Profile("bogus")}}).Config(); err == nil {
		t.Fatal("Config with unknown profile should error")
	}
}

func TestFingerprintStable(t *testing.T) {
	p := Policy{Profiles: []Profile{ProfileNone}, EgressAllow: []string{"b", "a"}}
	first := p.Fingerprint()
	for range 3 {
		if got := p.Fingerprint(); got != first {
			t.Fatalf("Fingerprint not deterministic: %q then %q", first, got)
		}
	}
}

func TestFingerprintOrderIndependent(t *testing.T) {
	a := Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"b.example.com", "a.example.com"},
		EgressDeny:  []string{"z.example.com", "y.example.com"},
		DNSServers:  []string{"1.1.1.1", "8.8.8.8"},
	}
	b := Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"a.example.com", "b.example.com"},
		EgressDeny:  []string{"y.example.com", "z.example.com"},
		DNSServers:  []string{"8.8.8.8", "1.1.1.1"},
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("Fingerprint must be independent of list order: %q vs %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprintDistinguishesConfigs(t *testing.T) {
	none := Policy{Profiles: []Profile{ProfileNone}, EgressAllow: []string{"api.example.com"}}
	public := Policy{Profiles: []Profile{ProfilePublic}, EgressAllow: []string{"api.example.com"}}
	if none.Fingerprint() == public.Fingerprint() {
		t.Fatal("different profiles must have different fingerprints")
	}

	allow := Policy{Profiles: []Profile{ProfileNone}, EgressAllow: []string{"a.example.com"}}
	deny := Policy{Profiles: []Profile{ProfileNone}, EgressDeny: []string{"a.example.com"}}
	if allow.Fingerprint() == deny.Fingerprint() {
		t.Fatal("allow vs deny lists must have different fingerprints")
	}

	// Every list entry must reach the digest: a list that stops contributing
	// silently lets an allow/deny change go undetected across runs.
	base := Policy{Profiles: []Profile{ProfileNone}}
	withAllow := Policy{Profiles: []Profile{ProfileNone}, EgressAllow: []string{"a.example.com"}}
	if base.Fingerprint() == withAllow.Fingerprint() {
		t.Fatal("adding an egress-allow entry must change the fingerprint")
	}
	withDeny := Policy{Profiles: []Profile{ProfileNone}, EgressDeny: []string{"a.example.com"}}
	if base.Fingerprint() == withDeny.Fingerprint() {
		t.Fatal("adding an egress-deny entry must change the fingerprint")
	}
}

func TestFingerprintCountsEveryEntry(t *testing.T) {
	one := Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"a.example.com"},
		EgressDeny:  []string{"b.example.com"},
	}
	twoAllowed := Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"a.example.com", "c.example.com"},
		EgressDeny:  []string{"b.example.com"},
	}
	if one.Fingerprint() == twoAllowed.Fingerprint() {
		t.Fatal("a second egress-allow entry must change the fingerprint")
	}
	twoDenied := Policy{
		Profiles:    []Profile{ProfileNone},
		EgressAllow: []string{"a.example.com"},
		EgressDeny:  []string{"b.example.com", "c.example.com"},
	}
	if one.Fingerprint() == twoDenied.Fingerprint() {
		t.Fatal("a second egress-deny entry must change the fingerprint")
	}
}

func TestProfileString(t *testing.T) {
	if got := ProfilePublic.String(); got != "public" {
		t.Errorf("ProfilePublic.String() = %q, want public", got)
	}
	if got := Profile("custom").String(); got != "custom" {
		t.Errorf("Profile(custom).String() = %q, want custom", got)
	}
}

func TestNormalizeDNSServers(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
		// errContains pins which rejection fired: several inputs would also
		// be rejected by a later check, so err != nil alone cannot tell the
		// "not an IP or host:port" branch from the "missing host" fall-through.
		errContains string
	}{
		{
			name: "bare IPv4 gets :53",
			in:   []string{"1.1.1.1", "8.8.8.8"},
			want: []string{"1.1.1.1:53", "8.8.8.8:53"},
		},
		{
			name: "bare IPv6 gets :53",
			in:   []string{"2606:4700:4700::1111"},
			want: []string{"[2606:4700:4700::1111]:53"},
		},
		{
			name: "host:port kept as-is",
			in:   []string{"1.1.1.1:5353", "dns.example.com:53"},
			want: []string{"1.1.1.1:5353", "dns.example.com:53"},
		},
		{
			name: "ipv6 with port kept as-is",
			in:   []string{"[2606:4700:4700::1111]:5353"},
			want: []string{"[2606:4700:4700::1111]:5353"},
		},
		{
			name: "whitespace trimmed",
			in:   []string{" 1.1.1.1 "},
			want: []string{"1.1.1.1:53"},
		},
		{
			name:        "empty entry rejected",
			in:          []string{"1.1.1.1", ""},
			wantErr:     true,
			errContains: "empty DNS server entry",
		},
		{name: "host without port rejected", in: []string{"1.1.1.1:"}, wantErr: true, errContains: "missing port"},
		{name: "empty host rejected", in: []string{":53"}, wantErr: true, errContains: "missing host"},
		{name: "bare hostname rejected", in: []string{"dns.example.com"}, wantErr: true, errContains: "must be an IP"},
		{name: "garbage rejected", in: []string{"not a dns server"}, wantErr: true, errContains: "must be an IP"},
		{name: "too many colons rejected", in: []string{"1.1.1.1:53:54"}, wantErr: true, errContains: "must be an IP"},
		{name: "nil input yields empty", in: nil, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeDNSServers(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeDNSServers(%v) expected error, got %v", tc.in, got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("NormalizeDNSServers(%v) error = %q, want it to contain %q", tc.in, err, tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeDNSServers(%v): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("NormalizeDNSServers(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("NormalizeDNSServers(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestConfigDNSServersNormalized(t *testing.T) {
	cfg, err := (Policy{
		Profiles:   []Profile{ProfilePublic},
		DNSServers: []string{"1.1.1.1", "8.8.8.8:5353"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DNS == nil {
		t.Fatal("Config must set DNS when DNSServers configured")
	}
	want := []string{"1.1.1.1:53", "8.8.8.8:5353"}
	if len(cfg.DNS.Nameservers) != len(want) {
		t.Fatalf("Nameservers = %v, want %v", cfg.DNS.Nameservers, want)
	}
	for i := range want {
		if cfg.DNS.Nameservers[i] != want[i] {
			t.Fatalf("Nameservers = %v, want %v", cfg.DNS.Nameservers, want)
		}
	}
}

func TestConfigDNSNilWhenAbsent(t *testing.T) {
	cfg, err := (Policy{Profiles: []Profile{ProfilePublic}}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DNS != nil {
		t.Fatalf("Config must leave DNS nil without DNSServers, got %+v", cfg.DNS)
	}
}

func TestConfigDNSWithNoneProfile(t *testing.T) {
	cfg, err := (Policy{
		Profiles:   []Profile{ProfileNone},
		DNSServers: []string{"1.1.1.1"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("none must stay deny-by-default, got %v", cfg.DefaultEgress)
	}
	if cfg.DNS == nil {
		t.Fatal("none + DNSServers must set DNS")
	}
	if got := cfg.DNS.Nameservers[0]; got != "1.1.1.1:53" {
		t.Fatalf("Nameservers[0] = %q, want 1.1.1.1:53", got)
	}
}

func TestConfigDNSOnlyDefaultsToPublic(t *testing.T) {
	cfg, err := (Policy{DNSServers: []string{"1.1.1.1"}}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	// A dns-only policy without a profile uses the default public profile.
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny {
		t.Fatalf("dns-only default egress = %v, want deny", cfg.DefaultEgress)
	}
	if cfg.DNS == nil {
		t.Fatal("dns-only policy must set DNS")
	}
	if !allowsPublic(cfg) {
		t.Fatalf("dns-only policy should use the default public profile, got %+v", cfg.Rules)
	}
}

func TestConfigDNSInvalidErrors(t *testing.T) {
	if _, err := (Policy{
		Profiles:   []Profile{ProfilePublic},
		DNSServers: []string{"garbage"},
	}).Config(); err == nil {
		t.Fatal("Config with invalid DNS server should error")
	}
}

func TestPolicyEmpty(t *testing.T) {
	if !(Policy{}).Empty() {
		t.Error("zero Policy should be empty")
	}
	// Each list must be able to make the policy non-empty on its own; a
	// single maskable field would let a policy silently read as unset.
	nonEmpty := []struct {
		name   string
		policy Policy
	}{
		{"profiles", Policy{Profiles: []Profile{ProfilePublic}}},
		{"egress-allow", Policy{EgressAllow: []string{"api.example.com"}}},
		{"egress-deny", Policy{EgressDeny: []string{"tracker.example.com"}}},
		{"dns-servers", Policy{DNSServers: []string{"1.1.1.1"}}},
	}
	for _, tc := range nonEmpty {
		t.Run(tc.name, func(t *testing.T) {
			if tc.policy.Empty() {
				t.Errorf("Policy with only %s set should not be empty", tc.name)
			}
		})
	}
}

func TestFingerprintProfileSetOrderIndependent(t *testing.T) {
	a := Policy{Profiles: []Profile{ProfilePublic, ProfileHost}}
	b := Policy{Profiles: []Profile{ProfileHost, ProfilePublic}}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatalf("fingerprint must be independent of profile order: %q vs %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprintProfileSetDeduplicates(t *testing.T) {
	once := Policy{Profiles: []Profile{ProfilePublic}}
	twice := Policy{Profiles: []Profile{ProfilePublic, ProfilePublic}}
	if once.Fingerprint() != twice.Fingerprint() {
		t.Fatal("duplicate profiles must not change the fingerprint")
	}
}

// TestFingerprintSingleProfileMatchesLegacyFormat pins the exact digest a
// single-profile policy produced before profiles became composable: the
// fingerprint emits one "profile=" line per profile, so widening the model must
// not change existing state and force every project VM to be recreated.
func TestFingerprintSingleProfileMatchesLegacyFormat(t *testing.T) {
	const legacyPublic = "a22d48312e2ea6d5e08a1b5bca6f04e1a42da13d2daaa1337b4d5be145a31116"
	if got := (Policy{Profiles: []Profile{ProfilePublic}}).Fingerprint(); got != legacyPublic {
		t.Fatalf("single-profile fingerprint = %q, want legacy %q", got, legacyPublic)
	}
}

// TestFingerprintDenyPolicyMigratesFromLegacy pins the pre-#231 fingerprint for
// a deny-bearing profiled policy and asserts the current fingerprint diverges
// from it, so an existing VM (whose persisted NetworkState holds the old hash)
// is recreated once to pick up the egress-deny-before-profile-allow order.
func TestFingerprintDenyPolicyMigratesFromLegacy(t *testing.T) {
	const legacyDenyPublic = "440e1b21984eb6657c69098f6e9ddd813c90b39ebc3f6f575ad640ec97528498"
	p := Policy{Profiles: []Profile{ProfilePublic}, EgressDeny: []string{"123.123.123.0/24"}}
	if got := p.Fingerprint(); got == legacyDenyPublic {
		t.Fatalf("deny-bearing fingerprint %q must diverge from the pre-fix value", got)
	}
	// The marker is content-independent: a reordered deny list still hashes the
	// same.
	reordered := Policy{Profiles: []Profile{ProfilePublic}, EgressDeny: []string{"10.0.0.0/8", "123.123.123.0/24"}}
	other := Policy{Profiles: []Profile{ProfilePublic}, EgressDeny: []string{"123.123.123.0/24", "10.0.0.0/8"}}
	if reordered.Fingerprint() != other.Fingerprint() {
		t.Fatal("deny order must not change the fingerprint")
	}
}

// TestFingerprintDenyWithoutProfileRulesKeepsLegacyFormat verifies the migration
// marker is omitted when the emitted rules do not change: a `none` policy emits
// no profile allow rules, so its deny rules already came first.
func TestFingerprintDenyWithoutProfileRulesKeepsLegacyFormat(t *testing.T) {
	const legacyNoneDeny = "d825326a2c0b481774b194a7cbb7597bbad1558e38726d0ea7b6e4be792ff3a7"
	p := Policy{Profiles: []Profile{ProfileNone}, EgressDeny: []string{"123.123.123.0/24"}}
	if got := p.Fingerprint(); got != legacyNoneDeny {
		t.Fatalf("none deny fingerprint = %q, want legacy %q", got, legacyNoneDeny)
	}
}

func TestFingerprintEmptyUsesPublicDefault(t *testing.T) {
	if (Policy{}).Fingerprint() != (Policy{Profiles: []Profile{ProfilePublic}}).Fingerprint() {
		t.Fatal("unset policy and explicit public policy must have the same fingerprint")
	}
}

func TestFingerprintDistinguishesDNSServers(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfilePublic}}
	withDNS := Policy{Profiles: []Profile{ProfilePublic}, DNSServers: []string{"1.1.1.1", "8.8.8.8"}}
	if base.Fingerprint() == withDNS.Fingerprint() {
		t.Fatal("adding dns-servers must change the fingerprint")
	}

	reordered := Policy{Profiles: []Profile{ProfilePublic}, DNSServers: []string{"8.8.8.8", "1.1.1.1"}}
	if reordered.Fingerprint() != withDNS.Fingerprint() {
		t.Fatal("fingerprint must be independent of dns-servers order")
	}

	different := Policy{Profiles: []Profile{ProfilePublic}, DNSServers: []string{"8.8.8.8"}}
	if different.Fingerprint() == withDNS.Fingerprint() {
		t.Fatal("different dns-servers must change the fingerprint")
	}
}

// isProfileDestination reports whether a rule destination names one of the
// composable profile groups (as opposed to a user allow/deny destination or
// the gateway-DNS rule, which shares the "host" destination but carries a port).
func isProfileDestination(destination string) bool {
	switch destination {
	case "public", "private", "host":
		return true
	default:
		return false
	}
}
