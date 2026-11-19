package learn

import (
	"context"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// networkFinding returns the network finding from findings, or nil.
func networkFinding(findings []analyser.Finding) *analyser.Finding {
	for i := range findings {
		if findings[i].Kind == analyser.KindObservation && findings[i].Evidence.Channel == "proxy" {
			return &findings[i]
		}
	}
	return nil
}

func egressAllowOf(t *testing.T, finding analyser.Finding) []string {
	t.Helper()
	for _, write := range finding.Writes {
		if write.ConfigKey == "network.egress-allow" {
			allow, ok := write.ConfigValue.([]string)
			if !ok {
				t.Fatalf("egress-allow value = %#v, want []string", write.ConfigValue)
			}
			return allow
		}
	}
	t.Fatal("the network finding carries no egress-allow write")
	return nil
}

// TestFindingsNetworkDiffUsesCurrentNotOverlay pins the never-widen diff: the
// proposed allow set is the pre-overlay Current policy unioned with the used
// destinations, never the channel overlay's proxy address or upstream CA.
func TestFindingsNetworkDiffUsesCurrentNotOverlay(t *testing.T) {
	analysis := Analysis{
		Current: network.Policy{
			Profiles:    []network.Profile{network.ProfileNone},
			EgressAllow: []string{"existing.example.com"},
		},
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example.com", Protocol: "https", Success: true, Count: 2},
		}},
	}

	finding := networkFinding(analysis.Findings())
	if finding == nil {
		t.Fatal("no network finding, want one")
	}
	want := []string{"api.example.com", "existing.example.com"}
	if got := egressAllowOf(t, *finding); !slices.Equal(got, want) {
		t.Errorf("egress-allow = %v, want %v", got, want)
	}
}

// TestFindingsSecretFromConfigEntryFallback pins the config-entry attribution
// fallback: a static credential with no channel attribution is scoped to the
// endpoint its config entry names (readback.Credential.Hosts).
func TestFindingsSecretFromConfigEntryFallback(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{
				Kind:     readback.CredentialStatic,
				Provider: "openai",
				EnvVar:   "OPENAI_API_KEY",
				Hosts:    []string{"api.openai.com"},
			},
		}},
	}

	findings := analysis.Findings()
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one secret finding", findings)
	}
	write := findings[0].Writes[0]
	if write.Secret == nil {
		t.Fatal("the finding carries no secret write")
	}
	if !slices.Equal(write.Secret.Hosts, []string{"api.openai.com"}) {
		t.Errorf("secret hosts = %v, want [api.openai.com]", write.Secret.Hosts)
	}
}

// TestFindingsNoHostNoSecret pins that an unattributable static credential
// yields no finding: no host means no migration.
func TestFindingsNoHostNoSecret(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY"},
		}},
	}

	if findings := analysis.Findings(); len(findings) != 0 {
		t.Errorf("findings = %+v, want none for an unattributable credential", findings)
	}
}

func TestSecretValueResolvesByEnvVar(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: "sk-secret"},
		}},
	}

	value, ok := analysis.SecretValue()(analyser.SecretWrite{EnvVar: "OPENAI_API_KEY"})
	if !ok || value != "sk-secret" {
		t.Errorf("SecretValue = (%q, %v), want (sk-secret, true)", value, ok)
	}
	if _, ok := analysis.SecretValue()(analyser.SecretWrite{EnvVar: "ABSENT_KEY"}); ok {
		t.Error("an unknown env var must not resolve")
	}
}

// TestAnalyseReadsBackAndApplies pins the after-run flow: Analyse reads the home
// back, derives the findings, and hands them to the applier exactly once.
func TestAnalyseReadsBackAndApplies(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	origRead, origApply := readBack, applyFindings
	readBack = func(string, string, readback.Manifest) (readback.ReadBack, error) {
		return readback.ReadBack{}, nil
	}
	var applied []analyser.Finding
	applyFindings = func(findings []analyser.Finding, _ analyser.ApplyOptions) []analyser.ApplyResult {
		applied = findings
		results := make([]analyser.ApplyResult, 0, len(findings))
		for _, finding := range findings {
			results = append(results, analyser.ApplyResult{Finding: finding, Outcome: analyser.OutcomeApplied})
		}
		return results
	}
	t.Cleanup(func() { readBack, applyFindings = origRead, origApply })

	analysis := Analysis{
		Slug:  "proj",
		Agent: "opencode",
		Home:  "/home/vol",
		Observation: channel.Observation{Destinations: []channel.Destination{
			{Host: "api.example.com", Protocol: "https", Success: true, Count: 3},
		}},
		Current: network.Policy{Profiles: []network.Profile{network.ProfilePublic}},
	}

	summary, err := Analyse(context.Background(), analysis, &termiotest.Mock{}, options.ModeRun, nil)
	if err != nil {
		t.Fatalf("Analyse: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("Apply got %d findings, want 1", len(applied))
	}
	if summary.ServicesReached != 1 {
		t.Errorf("ServicesReached = %d, want 1", summary.ServicesReached)
	}
	if len(summary.Results) != 1 {
		t.Errorf("Results = %+v, want one result", summary.Results)
	}
}
