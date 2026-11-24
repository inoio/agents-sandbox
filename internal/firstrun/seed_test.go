package firstrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
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
	require.Equal(t, 1, migrated.Secrets)

	slugDir := paths.UserSlugConfigDir("widget-abc")
	copied, err := os.ReadFile(filepath.Join(slugDir, "hermes", "config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(copied), "${MOONSHOT_API_KEY}")
	require.NotContains(t, string(copied), "sk-inline", "the raw value never reaches the copy")

	copiedInfo, err := os.Stat(filepath.Join(slugDir, "hermes", "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), copiedInfo.Mode().Perm(),
		"the managed config is ordinary configuration, written 0o644")

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
	require.Zero(t, migrated.Secrets)

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

	require.Equal(t, 1, migrated.Secrets, "the readable sibling is still seeded")
	require.Error(t, err, "the undecodable file is reported")
	require.Contains(t, err.Error(), "config-broken.yaml")
	require.NotContains(t, err.Error(), "merges no snippet",
		"an unseedable-only report must not name the unmergeable clause")
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
	require.Equal(t, 1, migrated.Secrets)
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

	require.Equal(t, 1, migrated.Secrets, "the mergeable snippet is still seeded")
	require.Error(t, err, "the non-mergeable config file is reported")
	require.Contains(t, err.Error(), "config.json")
	require.Contains(t, err.Error(), "merges no snippet")
	require.NotContains(t, err.Error(), "could not be seeded",
		"an unmergeable-only report must not name the unseedable clause")
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

	require.Zero(t, migrated.Secrets)
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

	require.Zero(t, migrated.Secrets)
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

	require.Zero(t, migrated.Secrets)
	require.Error(t, err, "a raw secret in an array under a secret-shaped key must refuse the file")
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "opencode", "opencode.jsonc"))
	require.True(t, os.IsNotExist(copyErr), "the file is never copied with a raw array secret")
}

// TestSeedFromHostRefusesAPluralSecretFamilyKey pins the Critical guard for a
// plural secret-list key: the Critical's own example {"tokens":["sk-raw"]} must
// refuse the whole file rather than copy the raw value into the managed config.
func TestSeedFromHostRefusesAPluralSecretFamilyKey(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc", `{"tokens":["sk-raw"]}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "opencode",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".config/opencode/opencode.jsonc")},
	}, paths, hostHome)

	require.Zero(t, migrated.Secrets)
	require.Error(t, err, "a raw secret under a plural secret-list key must refuse the file")
	_, copyErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "opencode", "opencode.jsonc"))
	require.True(t, os.IsNotExist(copyErr), "the file is never copied with a raw plural-key secret")
}

// TestSeedFromHostAcceptsAPluralKeyOfReferences pins that the widened key match
// does not over-refuse the seed: a plural key whose values are all non-strings
// (a token count) is migrated.Secrets, and a plural key of the credential's own
// reference-shaped value is accepted by the guard.
func TestSeedFromHostAcceptsAPluralKeyOfReferences(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc",
		`{"provider":{"openai":{"options":{"apiKey":"sk-good",`+
			`"baseURL":"https://api.openai.com/v1"}}},"tokens":[4096,true,null]}`)

	migrated, err := SeedFromHost(Facts{
		Agent:     "opencode",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".config/opencode/opencode.jsonc")},
	}, paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, 1, migrated.Secrets, "a plural key of non-string values is still seeded")
}

// TestSeedFromHostRefusesOnlyTheLeakingFile pins that a refused file does not
// stop the seed: a clean sibling is still migrated.Secrets and copied, while the file
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

	require.Equal(t, 1, migrated.Secrets, "the clean sibling is still seeded")
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

	require.Zero(t, migrated.Secrets)
	require.Error(t, err)
	slugDir := paths.UserSlugConfigDir("widget-abc")
	_, copyErr := os.Stat(filepath.Join(slugDir, "hermes", "config.yaml"))
	require.True(t, os.IsNotExist(copyErr), "a file with an unscopable credential is never copied raw")
	_, secretErr := os.Stat(filepath.Join(slugDir, "env.secret.yaml"))
	require.True(t, os.IsNotExist(secretErr), "no secret is written for an unscopable credential")
}

// TestSeedFromHostRejectsAnEmptySlug pins that the seed needs a repo slug for
// the per-slug scope it writes into: without one there is nowhere to seed.
func TestSeedFromHostRejectsAnEmptySlug(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	migrated, err := SeedFromHost(Facts{Agent: "hermes"}, configpaths.Get(), t.TempDir())

	require.Error(t, err)
	require.Zero(t, migrated.Secrets)
}

// TestSeedFromHostRejectsAnUnknownAgent pins that the seed resolves the agent
// profile before touching the host config.
func TestSeedFromHostRejectsAnUnknownAgent(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)

	migrated, err := SeedFromHost(
		Facts{Agent: "not-a-registered-agent", Slug: "widget-abc"},
		configpaths.Get(),
		t.TempDir(),
	)

	require.Error(t, err)
	require.Zero(t, migrated.Secrets)
}

// TestSeedFromHostRejectsAnInvalidAgentConfigDirName pins that the seed
// surfaces ConfigDirNames' invalid-name refusal rather than writing into a
// wrong or parent-traversing scope.
func TestSeedFromHostRejectsAnInvalidAgentConfigDirName(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	agent.Register(badConfigDirAgent{})

	migrated, err := SeedFromHost(
		Facts{Agent: badConfigDirAgent{}.Name(), Slug: "widget-abc"},
		configpaths.Get(),
		t.TempDir(),
	)

	require.Error(t, err)
	require.Zero(t, migrated.Secrets)
	require.Error(t, errors.Unwrap(err), "the config-dir-names error must be wrapped, not flattened")
}

// TestSeedFromHostReportsAnOverCapFile pins the bounded read: a host file over
// the read-back cap is left unseeded and reported rather than loaded raw.
func TestSeedFromHostReportsAnOverCapFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, hermesHostConfigPath, strings.Repeat("x", readback.MaxHashedBytes+1))

	migrated, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.Zero(t, migrated.Secrets)
	require.Error(t, err)
}

// TestSeedFromHostReportsAnUnwritableSecretFile pins the secret-write failure
// path: a per-slug secret file the writer cannot edit leaves the whole file
// unseeded and reported, so no placeholder is written without its secret.
func TestSeedFromHostReportsAnUnwritableSecretFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, hermesHostConfigPath, hermesHostConfig)
	scopeDir := paths.UserSlugConfigDir("widget-abc")
	require.NoError(t, os.WriteFile(
		filepath.Join(scopeDir, "env.secret.yaml"),
		[]byte("\tnot: [valid"),
		0o600,
	))

	migrated, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.Zero(t, migrated.Secrets)
	require.Error(t, err)
}

// TestWriteSeedSecretsPreservesTheCause pins the ERRORF_WRAP contract of the
// shared secret writer: a write failure keeps its cause reachable, so a caller
// can tell the failure apart.
func TestWriteSeedSecretsPreservesTheCause(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "env.secret.yaml"), []byte("\tnot: [valid"), 0o600))

	err := writeSeedSecrets(dir, []readback.Credential{{
		EnvVar: "MOONSHOT_API_KEY", Value: "sk-inline", Hosts: []string{"h"},
	}})

	require.Error(t, err)
	require.Error(t, errors.Unwrap(err), "the secret-write error must be wrapped, not flattened")
}

// TestSeedFromHostReportsAFailedManagedWrite pins the managed-config write
// failure: when the target cannot be written, the file is left unseeded and
// reported. The config holds no credential, so no secret is written first.
func TestSeedFromHostReportsAFailedManagedWrite(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, hermesHostConfigPath, "model:\n  provider: moonshot\n")
	scopeDir := paths.UserSlugConfigDir("widget-abc")
	require.NoError(t, os.WriteFile(filepath.Join(scopeDir, "hermes"), []byte("x"), 0o644))

	migrated, err := SeedFromHost(seedFacts(hostHome), paths, hostHome)

	require.Zero(t, migrated.Secrets)
	require.Error(t, err)
}

// TestSeedOneHostFilePreservesTheManagedWriteCause pins the ERRORF_WRAP
// contract of the managed-config write: the OS cause stays reachable through the
// wrapper, so a caller can tell the write failure apart.
func TestSeedOneHostFilePreservesTheManagedWriteCause(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, hermesHostConfigPath, "model:\n  provider: moonshot\n")
	scopeDir := paths.UserSlugConfigDir("widget-abc")
	targetDir := filepath.Join(scopeDir, "hermes")
	require.NoError(t, os.WriteFile(targetDir, []byte("x"), 0o644))

	migrated, err := seedOneHostFile(
		"hermes",
		hermesHostConfigPath,
		filepath.Join(hostHome, hermesHostConfigPath),
		scopeDir,
		targetDir,
	)

	require.Error(t, err)
	require.Zero(t, migrated)
	require.ErrorIs(t, err, syscall.ENOTDIR, "the OS cause must stay reachable through the wrapper")
}

// TestSeedOneHostFileReportsZeroOnEachFailure pins the returned migration count
// on every failure path: SeedFromHost discards the count when the error is
// returned, so each path is only observable through a direct call.
func TestSeedOneHostFileReportsZeroOnEachFailure(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()

	t.Run("over the read cap", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, hermesHostConfigPath, strings.Repeat("x", readback.MaxHashedBytes+1))
		scopeDir := paths.UserSlugConfigDir("slug-overcap")
		migrated, err := seedOneHostFile("hermes", hermesHostConfigPath,
			filepath.Join(hostHome, hermesHostConfigPath), scopeDir, filepath.Join(scopeDir, "hermes"))
		require.Error(t, err)
		require.Zero(t, migrated)
	})

	t.Run("an undecodable config", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, hermesHostConfigPath,
			"model:\n  provider: openai\n  api_key: sk\nbroken: !!int notanint\n")
		scopeDir := paths.UserSlugConfigDir("slug-undecodable")
		migrated, err := seedOneHostFile("hermes", hermesHostConfigPath,
			filepath.Join(hostHome, hermesHostConfigPath), scopeDir, filepath.Join(scopeDir, "hermes"))
		require.Error(t, err)
		require.Zero(t, migrated)
	})

	t.Run("an unscopable credential", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, hermesHostConfigPath, "model:\n  provider: moonshot\n  api_key: sk-inline\n")
		scopeDir := paths.UserSlugConfigDir("slug-unscopable")
		migrated, err := seedOneHostFile("hermes", hermesHostConfigPath,
			filepath.Join(hostHome, hermesHostConfigPath), scopeDir, filepath.Join(scopeDir, "hermes"))
		require.Error(t, err)
		require.Zero(t, migrated)
	})

	t.Run("an unwritable secret file", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, hermesHostConfigPath, hermesHostConfig)
		scopeDir := paths.UserSlugConfigDir("slug-secret")
		require.NoError(t, os.WriteFile(filepath.Join(scopeDir, "env.secret.yaml"), []byte("\tnot: [valid"), 0o600))
		migrated, err := seedOneHostFile("hermes", hermesHostConfigPath,
			filepath.Join(hostHome, hermesHostConfigPath), scopeDir, filepath.Join(scopeDir, "hermes"))
		require.Error(t, err)
		require.Zero(t, migrated)
	})

	t.Run("a failed managed write", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, hermesHostConfigPath, hermesHostConfig)
		scopeDir := paths.UserSlugConfigDir("slug-managed")
		targetDir := filepath.Join(scopeDir, "hermes")
		require.NoError(t, os.WriteFile(targetDir, []byte("x"), 0o644))
		migrated, err := seedOneHostFile("hermes", hermesHostConfigPath,
			filepath.Join(hostHome, hermesHostConfigPath), scopeDir, targetDir)
		require.Error(t, err)
		require.Zero(t, migrated)
	})
}

// TestHostRelativePathFallsBackOutsideHome pins the fallback: a path outside the
// host home is reduced to its base name, so the classifier still gets a
// meaningful trailing element rather than a parent-traversing path.
func TestHostRelativePathFallsBackOutsideHome(t *testing.T) {
	hostHome := t.TempDir()

	got := hostRelativePath(hostHome, filepath.Join(t.TempDir(), "elsewhere", "config.yaml"))

	require.Equal(t, "config.yaml", got)
}

// TestHostRelativePathFallsBackWhenRelFails pins the fallback for a host home and
// path that cannot be relativized (one relative, one absolute): the base name is
// used rather than an empty key.
func TestHostRelativePathFallsBackWhenRelFails(t *testing.T) {
	got := hostRelativePath("relative/home", filepath.Join(string(filepath.Separator), "abs", "config.yaml"))

	require.Equal(t, "config.yaml", got)
}

// TestHostRelativePathFallsBackAboveHome pins the parent case: a path that is
// the host home's parent relativizes to "..", which is not a usable home-relative
// key, so it falls back to the base name.
func TestHostRelativePathFallsBackAboveHome(t *testing.T) {
	hostHome := filepath.Join(t.TempDir(), "home")

	got := hostRelativePath(hostHome, filepath.Dir(hostHome))

	require.Equal(t, filepath.Base(filepath.Dir(hostHome)), got)
}

// TestPlaceholderStaticValuesRefusesAnUnmigratableCredential pins the guard's
// contract for each missing field: a credential with no value or no host cannot
// be migrated.Secrets, so the whole file is refused rather than copied with a raw value
// or an orphan placeholder. A credential with no env-var name is refused too,
// but by the sanitizer downstream rather than this guard. The content holds the
// value under an ordinary key, so the refuse-undeclared-secret guard does not
// mask the field guard by refusing for its own reason. The no-value row names an
// unknown format: for a known format the sanitizer refuses an empty value for
// its own reason (the empty-value substitution cannot decode), which would mask
// the guard, so only an unknown-format path makes the missing value observable.
func TestPlaceholderStaticValuesRefusesAnUnmigratableCredential(t *testing.T) {
	const knownPath = ".hermes/config.yaml"
	content := []byte("model:\n  name: sk-inline\n")
	tests := []struct {
		name        string
		path        string
		credential  readback.Credential
		wantRefused bool
	}{
		{
			name:        "no env-var name",
			path:        knownPath,
			credential:  readback.Credential{Value: "sk-inline", Hosts: []string{"h"}},
			wantRefused: true,
		},
		{
			name:        "no value",
			path:        "notes.txt",
			credential:  readback.Credential{EnvVar: "MOONSHOT_API_KEY", Hosts: []string{"h"}},
			wantRefused: true,
		},
		{
			name:        "no host",
			path:        knownPath,
			credential:  readback.Credential{EnvVar: "MOONSHOT_API_KEY", Value: "sk-inline"},
			wantRefused: true,
		},
		{
			name:       "migratable",
			path:       knownPath,
			credential: readback.Credential{EnvVar: "MOONSHOT_API_KEY", Value: "sk-inline", Hosts: []string{"h"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := placeholderStaticValues(test.path, content, []readback.Credential{test.credential}, "")
			if test.wantRefused {
				require.False(t, ok, "a credential missing a field must refuse the file")
				require.Empty(t, got)
				return
			}
			require.True(t, ok)
			require.Contains(t, got, "${MOONSHOT_API_KEY}")
			require.NotContains(t, got, "sk-inline")
		})
	}
}

// TestSeedFromHostSeedsEveryFileInTheList pins the accumulator: every
// seedable host file is migrated.Secrets, so the returned count is the sum, not the
// last file's count.
func TestSeedFromHostSeedsEveryFileInTheList(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)
	writeHostFile(t, hostHome, ".hermes/config-second.yaml",
		"model:\n  provider: openai\n  api_key: sk-second\n  base_url: https://api.openai.com/v1\n")

	migrated, err := SeedFromHost(Facts{
		Agent: "hermes",
		Slug:  "widget-abc",
		HostFiles: []string{
			filepath.Join(hostHome, ".hermes/config.yaml"),
			filepath.Join(hostHome, ".hermes/config-second.yaml"),
		},
	}, paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, 2, migrated.Secrets, "both seedable files must contribute to the count")
}

// TestSeedFromHostContinuesPastASkippedFile pins that a skip does not end the
// loop: a skipped file earlier in the list must not stop a later seedable file
// from being migrated.Secrets.
func TestSeedFromHostContinuesPastASkippedFile(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()

	t.Run("an inline-credential store", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, ".local/share/opencode/auth.json", `{"openai":{"type":"api","key":"sk-a"}}`)
		writeHostFile(t, hostHome, ".config/opencode/opencode.jsonc",
			`{"provider":{"openai":{"options":{"apiKey":"sk-good",`+
				`"baseURL":"https://api.openai.com/v1"}}}}`)

		migrated, err := SeedFromHost(Facts{
			Agent: "opencode",
			Slug:  "widget-abc",
			HostFiles: []string{
				filepath.Join(hostHome, ".local/share/opencode/auth.json"),
				filepath.Join(hostHome, ".config/opencode/opencode.jsonc"),
			},
		}, paths, hostHome)

		require.NoError(t, err)
		require.Equal(t, 1, migrated.Secrets, "a skipped value store must not stop the later seedable file")
	})

	t.Run("a non-config file", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, ".hermes/notes.txt", "provider: moonshot\napi_key: sk-raw\n")
		writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)

		migrated, err := SeedFromHost(Facts{
			Agent: "hermes",
			Slug:  "widget-abc",
			HostFiles: []string{
				filepath.Join(hostHome, ".hermes/notes.txt"),
				filepath.Join(hostHome, ".hermes/config.yaml"),
			},
		}, paths, hostHome)

		require.NoError(t, err)
		require.Equal(t, 1, migrated.Secrets, "a skipped non-config file must not stop the later seedable file")
	})

	t.Run("a non-mergeable config file", func(t *testing.T) {
		hostHome := t.TempDir()
		writeHostFile(t, hostHome, ".hermes/settings.yaml", "model:\n  provider: openai\n")
		writeHostFile(t, hostHome, ".hermes/config.yaml", hermesHostConfig)

		migrated, err := SeedFromHost(Facts{
			Agent: "hermes",
			Slug:  "widget-abc",
			HostFiles: []string{
				filepath.Join(hostHome, ".hermes/settings.yaml"),
				filepath.Join(hostHome, ".hermes/config.yaml"),
			},
		}, paths, hostHome)

		require.Equal(t, 1, migrated.Secrets, "a skipped non-mergeable file must not stop the later seedable file")
		require.Error(t, err, "the non-mergeable file is still reported")
	})
}

func TestTruncatedListNamesTheFirstTenPaths(t *testing.T) {
	paths := make([]string, 12)
	for i := range paths {
		paths[i] = fmt.Sprintf("/home/dev/.claude/skills/s%02d.md", i)
	}

	require.Equal(t, strings.Join(paths[:10], ", "), truncatedList(paths[:10]), "ten paths are all named")
	require.Equal(t, strings.Join(paths[:10], ", ")+", and 2 more", truncatedList(paths))
}

func TestSeedSkipsReportsASingleOverCapFile(t *testing.T) {
	err := seedSkips{overCapInstructions: 1}.err()

	require.EqualError(t, err, fmt.Sprintf(
		"firstrun: skipped 1 instruction file(s) over the seed cap of %d files and %d bytes",
		maxSeedInstructionFiles, maxSeedInstructionBytes))
}

// TestSeedFromHostWritesCodexsNativeIndirection pins that the seed writes a
// harness's native env indirection where it does not expand ${VAR} in its
// config: codex's inline experimental_bearer_token becomes env_key naming the
// secret's variable, which msb fills with the placeholder.
func TestSeedFromHostWritesCodexsNativeIndirection(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".codex/config.toml", `model_provider = "corp"

[model_providers.corp]
name = "corp"
base_url = "https://llm.corp.example/v1"
experimental_bearer_token = "sk-corp-inline"
`)
	facts := Facts{
		Agent:     "codex",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".codex/config.toml")},
	}

	migrated, err := SeedFromHost(facts, paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, 1, migrated.Secrets)
	slugDir := paths.UserSlugConfigDir("widget-abc")
	copied, err := os.ReadFile(filepath.Join(slugDir, "codex", "config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(copied), `env_key = "CORP_API_KEY"`)
	require.NotContains(t, string(copied), "experimental_bearer_token")
	require.NotContains(t, string(copied), "sk-corp-inline", "the raw value never reaches the copy")
	require.Equal(t, []string{"llm.corp.example"}, readSeedSecrets(t, slugDir)["CORP_API_KEY"].Hosts)
}

// TestSeedFromHostRefusesACodexHeaderToken pins that a token in a codex
// provider's http_headers, which the read-back does not classify, keeps the
// file out of the managed scope rather than reaching it verbatim.
func TestSeedFromHostRefusesACodexHeaderToken(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".codex/config.toml", `[model_providers.corp]
base_url = "https://llm.corp.example/v1"
http_headers = { "X-Corp-Auth" = "corp-secret-value-123" }
`)
	facts := Facts{
		Agent:     "codex",
		Slug:      "widget-abc",
		HostFiles: []string{filepath.Join(hostHome, ".codex/config.toml")},
	}

	_, err := SeedFromHost(facts, paths, hostHome)

	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(paths.UserSlugConfigDir("widget-abc"), "codex", "config.toml"))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the file is not seeded")
}
