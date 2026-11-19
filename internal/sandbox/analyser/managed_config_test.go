package analyser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// managedConfigTestPath is the opencode2 config path a default-form candidate
// carries.
const managedConfigTestPath = ".config/opencode/opencode.jsonc"

// writeManagedConfig writes content to rel under home, creating parent
// directories.
func writeManagedConfig(t *testing.T, home, rel, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, rel)), 0o750))
	testutil.WriteFile(t, home, rel, content)
}

// testStaticCredential builds one static credential sourced from source.
func testStaticCredential(source, envVar, value string) readback.Credential {
	return readback.Credential{
		Kind:     readback.CredentialStatic,
		Source:   source,
		Provider: "openai",
		EnvVar:   envVar,
		Value:    value,
	}
}

// changedConfigBack builds a read-back whose single config path changed.
func changedConfigBack(configPath string, static ...readback.Credential) readback.ReadBack {
	return readback.ReadBack{
		ConfigPaths: []string{configPath},
		Changed:     []string{configPath},
		Static:      static,
	}
}

// TestManagedConfigReplacesStaticSecretValues pins the default write-back form:
// every occurrence of a static credential's value in a changed config file is
// replaced by its ${ENVVAR} placeholder, so the value never enters the finding.
func TestManagedConfigReplacesStaticSecretValues(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-live-secret"
	writeManagedConfig(t, home, managedConfigTestPath, `{
  // the session's own provider entry
  "provider": { "openai": { "apiKey": "`+secret+`" } }
}
`)

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
	})

	require.Len(t, findings, 1)
	require.Equal(t, KindObservation, findings[0].Kind)
	require.NotEmpty(t, findings[0].EvidenceHash)
	write := findings[0].Writes[0]
	require.Equal(t, ScopePerSlugUser, write.Scope)
	require.NotNil(t, write.ManagedConfig)
	require.Equal(t, "opencode.jsonc", write.ManagedConfig.Name)
	require.Contains(t, string(write.ManagedConfig.Content), "${OPENAI_API_KEY}")
	require.NotContains(t, string(write.ManagedConfig.Content), secret)
}

// TestManagedConfigSkipsLoginStores pins that a changed login store is never
// copied back: the store is a credential the agent refreshes, not a config file
// it authored, so no finding is emitted for it.
func TestManagedConfigSkipsLoginStores(t *testing.T) {
	home := t.TempDir()
	const loginPath = ".claude/settings.json"
	writeManagedConfig(t, home, loginPath, `{"oauthAccount": {"accessToken": "sk-login"}}`)

	readBack := readback.ReadBack{
		ConfigPaths: []string{loginPath},
		Changed:     []string{loginPath},
		Login: []readback.Credential{{
			Kind: readback.CredentialLogin, Source: loginPath, Provider: "claude-code",
		}},
	}

	findings := ManagedConfigFindings(ManagedConfigFacts{Home: home, Agent: "claude-code", ReadBack: readBack})

	require.Empty(t, findings)
}

// TestManagedConfigSkipsUnreadable pins that a candidate the write-back cannot
// read whole — a missing file or one over the read-back's cap — is reported
// through the logger and yields no finding, so a truncated or empty file is
// never written back.
func TestManagedConfigSkipsUnreadable(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		home := t.TempDir()
		logger := &termiotest.Mock{}

		findings := ManagedConfigFindings(ManagedConfigFacts{
			Home:     home,
			Agent:    "opencode2",
			ReadBack: changedConfigBack(managedConfigTestPath),
			Logger:   logger,
		})

		require.Empty(t, findings)
		require.NotEmpty(t, logger.WarnCalls, "a skipped candidate must be reported")
	})

	t.Run("over cap", func(t *testing.T) {
		home := t.TempDir()
		logger := &termiotest.Mock{}
		writeManagedConfig(t, home, managedConfigTestPath, strings.Repeat("a", readback.MaxHashedBytes+1))

		findings := ManagedConfigFindings(ManagedConfigFacts{
			Home:     home,
			Agent:    "opencode2",
			ReadBack: changedConfigBack(managedConfigTestPath),
			Logger:   logger,
		})

		require.Empty(t, findings)
		require.NotEmpty(t, logger.WarnCalls, "an over-cap candidate must be reported")
	})
}

// TestManagedConfigOnlyChangedFiles pins the candidate selection: only a config
// path the read-back saw change is written back. An unchanged config path and a
// changed key that is not a config path (a credential probe, keyed
// "<agent>:<probe>") both yield no finding.
func TestManagedConfigOnlyChangedFiles(t *testing.T) {
	home := t.TempDir()
	const unchangedPath = ".config/opencode/AGENTS.md"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"model\": \"sonnet\"\n}\n")
	writeManagedConfig(t, home, unchangedPath, "# unchanged\n")

	readBack := readback.ReadBack{
		ConfigPaths: []string{unchangedPath, managedConfigTestPath},
		Changed:     []string{"opencode2:opencode2-sqlite", managedConfigTestPath},
	}

	findings := ManagedConfigFindings(ManagedConfigFacts{Home: home, Agent: "opencode2", ReadBack: readBack})

	require.Len(t, findings, 1)
	require.Equal(t, "opencode.jsonc", findings[0].Writes[0].ManagedConfig.Name)
}

// TestManagedConfigNeverCarriesAValue pins invariant 3: no field of the finding
// — evidence, write text, target name, or content — holds the static value.
func TestManagedConfigNeverCarriesAValue(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-never-persisted"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \""+secret+"\"\n}\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
	})

	require.Len(t, findings, 1)
	finding := findings[0]
	passwordFields := []string{
		finding.Evidence.Summary,
		strings.Join(finding.Evidence.Destinations, "\n"),
		strings.Join(finding.Evidence.Denied, "\n"),
		finding.Evidence.Channel,
		strings.Join(finding.Evidence.HeaderFields, "\n"),
		finding.Writes[0].ConfigKey,
		finding.Writes[0].Text,
		finding.Writes[0].ManagedConfig.Name,
		string(finding.Writes[0].ManagedConfig.Content),
	}
	for _, field := range passwordFields {
		require.NotContains(t, field, secret)
	}
}

// TestManagedConfigSkipsAValueItCannotReplace pins the safety net: when a static
// value survives the substitution — here a native-form value that is not the
// right-hand side of an assignment — the file is reported and no finding is
// emitted, so a value never reaches the finding by omission.
func TestManagedConfigSkipsAValueItCannotReplace(t *testing.T) {
	home := t.TempDir()
	const configPath = ".kimi-code/config.toml"
	const secret = "sk-kimi-secret"
	writeManagedConfig(t, home, configPath, "# note that merely mentions "+secret+"\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "kimi-code",
		ReadBack: changedConfigBack(
			configPath,
			testStaticCredential(configPath, "MOONSHOT_API_KEY", secret),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "a value the substitution could not replace must be reported")
}

// TestManagedConfigSkipsCredentialStoreForAnyAgent pins invariant 2 for a store
// scoped to another agent: opencode2 inherits and provisions
// .local/share/opencode/auth.json, whose login rule is scoped to agent
// "opencode", so it is absent from Login[] yet present in ConfigPaths. The
// agent-independent store predicate must still refuse it, or the raw credential
// store would be copied back.
func TestManagedConfigSkipsCredentialStoreForAnyAgent(t *testing.T) {
	home := t.TempDir()
	const authPath = ".local/share/opencode/auth.json"
	writeManagedConfig(t, home, authPath, `{"access_token": "sk-store"}`)

	readBack := readback.ReadBack{
		ConfigPaths: []string{authPath},
		Changed:     []string{authPath},
		// Deliberately no Login entry: the store is scoped to agent "opencode",
		// so opencode2's read-back does not classify it a login.
	}

	findings := ManagedConfigFindings(ManagedConfigFacts{Home: home, Agent: "opencode2", ReadBack: readBack})

	require.Empty(t, findings)
}

// TestManagedConfigSkipsAnEscapedStaticValue pins the decode-based safety net: a
// value stored in its escaped form (JSON's \" here, because the value holds a
// quote) is missed by the literal substitution and by a literal search, so only
// the decoded check stops it from reaching the finding.
func TestManagedConfigSkipsAnEscapedStaticValue(t *testing.T) {
	home := t.TempDir()
	const secret = `sk-a"b`
	// The file stores the quote escaped, so the raw bytes hold `sk-a\"b`.
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \"sk-a\\\"b\"\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an escaped value the substitution missed must be reported")
}

// TestManagedConfigRefusesAValueFromAnotherSource pins the global retention
// check: a candidate is any file the read-back's glob declared (opencode2's
// .config/opencode/* includes AGENTS.md), while substitution is per-file. A
// static value classified from a different file can therefore appear verbatim
// in the candidate; the whole static set, not just the candidate's own
// credentials, must refuse it.
func TestManagedConfigRefusesAValueFromAnotherSource(t *testing.T) {
	home := t.TempDir()
	const sourcePath = ".config/opencode/opencode.jsonc"
	const candidatePath = ".config/opencode/AGENTS.md"
	const secret = "sk-cross-source"
	writeManagedConfig(t, home, candidatePath, "Example: pass the key "+secret+" to the tool.\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: readback.ReadBack{
			ConfigPaths: []string{candidatePath},
			Changed:     []string{candidatePath},
			Static:      []readback.Credential{testStaticCredential(sourcePath, "OPENAI_API_KEY", secret)},
		},
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "a value from another source that survives must be reported")
}

// TestManagedConfigRefusesACredentialWithNoEnvVar pins the empty-env-var
// refusal: a static credential the read-back could not name cannot be replaced
// by a placeholder, so its file yields no finding rather than the raw value.
func TestManagedConfigRefusesACredentialWithNoEnvVar(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-unscoped"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \""+secret+"\"\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "opencode2",
		ReadBack: changedConfigBack(managedConfigTestPath, testStaticCredential(managedConfigTestPath, "", secret)),
		Logger:   logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "a credential with no env-var name must be reported")
}

// TestManagedConfigUsesNativeEnvIndirection pins the per-harness escape (#749):
// kimi reads no key from the process environment, so its write-back rewrites
// "api_key = \"<value>\"" to "api_key_env = \"<ENVVAR>\"" and emits no ${VAR}
// literal.
func TestManagedConfigUsesNativeEnvIndirection(t *testing.T) {
	home := t.TempDir()
	const configPath = ".kimi-code/config.toml"
	const secret = "sk-kimi-secret"
	writeManagedConfig(t, home, configPath, `api_key = "`+secret+`"`+"\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "kimi-code",
		ReadBack: changedConfigBack(
			configPath,
			testStaticCredential(configPath, "MOONSHOT_API_KEY", secret),
		),
	})

	require.Len(t, findings, 1)
	content := string(findings[0].Writes[0].ManagedConfig.Content)
	require.Contains(t, content, `api_key_env = "MOONSHOT_API_KEY"`)
	require.NotContains(t, content, "${", "the native form is not a ${VAR} literal")
	require.NotContains(t, content, secret)
}

// TestManagedConfigNativeIndirectionRewritesKeyForms pins the native rewrite's
// key handling for a quoted key ("api_key" = "…") and a dotted key
// (providers.x.api_key = "…"): the whole key token is replaced, including a
// quoted key's closing quote, so no dangling quote corrupts the output.
func TestManagedConfigNativeIndirectionRewritesKeyForms(t *testing.T) {
	const configPath = ".kimi-code/config.toml"
	const secret = "sk-kimi-secret"

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unquoted key",
			content: `api_key = "` + secret + `"` + "\n",
			want:    `api_key_env = "MOONSHOT_API_KEY"`,
		},
		{
			name:    "quoted key",
			content: `"api_key" = "` + secret + `"` + "\n",
			want:    `api_key_env = "MOONSHOT_API_KEY"`,
		},
		{
			name:    "dotted key",
			content: `providers.x.api_key = "` + secret + `"` + "\n",
			want:    `providers.x.api_key_env = "MOONSHOT_API_KEY"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeManagedConfig(t, home, configPath, test.content)

			findings := ManagedConfigFindings(ManagedConfigFacts{
				Home:  home,
				Agent: "kimi-code",
				ReadBack: changedConfigBack(
					configPath,
					testStaticCredential(configPath, "MOONSHOT_API_KEY", secret),
				),
			})

			require.Len(t, findings, 1)
			content := string(findings[0].Writes[0].ManagedConfig.Content)
			require.Contains(t, content, test.want)
			require.NotContains(t, content, secret)
		})
	}
}

// TestManagedConfigIgnoresAStaticCredentialWithNoValue pins that a static
// credential with no value is not a secret: it contributes nothing to the
// substitution, so the file is written back rather than refused.
func TestManagedConfigIgnoresAStaticCredentialWithNoValue(t *testing.T) {
	home := t.TempDir()
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"model\": \"sonnet\"\n}\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", ""),
		),
	})

	require.Len(t, findings, 1)
	require.NotContains(t, string(findings[0].Writes[0].ManagedConfig.Content), "${")
}

// TestManagedConfigUnknownAgentUsesTheDefaultPlaceholder pins that an Agent name
// resolving to no profile falls back to the default ${ENVVAR} form rather than a
// harness-specific one.
func TestManagedConfigUnknownAgentUsesTheDefaultPlaceholder(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-unknown-agent"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \""+secret+"\"\n}\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "no-such-agent",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
	})

	require.Len(t, findings, 1)
	require.Contains(t, string(findings[0].Writes[0].ManagedConfig.Content), "${OPENAI_API_KEY}")
}

// TestManagedConfigWritesAFormatItCannotDecode pins the fallback for a candidate
// whose extension configdecode does not know (an agent-authored markdown file):
// only the literal value check runs, so a value-free file is written back.
func TestManagedConfigWritesAFormatItCannotDecode(t *testing.T) {
	home := t.TempDir()
	const configPath = ".config/opencode/AGENTS.md"
	writeManagedConfig(t, home, configPath, "# house rules\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "opencode2",
		ReadBack: changedConfigBack(configPath),
	})

	require.Len(t, findings, 1)
	require.Equal(t, "AGENTS.md", findings[0].Writes[0].ManagedConfig.Name)
}

// TestManagedConfigWritesAFileThatDoesNotDecode pins the decode-error fallback: a
// candidate with a known format whose content does not parse is judged on the
// literal check alone, so a value-free malformed file is still written back.
func TestManagedConfigWritesAFileThatDoesNotDecode(t *testing.T) {
	home := t.TempDir()
	writeManagedConfig(t, home, managedConfigTestPath, "[1, 2\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "opencode2",
		ReadBack: changedConfigBack(managedConfigTestPath),
	})

	require.Len(t, findings, 1)
}

// TestManagedConfigRefusesAnEscapedValueInAnArray pins the decoded check's array
// walk: a value stored in its escaped form (the value holds a quote) inside a
// JSON array is missed by the literal search, so only the decoded walk refuses
// it.
func TestManagedConfigRefusesAnEscapedValueInAnArray(t *testing.T) {
	home := t.TempDir()
	const secret = `sk-a"b`
	// The array element stores the quote escaped, so the raw bytes hold `sk-a\"b`.
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"keys\": [\"sk-a\\\"b\"]\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an escaped value in an array must be reported")
}

// TestRewriteNativeAssignmentsIgnoresAnEmptyValue pins the guard: with no value
// to rewrite, the content is returned unchanged.
func TestRewriteNativeAssignmentsIgnoresAnEmptyValue(t *testing.T) {
	const content = "api_key = \"\"\n"

	got := rewriteNativeAssignments(content, readback.Credential{EnvVar: "MOONSHOT_API_KEY"}, "api_key_env")

	require.Equal(t, content, got)
}

// TestAssignmentKeyRangeRejectsASeparatorWithoutAKey pins the false return when a
// value follows an assignment separator with no key token before it: there is
// nothing to rewrite, so the caller's value-remains check refuses the file.
func TestAssignmentKeyRangeRejectsASeparatorWithoutAKey(t *testing.T) {
	const content = "+ = value"

	_, _, ok := assignmentKeyRange(content, strings.Index(content, "value"))

	require.False(t, ok)
}
