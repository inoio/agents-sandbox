package configpaths

import (
	"os"
	"path/filepath"
)

type ConfigPaths interface {
	UserConfigDir() string
	UserCacheDir() string
	UserStateDir() string
	UserOpencodeConfigDir() string

	UserEnvFile() string
	UserEnvSecretFile() string
	UserEnvSecretYAMLFile() string

	ProjectConfigDir() string
	ProjectOpencodeConfigDir() string
	ProjectEnvFile() string
	ProjectEnvSecretFile() string
	ProjectEnvSecretYAMLFile() string
	ProjectDockerfile() string
}

type realConfigPaths struct{}

// Get is the factory clients can use to obtain an Client.
// Tests override Get to inject mocks.
//
//nolint:gochecknoglobals // test hook for the otherwise unmockable SDK
var Get = func() ConfigPaths {
	return &realConfigPaths{}
}

// UserConfigDir returns the tool's user-config directory: $XDG_CONFIG_HOME/
// opencode-sandbox, defaulting to ~/.config/opencode-sandbox.
func (c *realConfigPaths) UserConfigDir() string {
	return filepath.Join(xdgBaseDir("XDG_CONFIG_HOME", ".config"), pathPrefix)
}

// UserCacheDir returns the tool's cache directory: $XDG_CACHE_HOME/
// opencode-sandbox, defaulting to ~/.cache/opencode-sandbox.
func (c *realConfigPaths) UserCacheDir() string {
	return filepath.Join(xdgBaseDir("XDG_CACHE_HOME", ".cache"), pathPrefix)
}

// UserStateDir returns the tool's state directory: $XDG_STATE_HOME/
// opencode-sandbox, defaulting to ~/.local/state/opencode-sandbox.
func (c *realConfigPaths) UserStateDir() string {
	return filepath.Join(xdgBaseDir("XDG_STATE_HOME", ".local/state"), pathPrefix)
}

// UserOpencodeConfigDir returns the directory of the opencode server's own
// user config, nested under the tool's user config base.
func (c *realConfigPaths) UserOpencodeConfigDir() string {
	return filepath.Join(c.UserConfigDir(), configDirName)
}

// ProjectConfigDir returns the tool's project-config directory: $PWD/opencode-sandbox.
func (c *realConfigPaths) ProjectConfigDir() string { return projectConfigDir }

// UserEnvFile returns the user-level environment definitions file.
func (c *realConfigPaths) UserEnvFile() string {
	return filepath.Join(c.UserConfigDir(), envFileName)
}

// UserEnvSecretFile returns the user-level secret environment definitions file.
func (c *realConfigPaths) UserEnvSecretFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretFileName)
}

// UserEnvSecretYAMLFile returns the user-level structured secret file.
func (c *realConfigPaths) UserEnvSecretYAMLFile() string {
	return filepath.Join(c.UserConfigDir(), envSecretYAMLFileName)
}

const pathPrefix = "opencode-sandbox"

// projectConfigDir is the project-local metadata directory for the tool.
const projectConfigDir = "." + pathPrefix

// Shared names for config subdirectories and files, used by both the
// project-local and the user-level config path helpers.
const (
	pathPrefixName    = "opencode-sandbox"
	ConfigDirName     = "opencode"
	EnvFileName       = "env"
	EnvSecretFileName = "env.secret"
	//nolint:gosec // G101 false positive: filename constant
	envSecretYAMLFileName = "env.secret.yaml"
	DockerFileName        = "Dockerfile"
)

const (
	configDirName     = ConfigDirName
	envFileName       = EnvFileName
	envSecretFileName = EnvSecretFileName
	dockerfileName    = DockerFileName
)

// ProjectDockerfile returns the project Dockerfile path.
func (c *realConfigPaths) ProjectDockerfile() string {
	return filepath.Join(projectConfigDir, dockerfileName)
}

// ProjectOpencodeConfigDir returns the project-local opencode config directory.
func (c *realConfigPaths) ProjectOpencodeConfigDir() string {
	return filepath.Join(projectConfigDir, configDirName)
}

func (c *realConfigPaths) ProjectEnvFile() string {
	return filepath.Join(projectConfigDir, envFileName)
}

func (c *realConfigPaths) ProjectEnvSecretFile() string {
	return filepath.Join(projectConfigDir, envSecretFileName)
}

// ProjectEnvSecretYAMLFile returns the project-level structured secret file.
func (c *realConfigPaths) ProjectEnvSecretYAMLFile() string {
	return filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName)
}

// xdgBaseDir resolves an XDG base directory for the given environment variable,
// falling back to $HOME/<fallback> when the variable is unset or holds a
// relative path (the XDG base directory spec only accepts absolute values). It
// returns the base directory without the application's own subdirectory; each
// caller appends "opencode-sandbox".
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
