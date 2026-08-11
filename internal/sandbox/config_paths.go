package sandbox

import (
	"os"
	"path/filepath"
)

type ConfigPaths interface {
	UserConfigDir() string
	UserCacheDir() string
	UserStateDir() string
	UserOpencodeConfigDir() string

	userEnvFile() string
	userEnvSecretFile() string

	projectEnvSecretYAMLFile() string
	userEnvSecretYAMLFile() string

	ProjectConfigDir() string
	projectOpencodeConfigDir() string
	projectEnvFile() string
	projectEnvSecretFile() string
	projectDockerfile() string
}

type realConfigPaths struct{}

// GetConfigPaths is the factory clients can use to obtain an Client.
// Tests override Get to inject mocks.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var GetConfigPaths = func() ConfigPaths {
	return &realConfigPaths{}
}

// UserConfigDir returns the tool's user-config directory: $XDG_CONFIG_HOME/
// opencode-msb, defaulting to ~/.config/opencode-msb.
func (c *realConfigPaths) UserConfigDir() string {
	return filepath.Join(xdgBaseDir("XDG_CONFIG_HOME", ".config"), pathPrefix)
}

// UserCacheDir returns the tool's cache directory: $XDG_CACHE_HOME/
// opencode-msb, defaulting to ~/.cache/opencode-msb.
func (c *realConfigPaths) UserCacheDir() string {
	return filepath.Join(xdgBaseDir("XDG_CACHE_HOME", ".cache"), pathPrefix)
}

// UserStateDir returns the tool's state directory: $XDG_STATE_HOME/
// opencode-msb, defaulting to ~/.local/state/opencode-msb.
func (c *realConfigPaths) UserStateDir() string {
	return filepath.Join(xdgBaseDir("XDG_STATE_HOME", ".local/state"), pathPrefix)
}

// UserOpencodeConfigDir returns the directory of the opencode server's own
// user config, nested under the tool's user config base.
func (c *realConfigPaths) UserOpencodeConfigDir() string {
	return filepath.Join(c.UserConfigDir(), configDirName)
}

// ProjectConfigDir returns the tool's project-config directory: $PWD/opencode-msb.
func (c *realConfigPaths) ProjectConfigDir() string { return projectConfigDir }

// userEnvFile returns the user-level environment definitions file.
func (c *realConfigPaths) userEnvFile() string {
	return filepath.Join(c.UserConfigDir(), envFileName)
}

// userEnvSecretFile returns the user-level secret environment definitions file.
func (c *realConfigPaths) userEnvSecretFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretFileName)
}

const pathPrefix = "opencode-msb"

// projectConfigDir is the project-local metadata directory for the tool.
const projectConfigDir = "." + pathPrefix

// Shared names for config subdirectories and files, used by both the
// project-local and the user-level config path helpers.
const (
	configDirName     = "opencode"
	envFileName       = "env"
	envSecretFileName = "env.secret"
	//nolint:gosec // G101 false positive: filename constant
	envSecretYAMLFileName = "env.secret.yaml"
	dockerfileName        = "Dockerfile"
)

// Project-level filesystem paths, built with filepath.Join to mirror the
// user-level config path handling.
func (c *realConfigPaths) projectDockerfile() string {
	return filepath.Join(projectConfigDir, dockerfileName)
}

// projectOpencodeConfigDir is the project-local opencode config directory.
func (c *realConfigPaths) projectOpencodeConfigDir() string {
	return filepath.Join(projectConfigDir, configDirName)
}

func (c *realConfigPaths) projectEnvFile() string {
	return filepath.Join(projectConfigDir, envFileName)
}

func (c *realConfigPaths) projectEnvSecretFile() string {
	return filepath.Join(projectConfigDir, envSecretFileName)
}

// userEnvSecretYAMLFile returns the user-level structured secret file.
func (c *realConfigPaths) userEnvSecretYAMLFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretYAMLFileName)
}

// projectEnvSecretYAMLFile returns the project-level structured secret file.
func (c *realConfigPaths) projectEnvSecretYAMLFile() string {
	return filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName)
}

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
