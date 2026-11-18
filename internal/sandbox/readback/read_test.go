package readback

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/projectguard"
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

// TestReadStaticCredentialsIgnoreTokenSubstringKeys: the markers match key
// *segments*, not substrings, so a token-count configuration (MAX_TOKENS) is
// not mistaken for a token credential.
func TestReadStaticCredentialsIgnoreTokenSubstringKeys(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env",
		"MAX_TOKENS=4096\nMAX_OUTPUT_TOKENS=8192\nNUM_TOKENS=100\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "TOKENS is a distinct segment from the TOKEN marker")
}

// TestReadStaticCredentialsMatchKeySegments: a marker that is a whole segment of
// the key still classifies the entry as a static secret.
func TestReadStaticCredentialsMatchKeySegments(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", strings.Join([]string{
		"OPENAI_API_KEY=sk-a",
		"MY_TOKEN=t",
		"AWS_SECRET_ACCESS_KEY=aws",
		"DB_PASSWORD=p",
	}, "\n")+"\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 4, "KEY, TOKEN, SECRET, and PASSWORD are all whole segments")
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

// TestReadPropagatesAWalkRefusal: a declared path the projectguard refuses to
// list aborts the read rather than reporting fewer config facts.
func TestReadPropagatesAWalkRefusal(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config/opencode")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	gone := filepath.Join(dir, "gone.jsonc")
	t.Cleanup(projectguard.Install(
		[]string{dir},
		map[string]projectguard.Approval{gone: projectguard.ApprovalOf([]byte("x"))},
	))

	_, err := Read(home, "opencode", Manifest{})

	require.ErrorIs(t, err, projectguard.ErrNotApproved)
}

// TestReadDoesNotListAProbeAsAConfigPath: a credential probe's entry carries no
// path, so it never becomes a fact-1 config path.
func TestReadDoesNotListAProbeAsAConfigPath(t *testing.T) {
	home := t.TempDir()
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.ConfigPaths, "a probe entry is not a config path")
}

// TestReadProcessesEntriesAfterADotenv: a config file sorted after the .env is
// still listed, so the skip at the end of the dotenv branch does not stop the
// loop.
func TestReadProcessesEntriesAfterADotenv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk\n")
	writeFile(t, home, ".hermes/config.yaml", "x")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, []string{".hermes/config.yaml"}, rb.ConfigPaths,
		"a config after the dotenv is still listed")
}

// TestReadStaticCredentialsMatchHyphenSegments: a '-' separates key segments
// too, so MY-TOKEN is a whole-segment match.
func TestReadStaticCredentialsMatchHyphenSegments(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "MY-TOKEN=t\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1, "a hyphen separates key segments")
}

// TestReadStaticCredentialsSkipWhitespaceComments: a comment line indented with
// whitespace is still a comment, not a credential whose key secret looking text.
func TestReadStaticCredentialsSkipWhitespaceComments(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "  # OPENAI_API_KEY=commented\n\tOPENAI_API_KEY=real\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "real", rb.Static[0].Value)
}

// TestReadStaticCredentialsSkipLinesWithoutValue: a line without '=' is not a
// credential, even when its text looks like a secret key.
func TestReadStaticCredentialsSkipLinesWithoutValue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
}

// TestReadStaticCredentialsTrimKeyWhitespace: whitespace around the key is
// trimmed before the secret-key test, so "KEY = value" is still a credential.
func TestReadStaticCredentialsTrimKeyWhitespace(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY = sk-x\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "sk-x", rb.Static[0].Value)
}

// TestAppendProbeCredentialsKeepsLoginAndStatic: a login credential in a probe's
// outcomes does not stop the loop, so a later static credential is still kept.
func TestAppendProbeCredentialsKeepsLoginAndStatic(t *testing.T) {
	var rb ReadBack

	rb.appendProbeCredentials([]probeOutcome{{credentials: []Credential{
		{Kind: CredentialLogin, Provider: "github"},
		{Kind: CredentialStatic, Provider: "openai", Value: "sk"},
	}}})

	require.Len(t, rb.Login, 1)
	require.Len(t, rb.Static, 1)
}

// TestReadFillsCredentialHosts: a static credential's Hosts come from the
// endpoint the config entry naming its provider declares, normalized to a host
// (scheme, port, and path dropped). The dotenv credential and the config
// endpoint live in different files; the provider name is what ties them.
func TestReadFillsCredentialHosts(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: openai\n  base_url: https://api.example:8443/v1\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "openai", rb.Static[0].Provider)
	require.Equal(t, []string{"api.example"}, rb.Static[0].Hosts,
		"the endpoint host, with scheme, port, and path dropped")
}

// TestReadCredentialHostsEmptyWithoutAnEndpoint: an entry that names the
// provider but declares no endpoint yields no hosts, never a guess.
func TestReadCredentialHostsEmptyWithoutAnEndpoint(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")
	writeFile(t, home, ".hermes/config.yaml", "model:\n  provider: openai\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Empty(t, rb.Static[0].Hosts)
}

// TestReadCredentialHostsEmptyWhenTheEndpointNamesAnotherProvider: an endpoint
// hosted by a different provider's entry is not the credential's endpoint, so
// it yields no hosts rather than a mismatched attribution.
func TestReadCredentialHostsEmptyWhenTheEndpointNamesAnotherProvider(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: anthropic\n  base_url: https://api.anthropic.example/v1\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Empty(t, rb.Static[0].Hosts)
}

// TestReadFillsCredentialHostsFromAProvidersEntry: opencode's nested shape
// (`provider.<id>.options.baseURL`) ties the endpoint to the provider by the
// map key, so the credential found in its SQLite probe gains that host.
func TestReadFillsCredentialHostsFromAProvidersEntry(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"provider":{"openai":{"options":{"baseURL":"https://api.example/v1"}}}}`)
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, []string{"api.example"}, rb.Static[0].Hosts)
}

// TestReadCredentialHostsIgnoreUnparsableAndEndpointlessConfigs: a config the
// read cannot parse, or whose endpoint value is not an absolute URL, never
// aborts the read and never invents a host.
func TestReadCredentialHostsIgnoreUnparsableAndEndpointlessConfigs(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")
	writeFile(t, home, ".hermes/config.yaml", "model: {provider: openai, base_url: not a url\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Empty(t, rb.Static[0].Hosts)
}

// TestReadLoginCredentialsHaveNoHosts: a login credential is recorded, never
// read or attributed, so its Hosts stay empty.
func TestReadLoginCredentialsHaveNoHosts(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".codex/auth.json", `{"tokens":{"access_token":"super-secret"}}`)

	rb, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Login, 1)
	require.Empty(t, rb.Login[0].Hosts)
}

// TestReadCappedFileRejectsUnreadableAndMissing: a path that cannot be opened,
// or whose contents cannot be read (a directory), is refused.
func TestReadCappedFileRejectsUnreadableAndMissing(t *testing.T) {
	data, ok := readCappedFile(filepath.Join(t.TempDir(), "missing"), 16)
	require.False(t, ok, "a missing file is refused")
	require.Nil(t, data)

	data, ok = readCappedFile(t.TempDir(), 16)
	require.False(t, ok, "a directory is refused")
	require.Nil(t, data)
}

// TestReadCappedFileBoundary pins the cap exactly: a file at the cap is read
// whole, one byte more is refused.
func TestReadCappedFileBoundary(t *testing.T) {
	dir := t.TempDir()
	atCap := filepath.Join(dir, "at-cap")
	require.NoError(t, os.WriteFile(atCap, bytes.Repeat([]byte("a"), 16), 0o600))
	overCap := filepath.Join(dir, "over-cap")
	require.NoError(t, os.WriteFile(overCap, bytes.Repeat([]byte("a"), 17), 0o600))

	data, ok := readCappedFile(atCap, 16)
	require.True(t, ok)
	require.Len(t, data, 16, "a file exactly at the cap is read whole")

	_, ok = readCappedFile(overCap, 16)
	require.False(t, ok, "a file over the cap is refused")
}
