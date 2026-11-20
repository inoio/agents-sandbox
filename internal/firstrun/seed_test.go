package firstrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// hermesHostConfigPath is the host path the seed test writes the config to.
const hermesHostConfigPath = ".hermes/config.yaml"

// hermesHostConfig is a host config with one inline provider key scoped by its
// entry's endpoint, the shape the seed is built around.
const hermesHostConfig = "model:\n" +
	"  provider: moonshot\n" +
	"  api_key: sk-inline\n" +
	"  base_url: https://api.custom.example/v1\n"

// seedFacts builds the facts the seed reads for the hermes host config under
// home.
func seedFacts(home string) Facts {
	return Facts{
		Agent:     "hermes",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(home, hermesHostConfigPath)},
	}
}

// readSeedSecrets parses the per-slug env.secret.yaml through the loader the VM
// build uses, so the assertion is on the written secret, not a byte string.
func readSeedSecrets(t *testing.T, slugDir string) map[string]envsecret.SecretSpec {
	t.Helper()
	path := filepath.Join(slugDir, "env.secret.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	ui := termiotest.NewTestMock(t)
	return envsecret.ParseSecretSpecYAMLData(path, data, &ui)
}

// TestSeedFromHostPlaceholdersStaticSecret pins the seed's core contract: a host
// config's inline static value is replaced by its ${ENVVAR} placeholder in the
// managed copy and its value lands only in env.secret.yaml — never the raw value
// in the copy.
func TestSeedFromHostPlaceholdersStaticSecret(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)

	migrated, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, 1, migrated)

	slugDir := paths.UserSlugConfigDir("widget-abc")
	copied, err := os.ReadFile(filepath.Join(slugDir, "hermes", "config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(copied), "${MOONSHOT_API_KEY}")
	require.NotContains(t, string(copied), "sk-inline", "the raw value never reaches the copy")

	secrets := readSeedSecrets(t, slugDir)
	require.Equal(t, envsecret.SecretSpec{
		Value:       "sk-inline",
		Hosts:       []string{"api.custom.example"},
		Placeholder: "${MOONSHOT_API_KEY}",
	}, secrets["MOONSHOT_API_KEY"])
}

// TestSeedFromHostDropsLoginEntry pins invariant 2: a login store is never
// copied into the managed scope and writes no secret.
func TestSeedFromHostDropsLoginEntry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".codex/auth.json", `{"tokens":{"access_token":"sk-oauth-secret"}}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "codex",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".codex/auth.json")},
	}, paths, hostHome)

	require.NoError(t, err)
	require.Zero(t, migrated)

	slugDir := paths.UserSlugConfigDir("widget-abc")
	_, copyErr := os.Stat(filepath.Join(slugDir, "codex", "auth.json"))
	require.True(t, os.IsNotExist(copyErr), "a login store is never copied")
	_, secretErr := os.Stat(filepath.Join(slugDir, "env.secret.yaml"))
	require.True(t, os.IsNotExist(secretErr), "a login writes no secret")
}

// TestSeedFromHostDerivesHostFromTheEntry pins invariant 5: the secret's host is
// the endpoint its own config entry names, not a curated provider table.
func TestSeedFromHostDerivesHostFromTheEntry(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml",
		"model:\n  provider: moonshot\n  api_key: sk-inline\n"+
			"  base_url: https://api.notcurated.example:8443/v1\n")

	_, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.NoError(t, err)
	secrets := readSeedSecrets(t, paths.UserSlugConfigDir("widget-abc"))
	require.Equal(t, []string{"api.notcurated.example"}, secrets["MOONSHOT_API_KEY"].Hosts,
		"the host comes from the entry's base_url, with scheme, port, and path dropped")
}

// TestSeedFromHostWritesOnlyThePerSlugScope pins that the seed never writes to
// the project scope: both the managed copy and the secret stay under the
// per-slug user scope.
func TestSeedFromHostWritesOnlyThePerSlugScope(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)

	_, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.NoError(t, err)
	_, projectCopyErr := os.Stat(filepath.Join(paths.ProjectConfigDir(), "hermes", "config.yaml"))
	require.True(t, os.IsNotExist(projectCopyErr), "no managed copy in the project scope")
	_, projectSecretErr := os.Stat(filepath.Join(paths.ProjectConfigDir(), "env.secret.yaml"))
	require.True(t, os.IsNotExist(projectSecretErr), "no secret in the project scope")
	_, perSlugErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "hermes", "config.yaml"))
	require.NoError(t, perSlugErr, "the managed copy lands in the per-slug user scope")
}

// TestSeedFromHostReportsAnUndecodableFile pins the reporting contract: a file
// of a known format that does not decode is reported and left unseeded, while a
// readable sibling is still seeded.
func TestSeedFromHostReportsAnUndecodableFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)
	writeHostFile(t, hostHome, ".hermes/config-broken.yaml",
		"model:\n  provider: openai\n  api_key: sk\nbroken: !!int notanint\n")

	migrated, err := SeedFromHost(Facts{
		Agent: "hermes",
		Slug:  "widget-abc",
		HostFiles: []string{
			filepath.Join(hostHome, ".hermes/config.yaml"),
			filepath.Join(hostHome, ".hermes/config-broken.yaml"),
		},
	}, paths, hostHome)

	require.Equal(t, 1, migrated, "the readable sibling is still seeded")
	require.Error(t, err, "the undecodable file is reported")
	require.Contains(t, err.Error(), "config-broken.yaml")
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "hermes", "config-broken.yaml"))
	require.True(t, os.IsNotExist(copyErr), "an undecodable file is never copied raw")
}

// TestSeedFromHostSilentlySkipsANonConfigFile pins the expected-skip contract:
// an agent's provision rules select broadly (`**`), so a `.txt` in the tree is
// skipped silently rather than reported as a failure.
func TestSeedFromHostSilentlySkipsANonConfigFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)
	writeHostFile(t, hostHome, ".hermes/notes.txt", "provider: moonshot\napi_key: sk-raw\n")

	migrated, err := SeedFromHost(Facts{
		Agent: "hermes",
		Slug:  "widget-abc",
		HostFiles: []string{
			filepath.Join(hostHome, ".hermes/config.yaml"),
			filepath.Join(hostHome, ".hermes/notes.txt"),
		},
	}, paths, hostHome)

	require.NoError(t, err, "a non-config file is an expected skip, not a failure")
	require.Equal(t, 1, migrated)
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "hermes", "notes.txt"))
	require.True(t, os.IsNotExist(copyErr), "a non-config file is never copied")
}

// TestSeedFromHostSkipsANonMergeableConfigFile pins that a known-format host
// file the agent's merge never reads is skipped and reported, while a mergeable
// snippet is still seeded: for opencode, config.json is not matched by
// opencode*.json*, so a managed copy of it would take no effect.
func TestSeedFromHostSkipsANonMergeableConfigFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-good",`+
			`"baseURL":"https://api.openai.com/v1"}}}}`)
	writeHostFile(t, hostHome, ".config/opencode/config.json", `{"theme":"dark"}`)

	migrated, err := SeedFromHost(Facts{
		Agent: "opencode",
		Slug:  "widget-abc",
		HostFiles: []string{
			filepath.Join(hostHome, ".config/opencode/opencode.jsonc"),
			filepath.Join(hostHome, ".config/opencode/config.json"),
		},
	}, paths, hostHome)

	require.Equal(t, 1, migrated, "the mergeable snippet is still seeded")
	require.Error(t, err, "the non-mergeable config file is reported")
	require.Contains(t, err.Error(), "config.json")
	slugDir := paths.UserSlugConfigDir("widget-abc")
	require.Contains(t, readSlugFile(t, slugDir, "opencode", "opencode.jsonc"), "${OPENAI_API_KEY}")
	_, configCopyErr := os.Stat(filepath.Join(slugDir, "opencode", "config.json"))
	require.True(t, os.IsNotExist(configCopyErr),
		"a non-mergeable config file is never written as an ineffective managed file")
}

// TestSeedFromHostRefusesAnMCPAuthorizationHeader pins the Critical guard: an
// MCP entry's Authorization header is not a classified credential, so without
// the secret-shaped-key guard its raw value would reach the managed config. The
// whole file is left unseeded and reported.
func TestSeedFromHostRefusesAnMCPAuthorizationHeader(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc",
		`{"mcp":{"github":{"headers":{"Authorization":"Bearer sk-mcp-secret"}}}}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "opencode",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".config/opencode/opencode.jsonc")},
	}, paths, hostHome)

	require.Zero(t, migrated)
	require.Error(t, err, "a raw Authorization header must refuse the file")
	slugDir := paths.UserSlugConfigDir("widget-abc")
	_, copyErr := os.Stat(filepath.Join(slugDir, "opencode", "opencode.jsonc"))
	require.True(t, os.IsNotExist(copyErr), "the file is never copied with a raw Authorization header")
}

// TestSeedFromHostRefusesAnUndeclaredAccessToken pins the guard for a provider
// entry key outside the recognized set: options.accessToken is not classified,
// so its raw value must refuse the file rather than be copied.
func TestSeedFromHostRefusesAnUndeclaredAccessToken(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc",
		`{"provider":{"openai":{"options":{"accessToken":"sk-raw-inline",`+
			`"baseURL":"https://api.openai.com/v1"}}}}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "opencode",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".config/opencode/opencode.jsonc")},
	}, paths, hostHome)

	require.Zero(t, migrated)
	require.Error(t, err)
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "opencode", "opencode.jsonc"))
	require.True(t, os.IsNotExist(copyErr))
}

// TestSeedFromHostRefusesAnUndeclaredSecretArray pins the Critical guard for a
// secret-shaped key holding an array of raw strings: the array's raw value must
// refuse the whole file rather than be copied verbatim into the managed config.
func TestSeedFromHostRefusesAnUndeclaredSecretArray(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc", `{"apiKeys":["sk-raw"]}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "opencode",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".config/opencode/opencode.jsonc")},
	}, paths, hostHome)

	require.Zero(t, migrated)
	require.Error(t, err, "a raw secret in an array under a secret-shaped key must refuse the file")
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "opencode", "opencode.jsonc"))
	require.True(t, os.IsNotExist(copyErr), "the file is never copied with a raw array secret")
}

// TestSeedFromHostRefusesOnlyTheLeakingFile pins that a refused file does not
// stop the seed: a clean sibling is still migrated and copied, while the file
// with an undeclared secret-shaped value is left unseeded and named.
func TestSeedFromHostRefusesOnlyTheLeakingFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode-good.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-good",`+
			`"baseURL":"https://api.openai.com/v1"}}}}`)
	writeHostFile(t, hostHome, ".config/opencode/opencode-bad.jsonc",
		`{"mcp":{"github":{"headers":{"Authorization":"Bearer sk-mcp-secret"}}}}`)

	migrated, err := SeedFromHost(Facts{
		Agent: "opencode",
		Slug:  "widget-abc",
		HostFiles: []string{
			filepath.Join(hostHome, ".config/opencode/opencode-good.jsonc"),
			filepath.Join(hostHome, ".config/opencode/opencode-bad.jsonc"),
		},
	}, paths, hostHome)

	require.Equal(t, 1, migrated, "the clean sibling is still seeded")
	require.Error(t, err)
	require.Contains(t, err.Error(), "opencode-bad.jsonc")
	slugDir := paths.UserSlugConfigDir("widget-abc")
	require.Contains(t, readSlugFile(t, slugDir, "opencode", "opencode-good.jsonc"), "${OPENAI_API_KEY}")
	_, badCopyErr := os.Stat(filepath.Join(slugDir, "opencode", "opencode-bad.jsonc"))
	require.True(t, os.IsNotExist(badCopyErr), "the leaking file is left unseeded")
}

// readSlugFile reads a file under the per-slug agent config dir, failing the
// test if it is absent.
func readSlugFile(t *testing.T, slugDir, agentDir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(slugDir, agentDir, name))
	require.NoError(t, err)
	return string(data)
}

// TestSeedFromHostLeavesAnUnscopableFileUnseeded pins the no-raw-value guard: a
// config whose credential has no derivable host cannot be seeded, so the whole
// file is left unseeded rather than copied with its raw value.
func TestSeedFromHostLeavesAnUnscopableFileUnseeded(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", "model:\n  provider: moonshot\n  api_key: sk-inline\n")

	migrated, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.Zero(t, migrated)
	require.Error(t, err)
	slugDir := paths.UserSlugConfigDir("widget-abc")
	_, copyErr := os.Stat(filepath.Join(slugDir, "hermes", "config.yaml"))
	require.True(t, os.IsNotExist(copyErr), "a file with an unscopable credential is never copied raw")
	_, secretErr := os.Stat(filepath.Join(slugDir, "env.secret.yaml"))
	require.True(t, os.IsNotExist(secretErr), "no secret is written for an unscopable credential")
}
