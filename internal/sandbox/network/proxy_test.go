package network

import (
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

func TestParseProxy(t *testing.T) {
	for _, tc := range []struct {
		in       string
		protocol ProxyProtocol
		address  string
	}{
		{in: "socks5://10.0.0.1:1080", protocol: ProxySOCKS5, address: "10.0.0.1:1080"},
		{in: "http-connect://proxy.example.com:3128", protocol: ProxyHTTPConnect, address: "proxy.example.com:3128"},
		{in: "socks4://10.0.0.1:1080", protocol: ProxySOCKS4, address: "10.0.0.1:1080"},
		{in: "  socks5://10.0.0.1:1080  ", protocol: ProxySOCKS5, address: "10.0.0.1:1080"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseProxy(tc.in)
			if err != nil {
				t.Fatalf("ParseProxy(%q): %v", tc.in, err)
			}
			if got.Protocol != tc.protocol || got.Address != tc.address {
				t.Fatalf("ParseProxy(%q) = %+v, want protocol %q address %q", tc.in, got, tc.protocol, tc.address)
			}
			if got.Username != "" || got.PasswordEnv != "" {
				t.Fatalf("ParseProxy(%q) must not invent credentials, got %+v", tc.in, got)
			}
		})
	}
}

func TestParseProxyRejectsMalformed(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"10.0.0.1:1080",              // no scheme
		"ftp://10.0.0.1:1080",        // unknown protocol
		"socks5://10.0.0.1",          // no port
		"socks5://10.0.0.1:",         // empty port
		"socks5://:1080",             // no host
		"socks5://10.0.0.1:0",        // port below range
		"socks5://10.0.0.1:65536",    // port above range
		"socks5://10.0.0.1:notaport", // non-numeric port
		"socks5://10.0.0.1:53:54",    // too many colons
	} {
		t.Run(in, func(t *testing.T) {
			if got, err := ParseProxy(in); err == nil {
				t.Fatalf("ParseProxy(%q) = %+v, want an error", in, got)
			}
		})
	}
}

// TestProxyCredentialsRejectedForSOCKS4 pins that the SDK carries no credential
// support for SOCKS4, so a configuration supplying one fails fast at config
// time rather than at VM creation.
func TestProxyCredentialsRejectedForSOCKS4(t *testing.T) {
	policy := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxySOCKS4, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "P",
	}}
	if err := policy.Validate(); err == nil {
		t.Fatal("socks4 credentials must be rejected")
	}
}

// TestProxyRejectsLoneCredential pins that a username and a password-env must
// be set together: the SDK's Credentials needs both, so a lone credential would
// otherwise reach the VM as an anonymous proxy while still changing the
// fingerprint — two policies reaching the VM identically that fingerprint
// differently. The rejection keeps the emission-faithful invariant by failing
// the misconfiguration before it can reach the VM.
func TestProxyRejectsLoneCredential(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy Proxy
	}{
		{"username only", Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u"}},
		{"password-env only", Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", PasswordEnv: "P"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := Policy{Profiles: []Profile{ProfileNone}, Proxy: tc.proxy}
			err := policy.Validate()
			if err == nil {
				t.Fatal("a lone proxy credential must be rejected")
			}
			for _, want := range []string{"username", "password-env"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q must name %q", err, want)
				}
			}
			if _, err := policy.Spec(); err == nil {
				t.Fatal("Spec must reject a lone proxy credential before any VM work")
			}
		})
	}
}

// TestProxyAddressMustBeReachableUnderNone pins S1's recorded composition
// decision: the runtime dials the proxy host-side, so the dial bypasses the
// egress policy and the launcher neither auto-adds the proxy address to the
// allow set nor fails on a policy that omits it. Under `none` a well-formed
// proxy is therefore accepted and its address stays out of egress-allow.
func TestProxyAddressMustBeReachableUnderNone(t *testing.T) {
	policy := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080"}}
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate under none = %v, want nil (host-side dial bypasses egress)", err)
	}
	if allowed := slices.Contains(policy.Effective().EgressAllow, "10.0.0.1:1080"); allowed {
		t.Fatal("the proxy address must not be auto-added to egress-allow")
	}
}

func TestProxyChangesFingerprint(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfileNone}}
	withProxy := base
	withProxy.Proxy = Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080"}
	if base.Fingerprint() == withProxy.Fingerprint() {
		t.Fatal("proxy must change the fingerprint")
	}
}

// TestProxyFieldsChangeFingerprint pins that every proxy field that reaches the
// VM contributes to the digest: a changed protocol, address, username, or
// password-env must force a recreate.
func TestProxyFieldsChangeFingerprint(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "P",
	}}
	for _, tc := range []struct {
		name  string
		proxy Proxy
	}{
		{"protocol", Proxy{Protocol: ProxyHTTPConnect, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "P"}},
		{"address", Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.2:1080", Username: "u", PasswordEnv: "P"}},
		{"username", Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "other", PasswordEnv: "P"}},
		{"password-env", Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "Q"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			other.Proxy = tc.proxy
			if base.Fingerprint() == other.Fingerprint() {
				t.Fatalf("changing proxy %s must change the fingerprint", tc.name)
			}
		})
	}
}

// TestProxyZeroValueKeepsFingerprintStable pins that an unset proxy contributes
// no digest lines, so every existing (proxy-less) VM keeps its network hash and
// is not recreated merely because the field was added.
func TestProxyZeroValueKeepsFingerprintStable(t *testing.T) {
	if (Policy{Profiles: []Profile{ProfileNone}}).Fingerprint() !=
		(Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{}}).Fingerprint() {
		t.Fatal("an explicitly zero proxy must fingerprint like an unset one")
	}
}

// TestSpecCarriesProxy pins that Spec projects the configured proxy into the
// launcher network spec the adapter emits.
func TestSpecCarriesProxy(t *testing.T) {
	spec, err := (Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "PW",
	}}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	want := msb.ProxySpec{
		Protocol: msb.ProxySOCKS5, Address: "10.0.0.1:1080", Username: "u", PasswordEnv: "PW",
	}
	if spec.Proxy != want {
		t.Fatalf("Spec.Proxy = %+v, want %+v", spec.Proxy, want)
	}
}

// TestSpecNormalizesProxy pins that Spec emits the protocol parsed and the
// address trimmed, so a whitespace-padded configuration reaches the VM as its
// canonical form.
func TestSpecNormalizesProxy(t *testing.T) {
	spec, err := (Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxyProtocol("  socks5  "), Address: " 10.0.0.1:1080 ",
	}}).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if spec.Proxy.Protocol != msb.ProxySOCKS5 || spec.Proxy.Address != "10.0.0.1:1080" {
		t.Fatalf("Spec.Proxy = %+v, want normalized socks5 / 10.0.0.1:1080", spec.Proxy)
	}
}

// TestFingerprintProxyEmissionFaithful pins that the fingerprint hashes what
// Spec emits, not the raw configuration: two proxies that reach the VM
// identically (a whitespace-padded one and its canonical form) fingerprint
// alike and never force a spurious recreate.
func TestFingerprintProxyEmissionFaithful(t *testing.T) {
	padded := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxyProtocol(" socks5 "), Address: " 10.0.0.1:1080 ",
	}}
	clean := Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxySOCKS5, Address: "10.0.0.1:1080",
	}}
	if padded.Fingerprint() != clean.Fingerprint() {
		t.Fatal("proxies that reach the VM identically must fingerprint alike")
	}
}

func TestProxyDescribe(t *testing.T) {
	got := (Policy{
		Profiles: []Profile{ProfileNone},
		Proxy:    Proxy{Protocol: ProxyHTTPConnect, Address: "proxy.example.com:3128", Username: "u", PasswordEnv: "PW"},
	}).Describe()
	want := []string{
		"profile: none",
		"proxy: http-connect://proxy.example.com:3128",
		"proxy-username: u",
		"proxy-password-env: PW",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

func TestPolicyValidateProxy(t *testing.T) {
	valid := []Policy{
		{Profiles: []Profile{ProfileNone}},
		{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1:1080"}},
		{
			Profiles: []Profile{ProfileNone},
			Proxy:    Proxy{Protocol: ProxyHTTPConnect, Address: "10.0.0.1:3128", Username: "u", PasswordEnv: "P"},
		},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("Policy%+v.Validate() = %v, want nil", p, err)
		}
	}

	invalid := []Policy{
		{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxyProtocol("ftp"), Address: "10.0.0.1:1080"}},
		{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxySOCKS5}},
		{Profiles: []Profile{ProfileNone}, Proxy: Proxy{Protocol: ProxySOCKS5, Address: "10.0.0.1"}},
		{
			Profiles: []Profile{ProfileNone},
			Proxy:    Proxy{Protocol: ProxySOCKS4, Address: "10.0.0.1:1080", PasswordEnv: "P"},
		},
	}
	for _, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("Policy%+v.Validate() = nil, want an error", p)
		}
	}
}

// TestProxySOCKS4CredentialErrorNamesProtocol pins the fail-fast message: the
// error must name SOCKS4 so a user can map it to the protocol's missing
// credential support rather than to a runtime failure.
func TestProxySOCKS4CredentialErrorNamesProtocol(t *testing.T) {
	err := (Policy{Profiles: []Profile{ProfileNone}, Proxy: Proxy{
		Protocol: ProxySOCKS4, Address: "10.0.0.1:1080", Username: "u",
	}}).Validate()
	if err == nil {
		t.Fatal("socks4 with a username must be rejected")
	}
	if !strings.Contains(err.Error(), "socks4") {
		t.Fatalf("error %q must name the socks4 protocol", err)
	}
}
