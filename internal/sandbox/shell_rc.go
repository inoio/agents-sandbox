package sandbox

import (
	"path/filepath"
)

// shellRcFile returns the rc file a PATH export should be appended to, chosen by
// the basename of the login shell. Known shells resolve to specific rc files;
// unknown or empty shells fall back to the platform default (bashrc on Linux,
// zshrc on macOS).
func shellRcFile(home, shell string) string {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return filepath.Join(home, shellRcDefault)
	}
}
