package network

import (
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestConfigNoneAllowlistBase(t *testing.T) {
	cfg, err := (Policy{Profile: ProfileNone}).Config()
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
	for _, r := range cfg.Rules {
		if r.Direction == msbSdk.PolicyDirectionEgress &&
			r.Action == msbSdk.PolicyActionAllow &&
			r.Destination == "public" {
			return true
		}
	}
	return false
}

func TestConfigNoneSingleHostAllowlist(t *testing.T) {
	cfg, err := (Policy{
		Profile:     ProfileNone,
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
	cfg, err := (Policy{Profile: ProfilePublic}).Config()
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
		Profile:     ProfilePublic,
		EgressAllow: []string{"123.123.0.0/16"},
		EgressDeny:  []string{"123.123.123.0/24"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	var denyIdx, allowIdx int
	foundDeny, foundAllow := false, false
	for i, r := range cfg.Rules {
		if r.Direction == msbSdk.PolicyDirectionEgress && r.Destination == "123.123.123.0/24" &&
			r.Action == msbSdk.PolicyActionDeny {
			denyIdx, foundDeny = i, true
		}
		if r.Direction == msbSdk.PolicyDirectionEgress && r.Destination == "123.123.0.0/16" &&
			r.Action == msbSdk.PolicyActionAllow {
			allowIdx, foundAllow = i, true
		}
	}
	if !foundDeny || !foundAllow {
		t.Fatalf("expected both deny and allow rules, found deny=%v allow=%v", foundDeny, foundAllow)
	}
	if denyIdx >= allowIdx {
		t.Fatalf("deny rule (idx %d) must precede allow rule (idx %d)", denyIdx, allowIdx)
	}
}

func TestConfigNoneAppliesLists(t *testing.T) {
	cfg, err := (Policy{
		Profile:     ProfileNone,
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
		Profile:     ProfilePublic,
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
		Profile:     ProfilePublic,
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
	if _, err := (Policy{Profile: Profile("bogus")}).Config(); err == nil {
		t.Fatal("Config with unknown profile should error")
	}
}

func TestFingerprintStable(t *testing.T) {
	p := Policy{Profile: ProfileNone, EgressAllow: []string{"b", "a"}}
	first := p.Fingerprint()
	for range 3 {
		if got := p.Fingerprint(); got != first {
			t.Fatalf("Fingerprint not deterministic: %q then %q", first, got)
		}
	}
}

func TestFingerprintOrderIndependent(t *testing.T) {
	a := Policy{
		Profile:     ProfileNone,
		EgressAllow: []string{"b.example.com", "a.example.com"},
		EgressDeny:  []string{"z.example.com", "y.example.com"},
		DNSServers:  []string{"1.1.1.1", "8.8.8.8"},
	}
	b := Policy{
		Profile:     ProfileNone,
		EgressAllow: []string{"a.example.com", "b.example.com"},
		EgressDeny:  []string{"y.example.com", "z.example.com"},
		DNSServers:  []string{"8.8.8.8", "1.1.1.1"},
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("Fingerprint must be independent of list order: %q vs %q", a.Fingerprint(), b.Fingerprint())
	}
}

func TestFingerprintDistinguishesConfigs(t *testing.T) {
	none := Policy{Profile: ProfileNone, EgressAllow: []string{"api.example.com"}}
	public := Policy{Profile: ProfilePublic, EgressAllow: []string{"api.example.com"}}
	if none.Fingerprint() == public.Fingerprint() {
		t.Fatal("different profiles must have different fingerprints")
	}

	allow := Policy{Profile: ProfileNone, EgressAllow: []string{"a.example.com"}}
	deny := Policy{Profile: ProfileNone, EgressDeny: []string{"a.example.com"}}
	if allow.Fingerprint() == deny.Fingerprint() {
		t.Fatal("allow vs deny lists must have different fingerprints")
	}

	// Every list entry must reach the digest: a list that stops contributing
	// silently lets an allow/deny change go undetected across runs.
	base := Policy{Profile: ProfileNone}
	withAllow := Policy{Profile: ProfileNone, EgressAllow: []string{"a.example.com"}}
	if base.Fingerprint() == withAllow.Fingerprint() {
		t.Fatal("adding an egress-allow entry must change the fingerprint")
	}
	withDeny := Policy{Profile: ProfileNone, EgressDeny: []string{"a.example.com"}}
	if base.Fingerprint() == withDeny.Fingerprint() {
		t.Fatal("adding an egress-deny entry must change the fingerprint")
	}
	// An entry must be listed under its own kind: a shared destination in
	// allow and in deny are different policies and must not collide.
	if withAllow.Fingerprint() != (Policy{Profile: ProfileNone, EgressAllow: []string{"a.example.com"}}).Fingerprint() {
		t.Fatal("fingerprint must be stable for the same policy")
	}
}

func TestFingerprintCountsEveryEntry(t *testing.T) {
	one := Policy{Profile: ProfileNone, EgressAllow: []string{"a.example.com"}, EgressDeny: []string{"b.example.com"}}
	twoAllowed := Policy{
		Profile:     ProfileNone,
		EgressAllow: []string{"a.example.com", "c.example.com"},
		EgressDeny:  []string{"b.example.com"},
	}
	if one.Fingerprint() == twoAllowed.Fingerprint() {
		t.Fatal("a second egress-allow entry must change the fingerprint")
	}
	twoDenied := Policy{
		Profile:     ProfileNone,
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
		Profile:    ProfilePublic,
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
	cfg, err := (Policy{Profile: ProfilePublic}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DNS != nil {
		t.Fatalf("Config must leave DNS nil without DNSServers, got %+v", cfg.DNS)
	}
}

func TestConfigDNSWithNoneProfile(t *testing.T) {
	cfg, err := (Policy{
		Profile:    ProfileNone,
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
		Profile:    ProfilePublic,
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
		{"profile", Policy{Profile: ProfilePublic}},
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

func TestFingerprintEmptyUsesPublicDefault(t *testing.T) {
	if (Policy{}).Fingerprint() != (Policy{Profile: ProfilePublic}).Fingerprint() {
		t.Fatal("unset policy and explicit public policy must have the same fingerprint")
	}
}

func TestFingerprintDistinguishesDNSServers(t *testing.T) {
	base := Policy{Profile: ProfilePublic}
	withDNS := Policy{Profile: ProfilePublic, DNSServers: []string{"1.1.1.1", "8.8.8.8"}}
	if base.Fingerprint() == withDNS.Fingerprint() {
		t.Fatal("adding dns-servers must change the fingerprint")
	}

	reordered := Policy{Profile: ProfilePublic, DNSServers: []string{"8.8.8.8", "1.1.1.1"}}
	if reordered.Fingerprint() != withDNS.Fingerprint() {
		t.Fatal("fingerprint must be independent of dns-servers order")
	}

	different := Policy{Profile: ProfilePublic, DNSServers: []string{"8.8.8.8"}}
	if different.Fingerprint() == withDNS.Fingerprint() {
		t.Fatal("different dns-servers must change the fingerprint")
	}
}
