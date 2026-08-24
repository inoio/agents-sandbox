package network

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestConfigNoneAirgap(t *testing.T) {
	cfg, err := (Policy{Profile: ProfileNone}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DefaultEgress != msbSdk.PolicyActionDeny || cfg.DefaultIngress != msbSdk.PolicyActionDeny {
		t.Fatalf(
			"none must deny both egress and ingress, got egress=%v ingress=%v",
			cfg.DefaultEgress,
			cfg.DefaultIngress,
		)
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

func TestConfigNoneIgnoresLists(t *testing.T) {
	cfg, err := (Policy{
		Profile:     ProfileNone,
		EgressAllow: []string{"1.1.1.1"},
	}).Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if len(cfg.Rules) != 0 {
		t.Fatalf("none must ignore egress lists, got %d rules", len(cfg.Rules))
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
