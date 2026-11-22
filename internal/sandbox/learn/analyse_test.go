package learn

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/analyser"
	"github.com/inoio/agents-sandbox/internal/sandbox/channel"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
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

	finding := networkFinding(analysis.Findings(nil))
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
				Value:    "sk-secret",
			},
		}},
	}

	findings := analysis.Findings(nil)
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
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: "sk-secret"},
		}},
	}

	if findings := analysis.Findings(nil); len(findings) != 0 {
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

// TestAnalysisFindingsIncludeManagedConfig pins that Findings appends the
// managed-config findings for the session's read-back: a changed, non-credential
// config file the agent authored yields a finding whose sanitized write is the
// agent config, so the after-run proposes copying it back.
func TestAnalysisFindingsIncludeManagedConfig(t *testing.T) {
	const configPath = ".config/opencode/opencode.jsonc"
	const secret = "sk-live-secret"
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, configPath)), 0o750))
	testutil.WriteFile(t, home, configPath, `{
  "provider": { "openai": { "apiKey": "`+secret+`" } }
}
`)

	analysis := Analysis{
		Agent: "opencode2",
		Home:  home,
		ReadBack: readback.ReadBack{
			ConfigPaths: []string{configPath},
			Changed:     []string{configPath},
			Static: []readback.Credential{{
				Kind:     readback.CredentialStatic,
				Source:   configPath,
				Provider: "openai",
				EnvVar:   "OPENAI_API_KEY",
				Hosts:    []string{"api.openai.com"},
				Value:    secret,
			}},
		},
	}

	findings := analysis.Findings(nil)

	var managed *analyser.Finding
	for i := range findings {
		if len(findings[i].Writes) == 0 {
			continue
		}
		if findings[i].Writes[0].ManagedConfig != nil {
			managed = &findings[i]
		}
	}
	if managed == nil {
		t.Fatalf("findings = %+v, want a managed-config finding", findings)
	}
	written := managed.Writes[0].ManagedConfig
	if written.Name != "opencode.jsonc" {
		t.Errorf("managed config name = %q, want opencode.jsonc", written.Name)
	}
	placeholder := envsecret.PlaceholderFor("OPENAI_API_KEY")
	if !strings.Contains(string(written.Content), placeholder) {
		t.Errorf("managed config = %q, want the %s placeholder", written.Content, placeholder)
	}
	if strings.Contains(string(written.Content), secret) {
		t.Errorf("managed config = %q, want no secret value", written.Content)
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

// TestSummarizeDoesNotCountADeniedHostsTunnel: a host whose request was denied
// still has an accepted CONNECT tunnel; like the egress lock, the summary does
// not count it as reached.
func TestSummarizeDoesNotCountADeniedHostsTunnel(t *testing.T) {
	analysis := Analysis{Observation: channel.Observation{Destinations: []channel.Destination{
		{Host: "dead.example", Protocol: channel.ConnectProtocol, Success: true, Count: 1},
		{Host: "dead.example", Protocol: "https", Denied: true, Count: 1},
		{Host: "api.example", Protocol: "https", Success: true, Count: 1},
	}}}

	summary := summarize(analysis, nil)

	if summary.ServicesReached != 1 || len(summary.Denied) != 1 {
		t.Errorf("summary = %+v, want api.example reached and dead.example only denied", summary)

// TestSummarizeCarriesTheProxyLogAndNamesFailedHandshakes: a tunnel whose every
// handshake failed still counts as reached (a pinned client looks the same),
// but the summary names its host, and the session's proxy log.
func TestSummarizeCarriesTheProxyLogAndNamesFailedHandshakes(t *testing.T) {
	analysis := Analysis{Observation: channel.Observation{
		Destinations: []channel.Destination{
			{Host: "pinned.example", Protocol: channel.ConnectProtocol, Success: true, Count: 1, Failed: 1},
			{Host: "api.example", Protocol: "https", Success: true, Count: 1},
		},
		ProxyLog:      "/state/learn/x/1.proxy.log",
		ProxyWarnings: 3,
	}}

	summary := summarize(analysis, nil)

	if summary.ServicesReached != 2 {
		t.Errorf("ServicesReached = %d, want 2", summary.ServicesReached)
	}
	if len(summary.HandshakeFailed) != 1 || summary.HandshakeFailed[0] != "pinned.example" {
		t.Errorf("HandshakeFailed = %v, want [pinned.example]", summary.HandshakeFailed)
	}
	if summary.ProxyLog != "/state/learn/x/1.proxy.log" || summary.ProxyWarnings != 3 {
		t.Errorf("summary = %+v, want the proxy log and its warning count", summary)
	}
}

// TestSummarizeLeavesOutAddressOnlyTunnels: a tunnel still recorded under an IP
// carried no attributable request, so it is not a service the session reached,
// matching what the egress lock proposes.
func TestSummarizeLeavesOutAddressOnlyTunnels(t *testing.T) {
	analysis := Analysis{Observation: channel.Observation{Destinations: []channel.Destination{
		{Host: "160.79.104.10", IP: "160.79.104.10", Protocol: channel.ConnectProtocol, Success: true, Count: 1},
		{Host: "api.example", Protocol: "https", Success: true, Count: 1},
	}}}

	summary := summarize(analysis, nil)

	if summary.ServicesReached != 1 {
		t.Errorf("ServicesReached = %d, want 1 (the address-only tunnel is not a service)", summary.ServicesReached)
	}
}

// TestSummarizeCountsServicesDeniedAndKeys pins summarize's counting: a blank
// host is skipped even when denied (it must not reach the Denied note), a denied
// destination lands in Denied (never ServicesReached), a successful one in
// ServicesReached, and KeysUsed counts only applied, non-removing static-secret
// writes — a skipped finding and a removal do not count, and a non-secret write
// never does.
func TestSummarizeCountsServicesDeniedAndKeys(t *testing.T) {
	analysis := Analysis{Observation: channel.Observation{Destinations: []channel.Destination{
		{Host: "", Denied: true},
		{Host: "denied.example", Denied: true},
		{Host: "blocked.example", Denied: true},
		{Host: "ok.example", Success: true},
	}}}
	results := []analyser.ApplyResult{
		{
			Outcome: analyser.OutcomeSkipped,
			Finding: analyser.Finding{Writes: []analyser.Write{
				{Secret: &analyser.SecretWrite{EnvVar: "SKIPPED_KEY"}},
			}},
		},
		{
			Outcome: analyser.OutcomeApplied,
			Finding: analyser.Finding{Writes: []analyser.Write{
				{ConfigKey: "network.egress-allow"},
				{Secret: &analyser.SecretWrite{EnvVar: "OPENAI_API_KEY"}},
				{Secret: &analyser.SecretWrite{EnvVar: "REMOVED_KEY", Remove: true}},
			}},
		},
	}

	summary := summarize(analysis, results)

	if summary.ServicesReached != 1 {
		t.Errorf("ServicesReached = %d, want 1 (the blank host is skipped)", summary.ServicesReached)
	}
	if want := []string{"blocked.example", "denied.example"}; !slices.Equal(summary.Denied, want) {
		t.Errorf("Denied = %v, want %v sorted", summary.Denied, want)
	}
	if summary.KeysUsed != 1 {
		t.Errorf("KeysUsed = %d, want 1 (only the applied, non-removing secret)", summary.KeysUsed)
	}
	if len(summary.Results) != len(results) {
		t.Errorf("Results = %+v, want the results passed through", summary.Results)
	}
}

// TestSecretValueEmptyValueDoesNotResolve pins that a matched credential with an
// empty value does not resolve: a static write must never be applied with a
// blank secret.
func TestSecretValueEmptyValueDoesNotResolve(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Kind: readback.CredentialStatic, EnvVar: "EMPTY_KEY", Value: ""},
		}},
	}

	value, ok := analysis.SecretValue()(analyser.SecretWrite{EnvVar: "EMPTY_KEY"})
	if ok || value != "" {
		t.Errorf("SecretValue = (%q, %v), want (\"\", false) for an empty value", value, ok)
	}
}

// TestSecretValuePrefersANonEmptyCredential pins that SecretValue resolves the
// first same-env-var credential that carries a value, skipping an earlier empty
// one, and still reports no value when every match is empty.
func TestSecretValuePrefersANonEmptyCredential(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: ""},
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: "sk-real"},
		}},
	}

	value, ok := analysis.SecretValue()(analyser.SecretWrite{EnvVar: "OPENAI_API_KEY"})
	if !ok || value != "sk-real" {
		t.Errorf("SecretValue = (%q, %v), want (sk-real, true) past the empty source", value, ok)
	}

	allEmpty := Analysis{
		ReadBack: readback.ReadBack{Static: []readback.Credential{
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: ""},
			{Kind: readback.CredentialStatic, EnvVar: "OPENAI_API_KEY", Value: ""},
		}},
	}
	if value, ok := allEmpty.SecretValue()(analyser.SecretWrite{EnvVar: "OPENAI_API_KEY"}); ok || value != "" {
		t.Errorf("SecretValue = (%q, %v), want (\"\", false) when every match is empty", value, ok)
	}
}

// TestAnalyseDerivesSecretFindingFromReadBack pins that Analyse threads the
// read-back it just read into the findings: a static credential with hosts must
// yield the derived secret finding. If the assignment were dropped the findings
// would derive from a zero ReadBack and none would appear.
func TestAnalyseDerivesSecretFindingFromReadBack(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	origRead, origApply := readBack, applyFindings
	readBack = func(string, string, readback.Manifest) (readback.ReadBack, error) {
		return readback.ReadBack{Static: []readback.Credential{
			{
				Kind:     readback.CredentialStatic,
				Provider: "openai",
				EnvVar:   "OPENAI_API_KEY",
				Hosts:    []string{"api.openai.com"},
				Value:    "sk-secret",
			},
		}}, nil
	}
	var applied []analyser.Finding
	applyFindings = func(findings []analyser.Finding, _ analyser.ApplyOptions) []analyser.ApplyResult {
		applied = findings
		return nil
	}
	t.Cleanup(func() { readBack, applyFindings = origRead, origApply })

	_, err := Analyse(context.Background(), Analysis{
		Slug:  "proj",
		Agent: "opencode",
		Home:  "/home/vol",
	}, &termiotest.Mock{}, options.ModeRun, nil)
	if err != nil {
		t.Fatalf("Analyse: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied findings = %+v, want the derived secret finding", applied)
	}
	if applied[0].Writes[0].Secret == nil || applied[0].Writes[0].Secret.EnvVar != "OPENAI_API_KEY" {
		t.Errorf("finding = %+v, want the OPENAI_API_KEY secret write", applied[0])
	}
}

// TestSecretValueResolvesAnExposedLoginKey pins that a key a harness's login
// stored in plain text resolves for the secret it moves into, skipping an
// exposed key of another env var and one without a value.
func TestSecretValueResolvesAnExposedLoginKey(t *testing.T) {
	analysis := Analysis{
		ReadBack: readback.ReadBack{Exposed: []readback.ExposedKey{
			{EnvVar: "ANTHROPIC_API_KEY", Value: "sk-ant"},
			{EnvVar: "OPENAI_API_KEY", Value: ""},
			{EnvVar: "OPENAI_API_KEY", Value: "sk-proj"},
		}},
	}

	value, ok := analysis.SecretValue()(analyser.SecretWrite{EnvVar: "OPENAI_API_KEY"})
	if !ok || value != "sk-proj" {
		t.Errorf("SecretValue = (%q, %v), want (sk-proj, true)", value, ok)
	}
}
