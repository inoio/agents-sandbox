package sandbox

import (
	"os"
	"path/filepath"
)

// xdgBaseDir resolves an XDG base directory for the given environment variable,
// falling back to $HOME/<fallback> when the variable is unset or holds a
// relative path (the XDG base directory spec only accepts absolute values). It
// returns the base directory without the application's own subdirectory; each
// caller appends "opencode-msb".
//
// The os package's UserConfigDir/UserCacheDir are deliberately not used: they
// return platform-specific paths (e.g. $HOME/Library/... on macOS) instead of
// honoring XDG on every platform. Cross-platform tools like git and curl read
// the XDG variables unconditionally, so we do the same.
func xdgBaseDir(env, fallback string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

// XdgConfigDir returns the tool's user-config directory: $XDG_CONFIG_HOME/
// opencode-msb, defaulting to ~/.config/opencode-msb.
func XdgConfigDir() string {
	return filepath.Join(xdgBaseDir("XDG_CONFIG_HOME", ".config"), Prefix)
}

// XdgCacheDir returns the tool's cache directory: $XDG_CACHE_HOME/
// opencode-msb, defaulting to ~/.cache/opencode-msb.
func XdgCacheDir() string {
	return filepath.Join(xdgBaseDir("XDG_CACHE_HOME", ".cache"), Prefix)
}

// XdgStateDir returns the tool's state directory: $XDG_STATE_HOME/
// opencode-msb, defaulting to ~/.local/state/opencode-msb.
func XdgStateDir() string {
	return filepath.Join(xdgBaseDir("XDG_STATE_HOME", ".local/state"), Prefix)
}
