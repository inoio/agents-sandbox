package readback

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadClassifiesStaticAndLogin(t *testing.T) {
	home := t.TempDir()
	// claude-code: a settings.json login entry → Login, value never read
	writeFile(t, home, ".claude/settings.json", `{"oauthAccount":{"email":"x"}}`)
	rb, err := Read(home, "claude-code", Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.Static) != 0 || len(rb.Login) == 0 {
		t.Fatalf("want login only, got %+v", rb)
	}
}

func TestReadUnknownAgentIsEmpty(t *testing.T) {
	rb, err := Read(t.TempDir(), "no-such-agent", Manifest{})
	if err != nil || len(rb.Static)+len(rb.Login) != 0 {
		t.Fatalf("want empty, got %+v %v", rb, err)
	}
}

// TestReadClassifiesLoginStore: a declared login store is recorded as a login
// credential whose value is never read out.
func TestReadClassifiesLoginStore(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".codex/auth.json", `{"tokens":{"access_token":"super-secret"}}`)

	rb, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "a login store must not yield a static credential")
	require.Len(t, rb.Login, 1)
	require.Equal(t, CredentialLogin, rb.Login[0].Kind)
	require.Equal(t, ".codex/auth.json", rb.Login[0].Source)
	require.Equal(t, "codex", rb.Login[0].Provider)
	require.Empty(t, rb.Login[0].Value, "a login value is never read")
	require.Equal(t, []string{".codex/auth.json"}, rb.ConfigPaths,
		"a login store is also a config fact")
}

// TestReadScopesSharedAuthStoreToOpenCode: the shared opencode auth.json is a
// login for opencode (v1) only. opencode2 keeps credentials in its database and
// never imports the file, so it must not report a login for it.
func TestReadScopesSharedAuthStoreToOpenCode(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".local/share/opencode/auth.json", `{"openai":{"type":"api"}}`)

	openCodeReadBack, err := Read(home, "opencode", Manifest{})
	require.NoError(t, err)
	require.Len(t, openCodeReadBack.Login, 1)
	require.Equal(t, "opencode", openCodeReadBack.Login[0].Provider)

	openCode2ReadBack, err := Read(home, "opencode2", Manifest{})
	require.NoError(t, err)
	require.Empty(t, openCode2ReadBack.Login, "opencode2 must not report the v1 auth store as a login")
}

// TestReadClassifiesStaticDotenv: a declared .env file yields one static
// credential per secret-looking KEY=VALUE line, with the value extracted in
// memory only. Obvious non-secrets are not credentials.
func TestReadClassifiesStaticDotenv(t *testing.T) {
	home := t.TempDir()
	writeFile(
		t,
		home,
		".hermes/.env",
		"# comment\n\nDEBUG=true\nOPENAI_API_KEY=sk-secret\nANTHROPIC_API_KEY=other\nLOGGING=verbose\n",
	)

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Login)
	require.Len(t, rb.Static, 2, "only the secret-looking KEY=VALUE lines are credentials")

	openai := rb.Static[0]
	require.Equal(t, CredentialStatic, openai.Kind)
	require.Equal(t, ".hermes/.env", openai.Source)
	require.Equal(t, "openai", openai.Provider)
	require.Equal(t, "authorization", openai.Field)
	require.Equal(t, "sk-secret", openai.Value)

	anthropic := rb.Static[1]
	require.Equal(t, "anthropic", anthropic.Provider)
	require.Equal(t, "x-api-key", anthropic.Field)
	require.Equal(t, "other", anthropic.Value)
}

// TestReadStaticCredentialsIgnoreNonSecrets: a key that does not name a secret
// (DEBUG, LOG_LEVEL) is configuration, not a static credential.
func TestReadStaticCredentialsIgnoreNonSecrets(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "DEBUG=true\nLOG_LEVEL=info\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
}

// TestReadListsConfigPaths: a declared config file is a fact-1 config path, not
// a credential.
func TestReadListsConfigPaths(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "{}")
	writeFile(t, home, ".config/opencode/agent/foo.jsonc", "{}")

	rb, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
	require.Empty(t, rb.Login)
	require.Equal(
		t,
		[]string{".config/opencode/opencode.jsonc"},
		rb.ConfigPaths,
		"the glob selects the direct children only",
	)
}

// TestReadReportsChangedAgainstBaseline: Changed is the current manifest's
// difference from the baseline.
func TestReadReportsChangedAgainstBaseline(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc", "a")
	baseline, err := TakeManifest(home, "opencode")
	require.NoError(t, err)
	writeFile(t, home, ".config/opencode/opencode.jsonc", "b")

	rb, err := Read(home, "opencode", baseline)

	require.NoError(t, err)
	require.Equal(t, []string{".config/opencode/opencode.jsonc"}, rb.Changed)
}

// TestReadMissingDeclaredPaths: a home with none of the declared paths yields
// no facts and no error.
func TestReadMissingDeclaredPaths(t *testing.T) {
	rb, err := Read(t.TempDir(), "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.ConfigPaths)
	require.Empty(t, rb.Static)
	require.Empty(t, rb.Login)
}
