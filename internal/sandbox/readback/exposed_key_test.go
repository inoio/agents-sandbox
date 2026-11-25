package readback

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// codexExposedKey is what the read-back reports for a plain-text key in codex's
// auth.json.
var codexExposedKey = ExposedKey{
	Source:   ".codex/auth.json",
	Provider: "openai",
	EnvVar:   "OPENAI_API_KEY",
	Hosts:    []string{"api.openai.com"},
	Remedy: "then run `printenv OPENAI_API_KEY | codex login --with-api-key` in " +
		"`agents-sandbox shell --agent codex`, so auth.json stores the secret's placeholder instead",
}

func TestReadReportsAPlainTextKeyInCodexsAuthJSON(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"api-key mode":       `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-proj-real"}`,
		"no auth_mode field": `{"OPENAI_API_KEY":"sk-proj-real"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			writeFile(t, home, ".codex/auth.json", content)

			readBack, err := Read(home, "codex", Manifest{})

			require.NoError(t, err)
			require.Equal(t, []ExposedKey{codexExposedKey}, readBack.Exposed)
			require.Empty(t, readBack.Static, "the key is reported, never read as a static credential")
		})
	}
}

func TestReadReportsNoExposedKeyWithoutAPlainTextKey(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"chatgpt login":           `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"access_token":"x"}}`,
		"msb default placeholder": `{"auth_mode":"apikey","OPENAI_API_KEY":"$MSB_OPENAI_API_KEY"}`,
		"explicit placeholder":    `{"auth_mode":"apikey","OPENAI_API_KEY":"${OPENAI_API_KEY}"}`,
		"empty key":               `{"auth_mode":"apikey","OPENAI_API_KEY":""}`,
		"not a json object":       `["sk-proj-real"]`,
		"not json":                `OPENAI_API_KEY=sk-proj-real`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			writeFile(t, home, ".codex/auth.json", content)

			readBack, err := Read(home, "codex", Manifest{})

			require.NoError(t, err)
			require.Empty(t, readBack.Exposed)
		})
	}
}

func TestReadReportsNoExposedKeyForAnotherAgentsStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, home, ".codex/auth.json", `{"OPENAI_API_KEY":"sk-proj-real"}`)

	readBack, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.Exposed)
}

func TestReadDoesNotFollowAStoreSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "host-file.json")
	require.NoError(t, os.WriteFile(outside, []byte(`{"OPENAI_API_KEY":"sk-proj-real"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, ".codex", "auth.json")))

	readBack, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.Exposed, "a symlink the agent planted is never followed out of the home")
}

func TestReadDoesNotInspectAnOversizedStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	padding := strings.Repeat(" ", maxExposedKeyStoreBytes)
	writeFile(t, home, ".codex/auth.json", `{"OPENAI_API_KEY":"sk-proj-real"}`+padding)

	readBack, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.Exposed)
}

func TestReadReportsAKeyAtTheStoreSizeBound(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	document := `{"OPENAI_API_KEY":"sk-proj-real"}`
	writeFile(t, home, ".codex/auth.json", document+strings.Repeat(" ", maxExposedKeyStoreBytes-len(document)))

	readBack, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Len(t, readBack.Exposed, 1)
}

func TestReadReportsAPlainTextKeyInClaudeCodesGlobalConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, home, ".claude.json", `{"hasCompletedOnboarding":true,"primaryApiKey":"sk-ant-real","projects":{}}`)

	readBack, err := Read(home, "claude-code", Manifest{})

	require.NoError(t, err)
	require.Equal(t, []ExposedKey{{
		Source:   ".claude.json",
		Provider: "anthropic",
		EnvVar:   "ANTHROPIC_API_KEY",
		Hosts:    []string{"api.anthropic.com"},
		Remedy:   "then run `/logout` in claude-code, so it uses the key from its environment instead",
	}}, readBack.Exposed)
	require.NotContains(t, readBack.ConfigPaths, ".claude.json", "the global config is never a write-back candidate")
}

func TestReadReportsNoExposedKeyInClaudeCodesConfigWithoutOne(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"subscription login": `{"hasCompletedOnboarding":true,"oauthAccount":{"emailAddress":"x"}}`,
		"placeholder":        `{"primaryApiKey":"$MSB_ANTHROPIC_API_KEY"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			writeFile(t, home, ".claude.json", content)

			readBack, err := Read(home, "claude-code", Manifest{})

			require.NoError(t, err)
			require.Empty(t, readBack.Exposed)
		})
	}
}

func TestReadDoesNotFollowASymlinkedStoreDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	outside := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(outside, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-proj-real"}`), 0o600),
	)
	require.NoError(t, os.Symlink(outside, filepath.Join(home, ".codex")))

	readBack, err := Read(home, "codex", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.Exposed, "a symlinked parent directory never leads the read out of the home")
}

func TestExposedKeysDoNotBlockOnAStoreFIFO(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(home, ".codex", "auth.json"), 0o600))

	// exposedKeys alone: the manifest's own hashing of the declared path is a
	// separate read.
	require.Empty(t, exposedKeys(home, "codex"))
}

func TestExposedKeysReportNothingForAMissingHome(t *testing.T) {
	t.Parallel()

	require.Empty(t, exposedKeys(filepath.Join(t.TempDir(), "gone"), "codex"))
}
