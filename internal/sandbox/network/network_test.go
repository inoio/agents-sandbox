package network

import (
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestParseProfile(t *testing.T) {
	for _, name := range []string{"public", "private", "host", "none"} {
		got, err := ParseProfile(name)
		if err != nil || got != Profile(name) {
			t.Errorf("ParseProfile(%q) = %q, %v; want %q, nil", name, got, err, name)
		}
	}
	if _, err := ParseProfile("bogus"); err == nil {
		t.Error("ParseProfile(bogus) = nil error, want an unknown-profile error")
	}
}

func TestParseProfiles(t *testing.T) {
	got, err := ParseProfiles([]string{" public ", "host"})
	if err != nil {
		t.Fatalf("ParseProfiles: %v", err)
	}
	if !slices.Equal(got, []Profile{ProfilePublic, ProfileHost}) {
		t.Fatalf("ParseProfiles = %v, want [public host]", got)
	}

	if _, err := ParseProfiles([]string{"bogus"}); err == nil {
		t.Fatal("ParseProfiles(bogus) should error")
	}
	if _, err := ParseProfiles([]string{"none", "public"}); err == nil {
		t.Fatal("ParseProfiles(none, public) should error")
	}
}

// TestParseProfilesRepeatedNone pins that dedupe runs before the combination
// check, so repeating `none` is not mistaken for combining it with another.
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

func TestSpecNoneAllowlistBase(t *testing.T) {
	spec, err := (Policy{Profiles: []Profile{ProfileNone}}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	// none is launcher-only: it selects no composable profile and carries no
	// rules, so the adapter emits no gateway-DNS rule.
	if len(spec.Profiles) != 0 {
		t.Fatalf("none must select no composable profile, got %v", spec.Profiles)
	}
	if len(spec.EgressAllow) != 0 || len(spec.EgressDeny) != 0 || len(spec.DNSServers) != 0 {
		t.Fatalf("none with no lists must carry no lists, got %+v", spec)
	}
}

func TestSpecUnsetDefaultsToPublic(t *testing.T) {
	spec, err := (Policy{}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if len(spec.Profiles) != 1 || spec.Profiles[0] != msb.NetworkProfilePublic {
		t.Fatalf("unset policy must default to the public profile, got %v", spec.Profiles)
	}
}

func TestSpecComposableProfiles(t *testing.T) {
	spec, err := (Policy{Profiles: []Profile{ProfilePublic, ProfilePrivate, ProfileHost, ProfilePublic}}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	want := []msb.NetworkProfile{
		msb.NetworkProfilePublic,
		msb.NetworkProfilePrivate,
		msb.NetworkProfileHost,
	}
	if !slices.Equal(spec.Profiles, want) {
		t.Fatalf("Spec.Profiles = %v, want deduped %v", spec.Profiles, want)
	}
}

func TestSpecDeduplicatesListsAndDropsEmpty(t *testing.T) {
	spec, err := (Policy{
		Profiles:    []Profile{ProfilePublic},
		EgressAllow: []string{"", "a.example.com", "a.example.com", ""},
		EgressDeny:  []string{"b.example.com", "b.example.com"},
	}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if !slices.Equal(spec.EgressAllow, []string{"a.example.com"}) {
		t.Errorf("EgressAllow = %v, want [a.example.com]", spec.EgressAllow)
	}
	if !slices.Equal(spec.EgressDeny, []string{"b.example.com"}) {
		t.Errorf("EgressDeny = %v, want [b.example.com]", spec.EgressDeny)
	}
}

func TestSpecRejectsNoneCombinedWithOther(t *testing.T) {
	if _, err := (Policy{Profiles: []Profile{ProfileNone, ProfilePublic}}).Spec(); err == nil {
		t.Fatal("Spec must reject none combined with another profile")
	}
}

func TestSpecUnknownProfileErrors(t *testing.T) {
	if _, err := (Policy{Profiles: []Profile{Profile("bogus")}}).Spec(); err == nil {
		t.Fatal("Spec with unknown profile should error")
	}
}

func TestSpecDNSServersNormalized(t *testing.T) {
	spec, err := (Policy{
		Profiles:   []Profile{ProfilePublic},
		DNSServers: []string{"1.1.1.1", "8.8.8.8:5353"},
	}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	want := []string{"1.1.1.1:53", "8.8.8.8:5353"}
	if !slices.Equal(spec.DNSServers, want) {
		t.Fatalf("DNSServers = %v, want %v", spec.DNSServers, want)
	}
}

func TestSpecDNSOnlyDefaultsToPublic(t *testing.T) {
	spec, err := (Policy{DNSServers: []string{"1.1.1.1"}}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if len(spec.Profiles) != 1 || spec.Profiles[0] != msb.NetworkProfilePublic {
		t.Fatalf("dns-only policy must use the default public profile, got %v", spec.Profiles)
	}
	if !slices.Equal(spec.DNSServers, []string{"1.1.1.1:53"}) {
		t.Errorf("DNSServers = %v, want 1.1.1.1:53", spec.DNSServers)
	}
}

func TestSpecDNSInvalidErrors(t *testing.T) {
	if _, err := (Policy{
		Profiles:   []Profile{ProfilePublic},
		DNSServers: []string{"garbage"},
	}).Spec(); err == nil {
		t.Fatal("Spec with invalid DNS server should error")
	}
}

// TestPolicyValidate pins the single validation both the config-load and the VM
// creation path run: a well-formed policy passes, and an unknown or
// non-combinable profile or an unnormalizable DNS server is rejected.
func TestPolicyValidate(t *testing.T) {
	valid := []Policy{
		{},
		{Profiles: []Profile{ProfilePublic, ProfileHost}},
		{DNSServers: []string{"1.1.1.1", "8.8.8.8:5353"}},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("Policy%+v.Validate() = %v, want nil", p, err)
		}
	}

	invalid := []Policy{
		{Profiles: []Profile{ProfileNone, ProfilePublic}},
		{Profiles: []Profile{Profile("bogus")}},
		{DNSServers: []string{"garbage"}},
		{DNSServers: []string{"1.1.1.1:notaport"}},
	}
	for _, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("Policy%+v.Validate() = nil, want an error", p)
		}
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
		{
			name:        "non-numeric port rejected",
			in:          []string{"1.1.1.1:notaport"},
			wantErr:     true,
			errContains: "invalid port",
		},
		{name: "port zero rejected", in: []string{"1.1.1.1:0"}, wantErr: true, errContains: "invalid port"},
		{name: "port above 65535 rejected", in: []string{"1.1.1.1:65536"}, wantErr: true, errContains: "invalid port"},
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

// TestFingerprintNonePolicyMigratesFromLegacy pins the pre-#233 fingerprints of
// `none` policies and asserts the current ones diverge, so an existing VM
// (whose persisted NetworkState holds the old hash and whose baked-in rules
// still allow gateway DNS) is recreated once without the gateway-DNS rule.
func TestFingerprintNonePolicyMigratesFromLegacy(t *testing.T) {
	cases := map[string]struct {
		legacy string
		policy Policy
	}{
		"bare": {
			legacy: "950b1e22ca472ac5ec7b1e1b44376630af9b707a935c83515bfd9039886716fd",
			policy: Policy{Profiles: []Profile{ProfileNone}},
		},
		"with deny": {
			legacy: "d825326a2c0b481774b194a7cbb7597bbad1558e38726d0ea7b6e4be792ff3a7",
			policy: Policy{Profiles: []Profile{ProfileNone}, EgressDeny: []string{"123.123.123.0/24"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.policy.Fingerprint(); got == tc.legacy {
				t.Fatalf("none fingerprint %q must diverge from the pre-#233 value", got)
			}
		})
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

func TestDescribeListsTheEffectivePolicyEntries(t *testing.T) {
	got := (Policy{
		Profiles:    []Profile{ProfilePublic, ProfilePrivate, ProfilePublic},
		EgressAllow: []string{"b.example.com", "a.example.com"},
		EgressDeny:  []string{"10.0.0.0/8"},
		DNSServers:  []string{"1.1.1.1:53"},
	}).Describe()
	want := []string{
		"profile: private",
		"profile: public",
		"egress-allow: a.example.com",
		"egress-allow: b.example.com",
		"egress-deny: 10.0.0.0/8",
		"dns-server: 1.1.1.1:53",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

func TestDescribeUnsetPolicyNamesTheDefaultProfile(t *testing.T) {
	got := (Policy{}).Describe()
	if !slices.Equal(got, []string{"profile: " + DefaultProfile.String()}) {
		t.Errorf("Describe(zero) = %q, want the default profile", got)
	}
}
