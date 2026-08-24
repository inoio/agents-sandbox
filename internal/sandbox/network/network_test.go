package network

import (
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
}
