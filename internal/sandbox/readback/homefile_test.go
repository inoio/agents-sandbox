package readback

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// opencodeConfigPath is a declared opencode read-back path.
const opencodeConfigPath = ".config/opencode/opencode.jsonc"

// hostProviderConfig is an opencode config holding an inline key, standing in
// for a host file an agent could point a symlink at.
const hostProviderConfig = `{"provider":{"corp":{"options":{"apiKey":"sk-host-secret","baseURL":"https://llm.corp.example"}}}}`

func TestReadDoesNotBlockOnAFIFOAtADeclaredPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.Dir(opencodeConfigPath)), 0o750))
	require.NoError(t, syscall.Mkfifo(filepath.Join(home, opencodeConfigPath), 0o600))

	readBack, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.ConfigPaths, "a FIFO is not a config file")
}

func TestReadDoesNotFollowADeclaredFileSymlinkOutOfTheHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hostFile := filepath.Join(t.TempDir(), "host.jsonc")
	require.NoError(t, os.WriteFile(hostFile, []byte(hostProviderConfig), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.Dir(opencodeConfigPath)), 0o750))
	require.NoError(t, os.Symlink(hostFile, filepath.Join(home, opencodeConfigPath)))

	readBack, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.ConfigPaths)
	require.Empty(t, readBack.Static, "a host file's key never reaches the read-back")
}

func TestReadDoesNotFollowASymlinkedConfigDirectoryOutOfTheHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "opencode.jsonc"), []byte(hostProviderConfig), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config"), 0o750))
	require.NoError(t, os.Symlink(hostDir, filepath.Join(home, ".config", "opencode")))

	readBack, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.ConfigPaths)
	require.Empty(t, readBack.Static)
}

func TestReadFollowsASymlinkThatStaysInTheHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, home, "dotfiles/opencode.jsonc", hostProviderConfig)
	require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.Dir(opencodeConfigPath)), 0o750))
	require.NoError(
		t,
		os.Symlink(filepath.Join("..", "..", "dotfiles", "opencode.jsonc"), filepath.Join(home, opencodeConfigPath)),
	)

	readBack, err := Read(home, "opencode", Manifest{})

	require.NoError(t, err)
	require.Equal(t, []string{opencodeConfigPath}, readBack.ConfigPaths)
	require.Len(t, readBack.Static, 1)
}

func TestReadRecordsASymlinkedOpencode2DatabaseUnreadable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hostHome := t.TempDir()
	writeOpenCode2DB(t, hostHome, [2]string{"openai", `{"type":"key","key":"sk-host"}`})
	require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.Dir(opencode2DBPath)), 0o750))
	require.NoError(t, os.Symlink(filepath.Join(hostHome, opencode2DBPath), filepath.Join(home, opencode2DBPath)))

	readBack, err := Read(home, "opencode2", Manifest{})

	require.NoError(t, err)
	require.Empty(t, readBack.Static, "a host database's key never reaches the read-back")
	require.Equal(t, []string{opencode2ProbeKey}, readBack.Unreadable)
}

func TestReadHomeFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, home, "small.json", "{}")
	writeFileSized(t, home, "large.json", 11)
	outside := filepath.Join(t.TempDir(), "outside.json")
	require.NoError(t, os.WriteFile(outside, []byte("{}"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(home, "escape.json")))

	content, ok := ReadHomeFile(home, "small.json", 10)
	require.True(t, ok)
	require.Equal(t, "{}", string(content))
	for name, rel := range map[string]string{
		"over the cap":     "large.json",
		"missing":          "gone.json",
		"symlink out":      "escape.json",
		"a directory":      ".",
		"escaping by path": "../outside.json",
	} {
		_, ok := ReadHomeFile(home, rel, 10)
		require.False(t, ok, name)
	}
	_, ok = ReadHomeFile(filepath.Join(home, "no-such-home"), "small.json", 10)
	require.False(t, ok, "a missing home")
}
