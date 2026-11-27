package analyser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// exposedCodexKey is a read-back report of a plain-text key in codex's auth.json.
var exposedCodexKey = readback.ExposedKey{
	Source:           ".codex/auth.json",
	Provider:         "openai",
	EnvVar:           "OPENAI_API_KEY",
	Hosts:            []string{"api.openai.com"},
	Value:            "sk-proj-real",
	ProvisionedStore: `{"auth_mode":"apikey","OPENAI_API_KEY":%q}` + "\n",
}

// exposedClaudeKey is a read-back report of a /login key in claude-code's
// global config, which needs no provisioned file.
var exposedClaudeKey = readback.ExposedKey{
	Source:   ".claude.json",
	Provider: "anthropic",
	EnvVar:   "ANTHROPIC_API_KEY",
	Hosts:    []string{"api.anthropic.com"},
	Value:    "sk-ant-real",
	FollowUp: "Run /logout once.",
}

// provisionedCodexStore is the auth.json the proposal provisions: the secret's
// explicit placeholder in place of the key.
const provisionedCodexStore = `{"auth_mode":"apikey","OPENAI_API_KEY":"${OPENAI_API_KEY}"}` + "\n"

func exposedKeyFindingFor(t *testing.T, key readback.ExposedKey) Finding {
	t.Helper()
	//exhaustruct:ignore // only the exposed key matters
	findings := ObservationFindings(SessionFacts{
		ReadBack: readback.ReadBack{Exposed: []readback.ExposedKey{key}},
		Current:  narrowNone(),
	})
	require.Len(t, findings, 1)
	return findings[0]
}

func TestObservationFindingsMoveAnExposedCodexKeyIntoASecretAndAProvisionedStore(t *testing.T) {
	t.Parallel()

	finding := exposedKeyFindingFor(t, exposedCodexKey)

	require.Equal(t, "An API key is stored in plain text in ~/.codex/auth.json on the home volume.",
		finding.Evidence.Summary)
	require.Equal(t, []string{"api.openai.com"}, finding.Evidence.Destinations)
	require.NotEmpty(t, finding.EvidenceHash)
	require.Len(t, finding.Writes, 2)
	secret := finding.Writes[0]
	require.Equal(t, ScopePerSlugUser, secret.Scope)
	//exhaustruct:ignore // the value is resolved at apply time
	require.Equal(t, &SecretWrite{EnvVar: "OPENAI_API_KEY", Provider: "openai", Hosts: []string{"api.openai.com"}},
		secret.Secret)
	store := finding.Writes[1]
	require.Equal(t, &HomeFileWrite{
		Target:  ".codex/auth.json",
		Name:    "codex-auth.json",
		Content: []byte(provisionedCodexStore),
	}, store.HomeFile)
	for _, write := range finding.Writes {
		require.NotContains(t, write.Text, "sk-proj-real", "the key never reaches the proposal text")
	}
	require.NotContains(t, string(store.HomeFile.Content), "sk-proj-real")
}

func TestObservationFindingsMoveAnExposedClaudeKeyIntoASecretWithItsFollowUp(t *testing.T) {
	t.Parallel()

	finding := exposedKeyFindingFor(t, exposedClaudeKey)

	require.Len(t, finding.Writes, 2)
	require.Equal(t, "ANTHROPIC_API_KEY", finding.Writes[0].Secret.EnvVar)
	require.Nil(t, finding.Writes[0].HomeFile)
	require.True(t, writeOnlyRendersText(finding.Writes[1]))
	require.Equal(t, "Run /logout once.", finding.Writes[1].Text)
}

func TestProvisionedStoreName(t *testing.T) {
	t.Parallel()
	for source, name := range map[string]string{
		".codex/auth.json":      "codex-auth.json",
		"./.codex//auth.json":   "codex-auth.json",
		".config/tool/key.json": "config-tool-key.json",
	} {
		require.Equal(t, name, provisionedStoreName(source), source)
	}
}

// TestApplySavesAnExposedCodexKeyAsASecretAndAKeepLoginHomeEntry applies the
// proposal end to end: the key lands in env.secret.yaml under the placeholder
// the provisioned store carries, and the per-slug config maps auth.json to the
// provisioned copy with keep-login, as homeconfig reads it back.
func TestApplySavesAnExposedCodexKeyAsASecretAndAKeepLoginHomeEntry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	ui, answered := interactiveUI()

	results := Apply([]Finding{exposedKeyFindingFor(t, exposedCodexKey)}, ApplyOptions{
		Paths: cp, Slug: testSlug, Agent: "codex", UI: ui, Mode: options.ModeRun,
		SecretValue: func(write SecretWrite) (string, bool) {
			return exposedCodexKey.Value, write.EnvVar == exposedCodexKey.EnvVar
		},
	})

	require.Equal(t, OutcomeApplied, results[0].Outcome, results[0].Err)
	require.Equal(t, 1, *answered, "the secret and the store are accepted together")
	slugDir := cp.UserSlugConfigDir(testSlug)
	require.Equal(t, []string{slugDir, slugDir}, results[0].WroteDirs)
	info, statErr := os.Stat(filepath.Join(slugDir, "home", "codex-auth.json"))
	require.NoError(t, statErr)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the provisioned store is private")
	specs := envsecret.ParseSecretSpecYAML(filepath.Join(slugDir, "env.secret.yaml"), &termiotest.Mock{})
	require.Equal(t, "sk-proj-real", specs["OPENAI_API_KEY"].Value)
	require.Equal(t, "${OPENAI_API_KEY}", specs["OPENAI_API_KEY"].Placeholder)
	require.Equal(t, []string{"api.openai.com"}, specs["OPENAI_API_KEY"].Hosts)

	files, missing, _, err := homeconfig.BuildHomeFilesWithModes([]string{slugDir}, "/home/dev", nil)
	require.NoError(t, err)
	require.Empty(t, missing)
	provisioned := files["/home/dev/.codex/auth.json"]
	require.JSONEq(t, provisionedCodexStore, string(provisioned.Data))
	require.True(t, provisioned.KeepLogin)
}

func TestApplyRefusesAProjectScopeHomeFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	ui, _ := interactiveUI()
	//exhaustruct:ignore // only the scope and the home file matter
	finding := Finding{Writes: []Write{{
		Scope:    ScopeProject,
		HomeFile: &HomeFileWrite{Target: ".codex/auth.json", Name: "codex-auth.json", Content: []byte("{}")},
	}}}

	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: configpaths.Get(), Slug: testSlug, CheckoutSlug: "checkout", UI: ui, Mode: options.ModeRun,
	})

	require.Equal(t, OutcomeSkipped, results[0].Outcome)
	require.Contains(t, results[0].Note, "per-slug user scope")
}

func TestApplyRefusesAnUneditableHomeConfigBeforeWriting(t *testing.T) {
	for _, entry := range []struct {
		name, config string
	}{
		{"config.json", `{}`},
		{"config.jsonc", `{/* config */}`},
		{"config.json5", `{}`},
		{"config.yaml", "home: [\n"},
		{"config.yml", "- not a mapping\n"},
		{"config.yaml", "home: scalar\n"},
	} {
		t.Run(entry.name+entry.config, func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			paths := configpaths.Get()
			dir := paths.UserSlugConfigDir(testSlug)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			path := filepath.Join(dir, entry.name)
			require.NoError(t, os.WriteFile(path, []byte(entry.config), 0o600))
			ui, _ := interactiveUI()
			results := Apply([]Finding{exposedKeyFindingFor(t, exposedCodexKey)}, ApplyOptions{
				Paths: paths, Slug: testSlug, Agent: "codex", UI: ui, Mode: options.ModeRun,
				SecretValue: func(SecretWrite) (string, bool) { return exposedCodexKey.Value, true },
			})
			require.Equal(t, OutcomeSkipped, results[0].Outcome, "%+v", results[0])
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, entry.config, string(content))
			require.NoFileExists(t, filepath.Join(dir, "env.secret.yaml"))
			require.NoDirExists(t, filepath.Join(dir, "home"))
		})
	}
}

func TestApplyRefusesAnExistingHomeTargetBeforeWriting(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(map[bool]string{false: "per-slug", true: "project"}[project], func(t *testing.T) {
			configpathstest.WithMockConfigPaths(t)
			paths := configpaths.Get()
			slugDir := paths.UserSlugConfigDir(testSlug)
			dir := slugDir
			if project {
				dir = paths.ProjectConfigDir()
			}
			require.NoError(t, os.MkdirAll(dir, 0o700))
			path := filepath.Join(dir, "config.yaml")
			original := "home:\n  ./.codex/auth.json: mine.json\n"
			require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
			ui, _ := interactiveUI()
			results := Apply([]Finding{exposedKeyFindingFor(t, exposedCodexKey)}, ApplyOptions{
				Paths: paths, Slug: testSlug, Agent: "codex", UI: ui, Mode: options.ModeRun,
				SecretValue: func(SecretWrite) (string, bool) { return exposedCodexKey.Value, true },
			})
			require.Equal(t, OutcomeSkipped, results[0].Outcome)
			require.Contains(t, results[0].Note, "already")
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, string(content))
			require.NoFileExists(t, filepath.Join(slugDir, "env.secret.yaml"))
			require.NoDirExists(t, filepath.Join(slugDir, "home"))
		})
	}
}

func TestApplyRefusesAHomeFileNameThatLeavesItsDirectory(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	ui, answered := interactiveUI()
	//exhaustruct:ignore // only the home file matters
	finding := Finding{Writes: []Write{{
		Scope:    ScopePerSlugUser,
		HomeFile: &HomeFileWrite{Target: ".codex/auth.json", Name: "../escape.json", Content: []byte("{}")},
	}}}

	results := Apply([]Finding{finding}, ApplyOptions{
		Paths: paths, Slug: testSlug, UI: ui, Mode: options.ModeRun,
	})

	require.Equal(t, OutcomeSkipped, results[0].Outcome)
	require.Contains(t, results[0].Note, "not a single file name")
	require.Contains(t, results[0].Note, "../escape.json")
	require.Empty(t, results[0].WroteDirs)
	require.Zero(t, *answered)
	require.NoFileExists(t, filepath.Join(paths.UserSlugConfigDir(testSlug), "config.yaml"))
}

func TestApplyFailsAHomeFileItCannotWrite(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	cp := configpaths.Get()
	slugDir := cp.UserSlugConfigDir(testSlug)
	// A file where the home/ directory belongs makes the write fail.
	require.NoError(t, os.MkdirAll(slugDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(slugDir, "home"), nil, 0o600))
	ui, _ := interactiveUI()
	//exhaustruct:ignore // only the home file matters
	finding := Finding{Writes: []Write{{
		Scope:    ScopePerSlugUser,
		HomeFile: &HomeFileWrite{Target: ".codex/auth.json", Name: "codex-auth.json", Content: []byte("{}")},
	}}}

	results := Apply([]Finding{finding}, ApplyOptions{Paths: cp, Slug: testSlug, UI: ui, Mode: options.ModeRun})

	require.Equal(t, OutcomeFailed, results[0].Outcome)
	require.Contains(t, results[0].Err, "write home file home/codex-auth.json")
}
