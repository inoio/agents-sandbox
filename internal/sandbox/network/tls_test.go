package network

import (
	"slices"
	"strings"
	"testing"
)

func TestPolicyFingerprintIncludesTLSInterception(t *testing.T) {
	base := Policy{Profiles: []Profile{ProfileNone}}
	withBypass := base
	withBypass.TLSBypass = []string{"*.internal.example"}
	if base.Fingerprint() == withBypass.Fingerprint() {
		t.Fatal("tls-bypass must change the fingerprint")
	}
	on := base
	on.InterceptTLS = InterceptOn
	if base.Fingerprint() == on.Fingerprint() {
		t.Fatal("intercept-tls must change the fingerprint")
	}
	strict := base
	strict.DisableStrict = true
	if base.Fingerprint() == strict.Fingerprint() {
		t.Fatal("disable-strict must change the fingerprint")
	}
}

func TestPolicyFingerprintTreatsEmptyInterceptModeAsAuto(t *testing.T) {
	unset := Policy{Profiles: []Profile{ProfileNone}}
	auto := unset
	auto.InterceptTLS = InterceptAuto
	if unset.Fingerprint() != auto.Fingerprint() {
		t.Fatal("unset and explicit auto must fingerprint the same")
	}
}

// TestPolicyFingerprintIgnoresTLSBypassUnderInterceptOff pins that a bypass host
// has no fingerprint effect when interception is off: networkConfig drops the
// bypass then, so it never reaches the VM and must not force a recreate.
func TestPolicyFingerprintIgnoresTLSBypassUnderInterceptOff(t *testing.T) {
	off := Policy{Profiles: []Profile{ProfileNone}, InterceptTLS: InterceptOff}
	withBypass := off
	withBypass.TLSBypass = []string{"*.internal.example"}
	if off.Fingerprint() != withBypass.Fingerprint() {
		t.Fatalf("tls-bypass must not change the fingerprint under intercept-tls off: %q vs %q",
			off.Fingerprint(), withBypass.Fingerprint())
	}
}

func TestParseInterceptModeRejectsUnknown(t *testing.T) {
	if _, err := ParseInterceptMode("sometimes"); err == nil {
		t.Fatal("want error for an unknown mode")
	}
	for in, want := range map[string]InterceptMode{"": InterceptAuto, "auto": InterceptAuto, "on": InterceptOn, "off": InterceptOff} {
		got, err := ParseInterceptMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseInterceptMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestValidateTLSBypassRejectsEmpty(t *testing.T) {
	if _, err := ValidateTLSBypass([]string{""}); err == nil {
		t.Fatal("want error for an empty bypass entry")
	}
	got, err := ValidateTLSBypass([]string{" example.com ", "*.internal"})
	if err != nil || len(got) != 2 || got[0] != "example.com" {
		t.Fatalf("ValidateTLSBypass = %v, %v", got, err)
	}
}

func TestDescribeListsTLSInterceptionSettings(t *testing.T) {
	got := (Policy{
		Profiles:      []Profile{ProfileNone},
		InterceptTLS:  InterceptOff,
		TLSBypass:     []string{"b.example.com", "a.example.com"},
		DisableStrict: true,
	}).Describe()
	want := []string{
		"profile: none",
		"intercept-tls: off",
		"tls-bypass: a.example.com",
		"tls-bypass: b.example.com",
		"disable-strict: true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

func TestDescribeOmitsDisableStrictWhenUnset(t *testing.T) {
	for _, line := range (Policy{Profiles: []Profile{ProfileNone}}).Describe() {
		if line == "disable-strict: true" {
			t.Fatal("disable-strict must not be described when unset")
		}
	}
}

// TestDescribeOmitsAutoInterceptMode pins that an auto interception mode is not
// displayed: it is microsandbox's own default and unset means auto, so showing
// it would change every existing policy's persisted Entries for no verdict
// change.
func TestDescribeOmitsAutoInterceptMode(t *testing.T) {
	for in, policy := range map[string]Policy{
		"unset": {Profiles: []Profile{ProfileNone}},
		"auto":  {Profiles: []Profile{ProfileNone}, InterceptTLS: InterceptAuto},
	} {
		for _, line := range policy.Describe() {
			if strings.HasPrefix(line, "intercept-tls:") {
				t.Fatalf("Describe(%s) = %q, want no intercept-tls line", in, line)
			}
		}
	}
}
