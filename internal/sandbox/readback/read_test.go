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
	require.Empty(t, rb.ConfigPaths, "a login store is not a config path")
}

// TestReadClassifiesStaticDotenv: a declared .env file yields one static
// credential per KEY=VALUE line, with the value extracted in memory only.
func TestReadClassifiesStaticDotenv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "# comment\n\nOPENAI_API_KEY=sk-secret\nANTHROPIC_API_KEY=other\nUNKNOWN=bare\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Login)
	require.Len(t, rb.Static, 3, "each non-comment KEY=VALUE line is one static credential")

	openai := rb.Static[0]
	require.Equal(t, CredentialStatic, openai.Kind)
	require.Equal(t, ".hermes/.env", openai.Source)
	require.Equal(t, "openai", openai.Provider)
	require.Equal(t, "authorization", openai.Field)
	require.Equal(t, "sk-secret", openai.Value)

	anthropic := rb.Static[1]
	require.Equal(t, "anthropic", anthropic.Provider)
	require.Equal(t, "other", anthropic.Value)

	unknown := rb.Static[2]
	require.Empty(t, unknown.Provider, "an unrecognised key yields no provider hint")
	require.Equal(t, "bare", unknown.Value)
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
