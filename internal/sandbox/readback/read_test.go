package readback

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestLoginStoreForMatchesExactPath pins that a pattern with no metacharacter
// matches its exact path.
func TestLoginStoreForMatchesExactPath(t *testing.T) {
	store, ok := loginStoreFor(".codex/auth.json")

	require.True(t, ok)
	require.Equal(t, "codex", store.provider)
}

// TestLoginStoreForMatchesAGlob pins that a directory store declared as a glob
// matches the files it selects, not just the literal pattern.
func TestLoginStoreForMatchesAGlob(t *testing.T) {
	store, ok := loginStoreFor(".kimi-code/credentials/a.json")

	require.True(t, ok)
	require.Equal(t, "kimi-code", store.provider)
}

// TestLoginStoreForDoesNotCrossSeparators pins that a glob's "*" never crosses a
// separator, so a nested file is not a member of the store.
func TestLoginStoreForDoesNotCrossSeparators(t *testing.T) {
	_, ok := loginStoreFor(".kimi-code/credentials/sub/a.json")

	require.False(t, ok, "* does not cross a path separator")
}

// TestLoginStoreForUnknownIsAbsent pins that a key no patterns match reports no
// store.
func TestLoginStoreForUnknownIsAbsent(t *testing.T) {
	_, ok := loginStoreFor(".nope/x")

	require.False(t, ok)
}

// TestReadClassifiesADirectoryLoginStore: a store declared as a glob records one
// login per file it selects. kimi-code declares .kimi-code/credentials/*.json,
// so both files are logins whose values are never read.
func TestReadClassifiesADirectoryLoginStore(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".kimi-code/credentials/a.json", `{"access_token":"secret-a"}`)
	writeFile(t, home, ".kimi-code/credentials/b.json", `{"access_token":"secret-b"}`)

	rb, err := Read(home, "kimi-code", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "a login store must not yield a static credential")
	require.Len(t, rb.Login, 2)
	for _, credential := range rb.Login {
		require.Equal(t, CredentialLogin, credential.Kind)
		require.Equal(t, "kimi-code", credential.Provider)
		require.Empty(t, credential.Value, "a login value is never read")
	}
	require.Equal(
		t,
		[]string{".kimi-code/credentials/a.json", ".kimi-code/credentials/b.json"},
		rb.ConfigPaths,
		"a login store is also a config fact",
	)
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

// TestReadCredentialHostsIgnoreNonProviderEndpoints: an endpoint that sits in
// an entry the config does not present as a provider container (an MCP server's
// "url") must not be attributed to a provider of the same name; an unrecognised
// shape yields no host, never a guess.
func TestReadCredentialHostsIgnoreNonProviderEndpoints(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"mcp":{"openai":{"type":"remote","url":"https://mcp.example/sse"}}}`)
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Empty(t, rb.Static[0].Hosts,
		"an MCP server's url is not the provider's endpoint")
}

// TestReadCredentialHostsIgnoreNamedNonProviderEndpoints: a node that carries a
// name/id field and an endpoint is only a provider entry when it sits in a
// provider container; a tool or agent entry that also names a url is not.
func TestReadCredentialHostsIgnoreNamedNonProviderEndpoints(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"agent":{"openai":{"name":"openai","url":"https://agent.example/x"}}}`)
	writeOpenCode2DB(t, home, [2]string{"openai", `{"type":"key","key":"sk"}`})

	rb, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Empty(t, rb.Static[0].Hosts,
		"a non-provider entry's url is not the provider's endpoint")
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

// TestConfigEndpointHostsSkipsEntriesItCannotRead: an entry with an unknown
// extension, no hash (an opaque over-cap file), or content that cannot be read
// contributes no endpoint: an unrecognised shape yields no host, never a guess.
func TestConfigEndpointHostsSkipsEntriesItCannotRead(t *testing.T) {
	home := t.TempDir()
	manifest := Manifest{Entries: []ManifestEntry{
		{Key: ".hermes/notes.txt", Size: 1, Hash: "abc"},   // unknown extension
		{Key: ".hermes/config.yaml", Size: 1, Hash: "abc"}, // known extension but the file is absent
		{Key: ".hermes/opaque.yaml", Size: 1, Hash: ""},    // opaque (over-cap) entry
		{Key: ".hermes:probe", Hash: "abc"},                // a probe entry
		{Key: ".hermes/.env", Size: 1, Hash: "abc"},        // the dotenv carries no endpoint
	}}

	hostsByProvider := configEndpointHosts(home, manifest)

	require.Empty(t, hostsByProvider)
}

// TestCollectEndpointHostsWalksSequences: an endpoint declared inside a
// sequence node is still found, so a provider entry expressed as a list item
// yields its host.
func TestCollectEndpointHostsWalksSequences(t *testing.T) {
	hostsByProvider := map[string][]string{}

	collectEndpointHosts(
		[]any{map[string]any{"provider": "openai", "base_url": "https://api.example/v1"}},
		hostsByProvider,
	)

	require.Equal(t, []string{"api.example"}, hostsByProvider["openai"])
}

// TestEndpointHostsIgnoresNonStringValues: an endpoint key whose value is not a
// string contributes nothing rather than a guessed host.
func TestEndpointHostsIgnoresNonStringValues(t *testing.T) {
	require.Empty(t, endpointHosts(map[string]any{"url": 123}))
}

// TestEndpointHostRejectsNonURLAndHostlessValues: a value that is not an
// absolute URL with a host normalizes to the empty host.
func TestEndpointHostRejectsNonURLAndHostlessValues(t *testing.T) {
	require.Empty(t, endpointHost("not a url"))
	require.Empty(t, endpointHost("http://[::1"))
}

// TestReadStaticCredentialCarriesDotenvKeyAsEnvVar: the dotenv key is the
// configured secret's env-var name, carried on the credential so a learn-mode
// finding can scope the secret write to the variable it replaces.
func TestReadStaticCredentialCarriesDotenvKeyAsEnvVar(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "OPENAI_API_KEY", rb.Static[0].EnvVar)
}

// TestReadLoginCredentialHasNoEnvVar: the env-var name is a static-secret fact;
// a login credential, whose value is never read, carries none.
func TestReadLoginCredentialHasNoEnvVar(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".codex/auth.json", `{"tokens":{"access_token":"super-secret"}}`)

	rb, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Login, 1)
	require.Empty(t, rb.Login[0].EnvVar)
}

// TestDerivedEnvVarName pins the env-var name an inline credential is scoped by
// when its entry names none: the provider id uppercased, non-alphanumerics
// folded to '_', with an _API_KEY suffix.
func TestDerivedEnvVarName(t *testing.T) {
	require.Equal(t, "MOONSHOT_API_KEY", derivedEnvVarName("moonshot"))
	require.Equal(t, "KIMI_API_KEY", derivedEnvVarName("kimi"))
	require.Equal(t, "OH_MY_PI_API_KEY", derivedEnvVarName("oh-my-pi"))
}

// TestReadClassifiesInlineConfigSecret: a credential key inline in a provider
// entry of a declared config file is a static secret, with provider, host, and
// env-var name taken from that entry.
func TestReadClassifiesInlineConfigSecret(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key: sk-inline\n  base_url: https://api.moonshot.example/v1\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Login)
	require.Len(t, rb.Static, 1)
	credential := rb.Static[0]
	require.Equal(t, CredentialStatic, credential.Kind)
	require.Equal(t, ".hermes/config.yaml", credential.Source)
	require.Equal(t, "moonshot", credential.Provider)
	require.Equal(t, "MOONSHOT_API_KEY", credential.EnvVar)
	require.Equal(t, []string{"api.moonshot.example"}, credential.Hosts)
	require.Equal(t, "sk-inline", credential.Value)
	require.Empty(t, credential.Field, "the harness owns the header")
}

// TestReadInlineSecretTakesHostFromItsEntry: the credential's Hosts come from
// its own entry's endpoint, normalized to a host.
func TestReadInlineSecretTakesHostFromItsEntry(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key: sk-inline\n  base_url: https://api.moonshot.example:8443/v1\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, []string{"api.moonshot.example"}, rb.Static[0].Hosts,
		"the entry's endpoint host, with scheme, port, and path dropped")
}

// TestReadInlineSecretDerivesEnvVarName: a bare api_key with no declared
// env-var name is scoped by <PROVIDER>_API_KEY, derived from the entry.
func TestReadInlineSecretDerivesEnvVarName(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: kimi\n  api_key: sk-inline\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "KIMI_API_KEY", rb.Static[0].EnvVar)
}

// TestReadInlineSecretUsesDeclaredEnvVarName: a secret-looking key in the
// entry's env sub-table names its own env var, which wins over the derived one.
func TestReadInlineSecretUsesDeclaredEnvVarName(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".kimi-code/config.toml", strings.Join([]string{
		"[providers.x]",
		`base_url = "https://api.x.example/v1"`,
		"",
		"[providers.x.env]",
		`KIMI_API_KEY = "sk-declared"`,
		`REGION = "us"`,
		"",
	}, "\n"))

	rb, err := Read(home, "kimi-code", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1, "only the secret-looking env key is a credential")
	require.Equal(t, "KIMI_API_KEY", rb.Static[0].EnvVar, "the declared name wins over X_API_KEY")
	require.Equal(t, "sk-declared", rb.Static[0].Value)
}

// TestReadInlineSecretSkipsPlaceholderValue: a ${...} reference is not a
// literal secret, so it yields no credential.
func TestReadInlineSecretSkipsPlaceholderValue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key: \"${MOONSHOT_API_KEY}\"\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
}

// TestReadInlineSecretSkipsApiKeyEnvOnly: an entry that names its key
// indirectly (api_key_env) holds no literal secret.
func TestReadInlineSecretSkipsApiKeyEnvOnly(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key_env: MOONSHOT_API_KEY\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
}

// TestReadInlineSecretSkipsNonStringValue: a numeric token setting is a count,
// not a literal secret.
func TestReadInlineSecretSkipsNonStringValue(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  token: 4096\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static)
}

// TestReadInlineSecretSkipsReferenceValues: a value that is a reference rather
// than a literal secret yields no credential — a ${...} placeholder, a
// leading-! command reference, or a bare environment-variable identifier the
// read-back cannot tell apart from a bound variable name.
func TestReadInlineSecretSkipsReferenceValues(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"placeholder", "${MOONSHOT_API_KEY}"},
		{"command reference", "!op read op://vault/openai/key"},
		{"bare env-var identifier", "COPILOT_GITHUB_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, home, ".hermes/config.yaml",
				fmt.Sprintf("model:\n  provider: moonshot\n  api_key: %q\n", tc.value))

			rb, err := Read(home, "hermes", Manifest{})

			require.NoError(t, err)
			require.Empty(t, rb.Static, "%q is a reference, not a secret", tc.value)
		})
	}
}

// TestReadInlineSecretKeepsALiteralValue: a normal key value is not an
// environment-variable identifier and stays a credential.
func TestReadInlineSecretKeepsALiteralValue(t *testing.T) {
	for _, value := range []string{"sk-abcd1234", "abc.def/xyz="} {
		home := t.TempDir()
		writeFile(t, home, ".hermes/config.yaml",
			fmt.Sprintf("model:\n  provider: moonshot\n  api_key: %q\n", value))

		rb, err := Read(home, "hermes", Manifest{})

		require.NoError(t, err)
		require.Len(t, rb.Static, 1, "%q is a literal secret", value)
		require.Equal(t, value, rb.Static[0].Value)
	}
}

// TestIsReferenceValue pins the reference forms the read-back refuses.
func TestIsReferenceValue(t *testing.T) {
	for _, value := range []string{
		"${MOONSHOT_API_KEY}",
		"  ${VAR}  ",
		"!op read op://vault/openai/key",
		"COPILOT_GITHUB_TOKEN",
		"ABCD",
	} {
		require.True(t, isReferenceValue(value), "%q is a reference", value)
	}
	for _, value := range []string{
		"sk-abcd1234",
		"abc.def/xyz=",
		"sk",
		"1234",
		"aB12",
		"ABC", // one character short of a bare identifier
	} {
		require.False(t, isReferenceValue(value), "%q is a literal", value)
	}
}

// TestReadInlineSecretOrderIsDeterministic: two provider entries yield their
// credentials in a stable, sorted-by-key order across repeated reads, not in
// map iteration order.
func TestReadInlineSecretOrderIsDeterministic(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"provider":{"beta":{"apiKey":"sk-beta"},"alpha":{"apiKey":"sk-alpha"}}}`)

	for range 5 {
		rb, err := Read(home, "opencode", Manifest{})

		require.NoError(t, err)
		require.Len(t, rb.Static, 2)
		require.Equal(t, "alpha", rb.Static[0].Provider)
		require.Equal(t, "beta", rb.Static[1].Provider)
	}
}

// TestReadInlineSecretDerivesEnvVarFromRawProvider: the derived env-var name
// keeps the raw provider token's word boundaries, so github-copilot yields
// GITHUB_COPILOT_API_KEY, while Credential.Provider stays the normalized form
// the endpoint-map key uses.
func TestReadInlineSecretDerivesEnvVarFromRawProvider(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"provider":{"github-copilot":{"apiKey":"sk-inline"}}}`)

	rb, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "githubcopilot", rb.Static[0].Provider, "the endpoint-map key is normalized")
	require.Equal(t, "GITHUB_COPILOT_API_KEY", rb.Static[0].EnvVar,
		"the derived name keeps the raw token's word boundaries")
}

// TestReadClassifiesInlineSecretFromJSON: the acceptance names JSON alongside
// TOML and YAML; a JSON/JSONC config with an inline provider key is classified.
func TestReadClassifiesInlineSecretFromJSON(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".config/opencode/opencode.jsonc",
		`{"provider":{"openai":{"api_key":"sk-json-inline"}}}`)

	rb, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Login)
	require.Len(t, rb.Static, 1)
	credential := rb.Static[0]
	require.Equal(t, CredentialStatic, credential.Kind)
	require.Equal(t, ".config/opencode/opencode.jsonc", credential.Source)
	require.Equal(t, "openai", credential.Provider)
	require.Equal(t, "OPENAI_API_KEY", credential.EnvVar)
	require.Equal(t, "sk-json-inline", credential.Value)
}

// TestReadInlineSecretSkipsAnEmptyNormalizedProvider: a provider name that
// normalizes to empty (e.g. "___") names no provider, so no credential is
// reported with an empty Provider and a bogus derived env-var name.
func TestReadInlineSecretSkipsAnEmptyNormalizedProvider(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: ___\n  api_key: sk-inline\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "a provider that normalizes to empty names no provider")
}

// TestConfigInlineCredentialsSkipsAnOpaqueEntry: an opaque (over-cap) entry has
// no content hash and is skipped before the reader opens it.
func TestConfigInlineCredentialsSkipsAnOpaqueEntry(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key: sk-inline\n")

	credentials := configInlineCredentials(home, Manifest{Entries: []ManifestEntry{
		{Key: ".hermes/config.yaml", Size: 1, Hash: ""},
	}})

	require.Empty(t, credentials, "an opaque entry carries no credential")
}

// TestReadInlineSecretSkipsALoginStore: a declared login store is excluded
// from the inline walk, so a token it holds is a login, never a static secret.
func TestReadInlineSecretSkipsALoginStore(t *testing.T) {
	home := t.TempDir()
	// The store is shaped with a provider entry so the inline walk would
	// consider its token: the login-store skip is what keeps it a login.
	writeFile(t, home, ".codex/auth.json", `{"provider":"codex","token":"super-secret"}`)

	rb, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Empty(t, rb.Static, "a login store must not yield a static credential")
	require.Len(t, rb.Login, 1)
	require.Equal(t, "codex", rb.Login[0].Provider)
}

// TestReadInlineSecretKeepsDotenvHostFill: a dotenv credential, whose file
// names no host, still gets its host from the configEndpointHosts fill.
func TestReadInlineSecretKeepsDotenvHostFill(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/.env", "OPENAI_API_KEY=sk-secret\n")
	writeFile(t, home, ".hermes/config.yaml",
		"model:\n  provider: openai\n  base_url: https://api.example/v1\n")

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, "openai", rb.Static[0].Provider)
	require.Equal(t, []string{"api.example"}, rb.Static[0].Hosts)
}

// TestReadInlineSecretKeepsItsOwnEntryHost: when a second entry names the same
// provider with a different endpoint, the inline credential keeps its own
// entry's host, not the provider-keyed union the fill would otherwise apply.
func TestReadInlineSecretKeepsItsOwnEntryHost(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml", strings.Join([]string{
		"model:",
		"  provider: openai",
		"  api_key: sk-inline",
		"  base_url: https://own.example/v1",
		"fallback:",
		"  provider: openai",
		"  base_url: https://other.example/v1",
		"",
	}, "\n"))

	rb, err := Read(home, "hermes", Manifest{})

	require.NoError(t, err)
	require.Len(t, rb.Static, 1)
	require.Equal(t, []string{"own.example"}, rb.Static[0].Hosts,
		"the credential's own entry host, not the provider-keyed union")
}

// TestConfigInlineCredentialsSkipsAnUnknownExtension: only a config format the
// decoder knows is walked, so an unrecognised file yields no credential.
func TestConfigInlineCredentialsSkipsAnUnknownExtension(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/notes.txt", "provider: moonshot\napi_key: sk\n")

	credentials := configInlineCredentials(home, Manifest{Entries: []ManifestEntry{
		{Key: ".hermes/notes.txt", Size: 1, Hash: "abc"},
	}})

	require.Empty(t, credentials)
}

// configEntry is a readable provider entry that declares an endpoint host.
func configEntry(key string) ManifestEntry {
	return ManifestEntry{Key: key, Size: 1, ModTime: time.Now(), Hash: "abc"}
}

// TestConfigEndpointHostsFiltersProbeAndOpaqueEntries pins that a probe entry
// and an opaque (hashless) entry contribute no endpoint even when a readable
// provider config stands behind them: only a real, hashed config file is
// considered.
func TestConfigEndpointHostsFiltersProbeAndOpaqueEntries(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml", "model:\n  provider: openai\n  base_url: https://api.example/v1\n")

	probe := Manifest{Entries: []ManifestEntry{{Key: ".hermes/config.yaml", Hash: "abc"}}}
	require.Empty(t, configEndpointHosts(home, probe), "a probe entry is not a config file")

	opaque := Manifest{Entries: []ManifestEntry{{Key: ".hermes/config.yaml", Size: 1, Hash: ""}}}
	require.Empty(t, configEndpointHosts(home, opaque), "an opaque entry carries no endpoint")
}

// TestConfigEndpointHostsProcessesEntriesAfterSkippedOnes pins that an entry
// skipped for an unknown extension, an undecodable file, or a parse error does
// not stop the scan: a later readable provider config still supplies its host.
// The undecodable file is crafted to leave a partial, host-bearing map behind
// its error, so the parse error must discard it rather than trusting it.
func TestConfigEndpointHostsProcessesEntriesAfterSkippedOnes(t *testing.T) {
	home := t.TempDir()
	// The error is a sibling of the provider entry, so the decoder leaves the
	// provider entry populated in the partial map its error returns.
	writeFile(t, home, ".hermes/broken.yaml",
		"model:\n  provider: openai\n  base_url: https://leak.example\nbad: !!int notanint\n")
	writeFile(t, home, ".hermes/notes.txt", "provider: openai\nbase_url: https://notes.example\n")
	writeFile(t, home, ".hermes/config.yaml", "model:\n  provider: openai\n  base_url: https://api.example/v1\n")

	manifest := Manifest{Entries: []ManifestEntry{
		// A probe entry first: skipping it must not stop the scan.
		{Key: ".hermes:probe", Hash: "abc"},
		configEntry(".hermes/broken.yaml"),
		configEntry(".hermes/notes.txt"),
		configEntry(".hermes/config.yaml"),
	}}

	hosts := configEndpointHosts(home, manifest)

	require.Equal(t, []string{"api.example"}, hosts["openai"])
	require.Len(t, hosts, 1, "only the readable, parseable provider config contributes")
}

// TestConfigEndpointHostsCompactsEveryProvider pins that every provider's hosts
// are sorted and de-duplicated, so adding an endpoint in another config file
// does not leave an unsorted or duplicated host list.
func TestConfigEndpointHostsCompactsEveryProvider(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml", strings.Join([]string{
		"first:",
		"  provider: openai",
		"  base_url: https://b.example",
		"second:",
		"  provider: openai",
		"  base_url: https://b.example",
		"third:",
		"  provider: openai",
		"  base_url: https://a.example",
		"fourth:",
		"  provider: anthropic",
		"  base_url: https://d.example",
		"fifth:",
		"  provider: anthropic",
		"  base_url: https://c.example",
		"",
	}, "\n"))

	hosts := configEndpointHosts(home, Manifest{Entries: []ManifestEntry{configEntry(".hermes/config.yaml")}})

	require.Equal(t, []string{"a.example", "b.example"}, hosts["openai"])
	require.Equal(t, []string{"c.example", "d.example"}, hosts["anthropic"])
}

// TestConfigEndpointHostsHasNoEntryWithoutAnEndpoint pins that a provider entry
// with no endpoint contributes no map entry at all: the hosts map must not
// carry empty provider keys.
func TestConfigEndpointHostsHasNoEntryWithoutAnEndpoint(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".hermes/config.yaml", "model:\n  provider: openai\n")

	hosts := configEndpointHosts(home, Manifest{Entries: []ManifestEntry{configEntry(".hermes/config.yaml")}})

	require.Empty(t, hosts)
}

// TestProviderNamedByEntryNeedsAProvider pins that only an explicit non-empty
// provider field or a container key names a provider; an empty field, a
// providerless entry, or a name that normalizes to empty names none. The token
// is returned verbatim; callers normalize it for the endpoint-map and credential
// keys.
func TestProviderNamedByEntryNeedsAProvider(t *testing.T) {
	provider, ok := providerNamedByEntry(map[string]any{"url": "https://x.example"}, "")
	require.False(t, ok, "a providerless entry names no provider")
	require.Empty(t, provider)

	provider, ok = providerNamedByEntry(map[string]any{"provider": ""}, "")
	require.False(t, ok, "an empty provider field names no provider")
	require.Empty(t, provider)

	provider, ok = providerNamedByEntry(map[string]any{"provider": "OpenAI"}, "")
	require.True(t, ok)
	require.Equal(t, "OpenAI", provider, "the raw token is returned")

	provider, ok = providerNamedByEntry(map[string]any{}, "OpenAI")
	require.True(t, ok, "a provider-container key names the provider")
	require.Equal(t, "OpenAI", provider)

	provider, ok = providerNamedByEntry(map[string]any{"provider": "___"}, "")
	require.False(t, ok, "a name that normalizes to empty names no provider")
	require.Empty(t, provider)

	provider, ok = providerNamedByEntry(map[string]any{}, "___")
	require.False(t, ok, "a container key that normalizes to empty names no provider")
	require.Empty(t, provider)
}

// TestEndpointHostsIgnoresNonEndpointKeys pins that only a value under an
// endpoint key yields a host: a URL under an unrelated key is not an endpoint.
func TestEndpointHostsIgnoresNonEndpointKeys(t *testing.T) {
	require.Empty(t, endpointHosts(map[string]any{"name": "https://not-an-endpoint.example"}))
	require.Equal(t, []string{"api.example"}, endpointHosts(map[string]any{"url": "https://api.example"}))
}

// TestIsEndpointKeyMatchesEndpointNames pins the endpoint-key vocabulary.
func TestIsEndpointKeyMatchesEndpointNames(t *testing.T) {
	require.True(t, isEndpointKey("baseURL"))
	require.True(t, isEndpointKey("endpoint"))
	require.True(t, isEndpointKey("url"))
	require.False(t, isEndpointKey("name"), "a non-endpoint key is not an endpoint")
	require.False(t, isEndpointKey(""), "an empty key is not an endpoint")
}

// TestIsProviderContainerRecognizesOnlyProviderKeys pins the container
// vocabulary; a non-provider key is not a container.
func TestIsProviderContainerRecognizesOnlyProviderKeys(t *testing.T) {
	require.True(t, isProviderContainer("provider"))
	require.True(t, isProviderContainer("Providers"))
	require.False(t, isProviderContainer("mcp"))
	require.False(t, isProviderContainer(""))
}
