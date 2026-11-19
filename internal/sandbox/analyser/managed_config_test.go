package analyser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
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
		Home:          home,
		Agent:         "opencode2",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
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

// TestManagedConfigOnlyChangedSnippetCandidates pins that the changed check is
// what admits a candidate, not the snippet match alone: two snippet-matching
// files, only one changed, yield exactly the changed one's finding.
func TestManagedConfigOnlyChangedSnippetCandidates(t *testing.T) {
	home := t.TempDir()
	const changedPath = ".config/opencode/opencode.jsonc"
	const unchangedPath = ".config/opencode/opencode.unchanged.jsonc"
	writeManagedConfig(t, home, changedPath, "{\n  \"model\": \"sonnet\"\n}\n")
	writeManagedConfig(t, home, unchangedPath, "{\n  \"model\": \"haiku\"\n}\n")

	readBack := readback.ReadBack{
		ConfigPaths: []string{unchangedPath, changedPath},
		Changed:     []string{changedPath},
	}

	findings := ManagedConfigFindings(ManagedConfigFacts{Home: home, Agent: "opencode2", ReadBack: readBack})

	require.Len(t, findings, 1)
	require.Equal(t, "opencode.jsonc", findings[0].Writes[0].ManagedConfig.Name)
}

// TestManagedConfigContinuesPastASkippedCandidate pins that a candidate the
// write-back cannot sanitize is skipped, not treated as the end of the set: an
// unreadable file followed by a readable one still yields the readable one's
// finding.
func TestManagedConfigContinuesPastASkippedCandidate(t *testing.T) {
	home := t.TempDir()
	const unreadablePath = ".config/opencode/opencode.missing.jsonc"
	const validPath = ".config/opencode/opencode.jsonc"
	writeManagedConfig(t, home, validPath, "{\n  \"model\": \"sonnet\"\n}\n")

	readBack := readback.ReadBack{
		ConfigPaths: []string{unreadablePath, validPath},
		Changed:     []string{unreadablePath, validPath},
	}

	findings := ManagedConfigFindings(ManagedConfigFacts{Home: home, Agent: "opencode2", ReadBack: readBack})

	require.Len(t, findings, 1)
	require.Equal(t, "opencode.jsonc", findings[0].Writes[0].ManagedConfig.Name)
}

// TestManagedConfigSkipsAnUnreadableDecodableEmptyCandidate pins that the
// unreadable/over-cap refusal is what stops an empty write, not the value-remains
// check: a missing hermes config.yaml (a format that decodes an empty document
// without error) yields no finding, rather than an empty-content one.
func TestManagedConfigSkipsAnUnreadableDecodableEmptyCandidate(t *testing.T) {
	home := t.TempDir()
	logger := &termiotest.Mock{}
	const configPath = ".hermes/config.yaml"
	require.True(t, snippetNameMatches("hermes", configPath), "the premise: hermes merges this snippet")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "hermes",
		ReadBack: changedConfigBack(configPath),
		Logger:   logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an unreadable candidate must be reported")
}

// TestManagedConfigSkipsAnUnsubstitutableCredentialOnADecodableFormat pins that
// the substitution refusal is what stops the write, not the value-remains check:
// a hermes config.yaml whose credential is not proposed as a secret yields no
// finding, rather than an empty-content one.
func TestManagedConfigSkipsAnUnsubstitutableCredentialOnADecodableFormat(t *testing.T) {
	home := t.TempDir()
	const configPath = ".hermes/config.yaml"
	writeManagedConfig(t, home, configPath, "model: sonnet\n")
	logger := &termiotest.Mock{}
	require.True(t, snippetNameMatches("hermes", configPath), "the premise: hermes merges this snippet")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "hermes",
		// The credential has a value but no proposed secret, so it cannot be
		// replaced by a placeholder.
		ReadBack: changedConfigBack(
			configPath,
			testStaticCredential(configPath, "OPENAI_API_KEY", "sk-unlisted"),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "a credential that cannot be replaced must be reported")
}

// TestManagedConfigNeverCarriesAValue pins invariant 3: no field of the finding
// — evidence, write text, target name, or content — holds the static value.
func TestManagedConfigNeverCarriesAValue(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-never-persisted"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \""+secret+"\"\n}\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:          home,
		Agent:         "opencode2",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
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
		SecretEnvVars: []string{"MOONSHOT_API_KEY"},
		Logger:        logger,
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
	const candidatePath = ".config/opencode/opencode.json5"
	const secret = "sk-cross-source"
	writeManagedConfig(t, home, candidatePath, "{\n  \"note\": \""+secret+"\"\n}\n")
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
		SecretEnvVars: []string{"MOONSHOT_API_KEY"},
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
				SecretEnvVars: []string{"MOONSHOT_API_KEY"},
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
		Home:          home,
		Agent:         "no-such-agent",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
	})

	require.Len(t, findings, 1)
	require.Contains(t, string(findings[0].Writes[0].ManagedConfig.Content), "${OPENAI_API_KEY}")
}

// TestManagedConfigSkipsANonSnippetName pins that a candidate the agent's
// snippet pattern does not match is not proposed: provisioning merges only
// snippet-matching files, so opencode's config-family "config.json" (a mirror
// name, not a snippet) would be a managed write the next run never reads. The
// snippet-matching sibling is still proposed.
func TestManagedConfigSkipsANonSnippetName(t *testing.T) {
	home := t.TempDir()
	const mirrorPath = ".config/opencode/config.json"
	const snippetPath = ".config/opencode/opencode.jsonc"
	writeManagedConfig(t, home, mirrorPath, "{\n  \"model\": \"sonnet\"\n}\n")
	writeManagedConfig(t, home, snippetPath, "{\n  \"model\": \"sonnet\"\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: readback.ReadBack{
			ConfigPaths: []string{mirrorPath, snippetPath},
			Changed:     []string{mirrorPath, snippetPath},
		},
		Logger: logger,
	})

	require.Len(t, findings, 1)
	require.Equal(t, "opencode.jsonc", findings[0].Writes[0].ManagedConfig.Name)
	require.NotEmpty(t, logger.WarnCalls, "a non-snippet candidate must be reported")
}

// TestManagedConfigRefusesAFileThatDoesNotDecode pins the decode-error safety
// net: a candidate with a known format whose content does not parse cannot be
// walked for an escaped value, so it is reported and no finding is emitted — a
// value must never reach the managed config by omission.
func TestManagedConfigRefusesAFileThatDoesNotDecode(t *testing.T) {
	home := t.TempDir()
	writeManagedConfig(t, home, managedConfigTestPath, "[1, 2\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "opencode2",
		ReadBack: changedConfigBack(managedConfigTestPath),
		Logger:   logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an undecodable candidate must be reported")
}

// TestContentRetainsStaticValueUnknownFormat pins the fallback for a path whose
// extension configdecode does not know: only the literal check runs, so a
// value-free file is reported as retaining nothing.
func TestContentRetainsStaticValueUnknownFormat(t *testing.T) {
	credentials := []readback.Credential{testStaticCredential(".config/opencode/notes.txt", "OPENAI_API_KEY", "sk-x")}

	require.False(t, contentRetainsStaticValue(".config/opencode/notes.txt", "no secret here\n", credentials))
}

// TestManagedConfigRefusesACredentialThatWillNotBeASecret pins the placeholder
// invariant: the write-back substitutes a credential only when the analysis will
// propose it as an env.secret.yaml entry. A credential that will not become a
// secret leaves the file refused, so it never carries an orphan ${ENVVAR}
// placeholder (the harness would send the literal) or a raw sibling value.
func TestManagedConfigRefusesACredentialThatWillNotBeASecret(t *testing.T) {
	home := t.TempDir()
	const secret = "sk-unscoped"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"apiKey\": \""+secret+"\"\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "opencode2",
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
		// No SecretEnvVars: the credential has no host, so no secret is proposed.
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "a credential that will not be a secret must be reported")
}

// TestManagedConfigNativeIndirectionLeavesAnEnvTableKey pins that the native
// rewrite does not rename an assignment whose key is already the env-var name.
// kimi's env-table entry KIMI_API_KEY = "<value>" must keep its key and take the
// ${ENVVAR} placeholder; renaming it to api_key_env would drop the credential.
func TestManagedConfigNativeIndirectionLeavesAnEnvTableKey(t *testing.T) {
	home := t.TempDir()
	const configPath = ".kimi-code/config.toml"
	const secret = "sk-declared"
	writeManagedConfig(t, home, configPath, "[providers.x.env]\nKIMI_API_KEY = \""+secret+"\"\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:  home,
		Agent: "kimi-code",
		ReadBack: changedConfigBack(
			configPath,
			testStaticCredential(configPath, "KIMI_API_KEY", secret),
		),
		SecretEnvVars: []string{"KIMI_API_KEY"},
	})

	require.Len(t, findings, 1)
	content := string(findings[0].Writes[0].ManagedConfig.Content)
	require.Contains(t, content, `KIMI_API_KEY = "${KIMI_API_KEY}"`)
	require.NotContains(t, content, "api_key_env", "the env-var key is not renamed to the indirection key")
	require.NotContains(t, content, secret)
}

// TestManagedConfigReplacesPrefixValuesLongestFirst pins the substitution order:
// when one value is a prefix of another in the same file, the longer value is
// replaced first, so the shorter one cannot corrupt it into a placeholder
// followed by the longer value's tail.
func TestManagedConfigReplacesPrefixValuesLongestFirst(t *testing.T) {
	home := t.TempDir()
	const short = "sk-abc"
	const long = "sk-abcdef"
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"a\": \""+short+"\",\n  \"b\": \""+long+"\"\n}\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:          home,
		Agent:         "opencode2",
		SecretEnvVars: []string{"A_API_KEY", "B_API_KEY"},
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "A_API_KEY", short),
			testStaticCredential(managedConfigTestPath, "B_API_KEY", long),
		),
	})

	require.Len(t, findings, 1)
	content := string(findings[0].Writes[0].ManagedConfig.Content)
	require.Contains(t, content, "${B_API_KEY}", "the longer value keeps its own placeholder")
	require.NotContains(t, content, "${A_API_KEY}def", "the shorter value must not corrupt the longer one")
	require.NotContains(t, content, short)
	require.NotContains(t, content, long)
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
		SecretEnvVars: []string{"OPENAI_API_KEY"},
		Logger:        logger,
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

// TestAssignmentKeyRange pins the key-token range the native rewrite replaces:
// the token before a '=' or ':' separator, with a quoted token's own quotes
// included (so replacing it leaves no dangling quote) and a dotted key's final
// segment alone, and no range when no key precedes the value.
func TestAssignmentKeyRange(t *testing.T) {
	tests := []struct {
		name    string
		content string
		value   string
		want    string
		ok      bool
	}{
		{name: "spaced equals", content: `api_key = "sk"`, value: "sk", want: "api_key", ok: true},
		{name: "tight equals", content: `api_key="sk"`, value: "sk", want: "api_key", ok: true},
		{name: "quoted key", content: `"api_key" = "sk"`, value: "sk", want: `"api_key"`, ok: true},
		{name: "dotted key", content: `providers.x.api_key = "sk"`, value: "sk", want: "api_key", ok: true},
		{name: "hyphenated key", content: `my-key = "sk"`, value: "sk", want: "my-key", ok: true},
		{name: "digits and underscore", content: `a1_2 = "sk"`, value: "sk", want: "a1_2", ok: true},
		{name: "colon separator", content: `key: "sk"`, value: "sk", want: "key", ok: true},
		{name: "tab around separator", content: "key:\t\"sk\"", value: "sk", want: "key", ok: true},
		{name: "colon with no key", content: `: "sk"`, value: "sk", want: "", ok: false},
		{name: "equals with no key", content: `= "sk"`, value: "sk", want: "", ok: false},
		{name: "value at content start", content: `"sk"`, value: "sk", want: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := assignmentKeyRange(test.content, strings.Index(test.content, test.value))
			require.Equal(t, test.ok, ok)
			if test.ok {
				require.Equal(t, test.want, test.content[start:end])
			}
		})
	}
}

// TestSkipSpaceBackward pins the ASCII-whitespace walk: index moves left past
// every space/tab/CR/LF, and stops on any other byte or at the start.
func TestSkipSpaceBackward(t *testing.T) {
	require.Equal(t, 1, skipSpaceBackward("a   b", 4), "three spaces are skipped")
	require.Equal(t, 1, skipSpaceBackward("ab", 1), "a non-space stops the walk")
	require.Equal(t, 0, skipSpaceBackward("ab", 0), "index 0 stays")
	require.Equal(t, 0, skipSpaceBackward("   x", 3), "leading whitespace walks to the start")
}

// TestIsQuote pins the quote classifier to double and single quotes only.
func TestIsQuote(t *testing.T) {
	for _, b := range []byte{'"', '\''} {
		require.True(t, isQuote(b), "%q is a quote", b)
	}
	for _, b := range []byte{'a', '`', '='} {
		require.False(t, isQuote(b), "%q is not a quote", b)
	}
}

// TestIsKeyChar pins the key-token classifier to ASCII letters, digits, '_' and
// '-'; a '.' is excluded so a dotted key's final segment is the token.
func TestIsKeyChar(t *testing.T) {
	for _, b := range []byte{'a', 'z', 'A', 'Z', '0', '9', '_', '-'} {
		require.True(t, isKeyChar(b), "%q is a key char", b)
	}
	for _, b := range []byte{'.', ' ', '=', '"', '/', 0xC3} {
		require.False(t, isKeyChar(b), "%q is not a key char", b)
	}
}

// TestIsSpaceByte pins the whitespace classifier to ASCII space, tab, CR and LF.
func TestIsSpaceByte(t *testing.T) {
	for _, b := range []byte{' ', '\t', '\r', '\n'} {
		require.True(t, isSpaceByte(b), "%q is whitespace", b)
	}
	for _, b := range []byte{'a', '=', '.', '\v'} {
		require.False(t, isSpaceByte(b), "%q is not whitespace", b)
	}
}

// TestUnquotedKey pins the key dequoting: a token with matching quotes loses
// them, and any other token — one quote only, two characters or fewer, or an
// unquoted name — is returned unchanged.
func TestUnquotedKey(t *testing.T) {
	require.Equal(t, "api_key", unquotedKey(`"api_key"`))
	require.Equal(t, "api_key", unquotedKey(`'api_key'`))
	require.Equal(t, "api_key", unquotedKey("api_key"))
	require.Equal(t, `"api`, unquotedKey(`"api`), "a leading quote with no closing quote is kept")
	require.Equal(t, `api"`, unquotedKey(`api"`), "a trailing quote with no opening quote is kept")
	require.Empty(t, unquotedKey(`""`))
	require.Equal(t, `"`, unquotedKey(`"`), "a one-byte token is not a quoted token")
}

// TestManagedConfigIgnoresAnEmptyValueEvenWhenProposedAsASecret pins that an
// empty value is never a secret and never substituted: even with its env-var
// name proposed as a secret, the file is written back byte-for-byte, never
// corrupted by a replace of the empty string and never refused by the
// value-remains check.
func TestManagedConfigIgnoresAnEmptyValueEvenWhenProposedAsASecret(t *testing.T) {
	home := t.TempDir()
	const content = "{\n  \"model\": \"sonnet\"\n}\n"
	writeManagedConfig(t, home, managedConfigTestPath, content)

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:          home,
		Agent:         "opencode2",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", ""),
		),
	})

	require.Len(t, findings, 1)
	require.JSONEq(t, content, string(findings[0].Writes[0].ManagedConfig.Content))
}

// TestManagedConfigRefusesAnEscapedStaticValueInAKey pins the decoded walk's
// key check: a value stored in its escaped form as a JSON key (the value holds a
// quote) is missed by the literal search, so only the decoded walk catches it
// and refuses the file.
func TestManagedConfigRefusesAnEscapedStaticValueInAKey(t *testing.T) {
	home := t.TempDir()
	const secret = `sk-a"b`
	// The key stores the quote escaped, so the raw bytes hold `sk-a\"b`.
	writeManagedConfig(t, home, managedConfigTestPath, "{\n  \"sk-a\\\"b\": \"x\"\n}\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:          home,
		Agent:         "opencode2",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
		ReadBack: changedConfigBack(
			managedConfigTestPath,
			testStaticCredential(managedConfigTestPath, "OPENAI_API_KEY", secret),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an escaped value stored as a key must be reported")
}

// TestDecodedContainsStaticValueFindsAKey pins the decoded walk's key check
// directly: a value held as a document key is found, and one absent from every
// key and leaf is not.
func TestDecodedContainsStaticValueFindsAKey(t *testing.T) {
	credentials := []readback.Credential{{Value: `sk-a"b`}}
	document := map[string]any{`sk-a"b`: "x"}

	require.True(t, decodedContainsStaticValue(document, credentials))
	require.False(t, decodedContainsStaticValue(map[string]any{"other": "x"}, credentials))
}

// TestDecodedContainsStaticValueWalksAnyMap pins the map[any]any walk directly:
// a string key holding the value is found, a non-string key is not a value, and
// a map without the value is not reported (so the fix does not over-refuse).
func TestDecodedContainsStaticValueWalksAnyMap(t *testing.T) {
	credentials := []readback.Credential{{Value: "sk-x"}}

	require.True(t, decodedContainsStaticValue(map[any]any{"sk-x": "y"}, credentials))
	require.False(t, decodedContainsStaticValue(map[any]any{1: "y", "other": "z"}, credentials))
}

// TestManagedConfigWritesAYAMLFileWithANonStringKeyAndNoSecret pins that the
// map[any]any walk does not over-refuse: a YAML config with a non-string key and
// no secret value is written back rather than dropped.
func TestManagedConfigWritesAYAMLFileWithANonStringKeyAndNoSecret(t *testing.T) {
	home := t.TempDir()
	const configPath = ".hermes/config.yaml"
	writeManagedConfig(t, home, configPath, "model:\n  provider: openai\n  123: x\n")

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:     home,
		Agent:    "hermes",
		ReadBack: changedConfigBack(configPath),
	})

	require.Len(t, findings, 1)
}

// TestManagedConfigRefusesAnEscapedValueBehindANonStringKey pins the decoded
// walk against YAML's nested map[any]any: a mapping that has a non-string key
// decodes to map[any]any rather than map[string]any, so an escaped value beneath
// it is missed by the literal search and only the walk can refuse the file.
func TestManagedConfigRefusesAnEscapedValueBehindANonStringKey(t *testing.T) {
	home := t.TempDir()
	const configPath = ".hermes/config.yaml"
	const secret = `sk-a"b`
	// The value is stored escaped and the enclosing mapping has an int key, so
	// it decodes to map[any]any.
	writeManagedConfig(t, home, configPath, "model:\n  provider: openai\n  api_key: \"sk-a\\\"b\"\n  123: x\n")
	logger := &termiotest.Mock{}

	findings := ManagedConfigFindings(ManagedConfigFacts{
		Home:          home,
		Agent:         "hermes",
		SecretEnvVars: []string{"OPENAI_API_KEY"},
		ReadBack: changedConfigBack(
			configPath,
			testStaticCredential(configPath, "OPENAI_API_KEY", secret),
		),
		Logger: logger,
	})

	require.Empty(t, findings)
	require.NotEmpty(t, logger.WarnCalls, "an escaped value behind a non-string key must be reported")
}

// TestSubstituteStaticValuesRewritesEveryCredential pins that the substitution
// loop under a native indirection key processes every credential, not just the
// first: two credentials both yield their rewritten assignment and neither value
// survives.
func TestSubstituteStaticValuesRewritesEveryCredential(t *testing.T) {
	const content = "one = \"sk-one\"\ntwo = \"sk-two\"\n"
	credentials := []readback.Credential{
		{EnvVar: "ONE_KEY", Value: "sk-one"},
		{EnvVar: "TWO_KEY", Value: "sk-two"},
	}

	got, ok := substituteStaticValues(
		content,
		credentials,
		"api_key_env",
		map[string]bool{"ONE_KEY": true, "TWO_KEY": true},
	)

	require.True(t, ok)
	require.NotContains(t, got, "sk-one")
	require.NotContains(t, got, "sk-two")
}

// snippetlessAgent is a registered stub that does not implement
// agent.ConfigMerger, so tests can drive the "no snippet pattern" branch.
type snippetlessAgent struct{ name string }

func (a snippetlessAgent) Name() string             { return a.name }
func (snippetlessAgent) ConfigDirName() string      { return "snippetless" }
func (snippetlessAgent) Experimental() bool         { return false }
func (snippetlessAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// withSnippetlessAgent registers the stub under name for the test, restoring the
// built-in afterward.
func withSnippetlessAgent(t *testing.T, name string) {
	t.Helper()
	original, ok := agent.Lookup(name)
	if !ok {
		t.Fatalf("%s must be a registered agent", name)
	}
	agent.Register(snippetlessAgent{name: name})
	t.Cleanup(func() { agent.Register(original) })
}

// TestSnippetNameMatchesIsPermissiveWithoutASnippetPattern pins the guard: an
// agent that declares no snippet pattern is not second-guessed, so a candidate
// is not dropped on a pattern the agent never stated.
func TestSnippetNameMatchesIsPermissiveWithoutASnippetPattern(t *testing.T) {
	withSnippetlessAgent(t, "codex")

	require.True(t, snippetNameMatches("codex", ".config/codex/anything.txt"))
}
