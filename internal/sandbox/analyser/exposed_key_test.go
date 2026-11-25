package analyser

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// exposedCodexKey is a read-back report of a plain-text key in codex's auth.json.
var exposedCodexKey = readback.ExposedKey{
	Source:   ".codex/auth.json",
	Provider: "openai",
	EnvVar:   "OPENAI_API_KEY",
	Hosts:    []string{"api.openai.com"},
	Remedy:   "then log in again with the placeholder",
}

func TestObservationFindingsReportAnExposedKey(t *testing.T) {
	t.Parallel()
	//exhaustruct:ignore // only the exposed key matters
	facts := SessionFacts{
		ReadBack: readback.ReadBack{Exposed: []readback.ExposedKey{exposedCodexKey}},
		Current:  narrowNone(),
	}

	findings := ObservationFindings(facts)

	require.Len(t, findings, 1)
	finding := findings[0]
	require.Equal(t, "An API key is stored in plain text in ~/.codex/auth.json on the home volume.",
		finding.Evidence.Summary)
	require.Equal(t, []string{"api.openai.com"}, finding.Evidence.Destinations)
	require.Equal(t, evidenceChannelReadback, finding.Evidence.Channel)
	require.NotEmpty(t, finding.EvidenceHash)
	require.Len(t, finding.Writes, 1)
	write := finding.Writes[0]
	require.True(t, writeOnlyRendersText(write), "the guidance makes no machine change")
	require.Equal(t, "Keep it on the host: declare OPENAI_API_KEY as an env secret for api.openai.com, "+
		"then log in again with the placeholder.", write.Text)
}

func TestApplyReportsAnExposedKeyWithoutAsking(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui, answered := interactiveUI()
	findings := exposedKeyFindings([]readback.ExposedKey{exposedCodexKey})

	results := Apply(findings, ApplyOptions{
		Paths: configpaths.Get(), Slug: testSlug, Agent: "codex", UI: ui, Mode: options.ModeRun,
	})

	require.Len(t, results, 1)
	require.Equal(t, OutcomeNotApplicable, results[0].Outcome)
	require.Zero(t, *answered, "a report asks nothing")
	require.Empty(t, results[0].WroteDirs)
}
