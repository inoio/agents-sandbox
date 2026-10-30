package configpaths

import (
	"os"
	"path/filepath"
)

type ConfigPaths interface {
	UserConfigDir() string
	UserSlugConfigDir(slug string) string
	UserCacheDir() string
	UserStateDir() string
	UserDataDir() string
	UserHomeDir() (string, error)

	UserEnvFile() string
	UserEnvSecretFile() string
	UserEnvSecretYAMLFile() string

	ProjectConfigDir() string
	ProjectConfigDirAbs() string
	ProjectEnvFile() string
	ProjectEnvSecretFile() string
	ProjectEnvSecretYAMLFile() string
	ProjectDockerfile() string
	IgnoredWorktreeConfigDir() string
}

type realConfigPaths struct{}

// Get is the factory clients can use to get a Client.
// Tests override Get to inject mocks.
var Get = func() ConfigPaths {
	return &realConfigPaths{}
}

// UserConfigDir returns the tool's user-config directory: $XDG_CONFIG_HOME/
// agents-sandbox, defaulting to ~/.config/agents-sandbox.
func (c *realConfigPaths) UserConfigDir() string {
	return filepath.Join(xdgBaseDir("XDG_CONFIG_HOME", ".config"), pathPrefix)
}

// UserSlugConfigDir returns the per-slug user-config directory: the tool's user
// config dir joined with the project slug. It is keyed by the repo slug, so
// every clone of one origin shares it.
func (c *realConfigPaths) UserSlugConfigDir(slug string) string {
	return filepath.Join(c.UserConfigDir(), slug)
}

// UserCacheDir returns the tool's cache directory: $XDG_CACHE_HOME/
// agents-sandbox, defaulting to ~/.cache/agents-sandbox.
func (c *realConfigPaths) UserCacheDir() string {
	return filepath.Join(xdgBaseDir("XDG_CACHE_HOME", ".cache"), pathPrefix)
}

// UserStateDir returns the tool's state directory: $XDG_STATE_HOME/
// agents-sandbox, defaulting to ~/.local/state/agents-sandbox.
func (c *realConfigPaths) UserStateDir() string {
	return filepath.Join(xdgBaseDir("XDG_STATE_HOME", ".local/state"), pathPrefix)
}

// UserDataDir returns the tool's data directory: $XDG_DATA_HOME/
// agents-sandbox, defaulting to ~/.local/share/agents-sandbox.
func (c *realConfigPaths) UserDataDir() string {
	return filepath.Join(xdgBaseDir("XDG_DATA_HOME", ".local/share"), pathPrefix)
}

// MSBHome returns the microsandbox home agents-sandbox owns: a private "msb"
// directory under the tool's data dir, never the user's ~/.microsandbox. It is a
// free function (not a ConfigPaths method) so the low-level sandbox/msb adapter
// can resolve it without importing the higher-level runtime package.
func MSBHome() string {
	return filepath.Join(Get().UserDataDir(), "msb")
}

// UserHomeDir returns the host user's home directory. It is the root the
// home-relative agent config and home manifests are resolved against, exposed
// through this seam so every host-home lookup is mockable like the other paths.
func (c *realConfigPaths) UserHomeDir() (string, error) {
	return os.UserHomeDir()
}

// ProjectConfigDir returns the project-config directory: the relative
// .agents-sandbox resolved against the CWD, or, in a bare checkout whose
// worktrees live inside the bare root, the absolute <bare-root>/.agents-sandbox.
// It never creates the directory.
func (c *realConfigPaths) ProjectConfigDir() string { return c.projectConfigBase() }

// projectConfigBase returns the config dir for this process's CWD. It never
// creates a directory. When os.Getwd fails it degrades to the relative constant,
// as ProjectConfigDirAbs does.
func (c *realConfigPaths) projectConfigBase() string {
	cwd, err := os.Getwd()
	if err != nil { // gomutants:disable BRANCH_IF reason="an unreadable CWD makes resolveProjectConfig of the empty path resolve through the relative dot, which never yields the absolute bare base, so the fall-through returns projectConfigDir too"
		return projectConfigDir
	}
	base, _ := resolveProjectConfig(cwd)
	return base
}

// ProjectConfigDirAbs returns the project-config directory as an absolute path by
// resolving the base against the current directory, so display can name an
// explicit location regardless of where it is read. A bare checkout already
// yields an absolute base. If the current directory cannot be read it degrades to
// the relative base.
func (c *realConfigPaths) ProjectConfigDirAbs() string {
	base := c.projectConfigBase()
	if filepath.IsAbs(base) {
		return base
	}
	cwd, err := os.Getwd()
	if err != nil { // gomutants:disable BRANCH_IF reason="os.Getwd returns an empty dir when it fails, so the fall-through filepath.Join of the empty dir and base is base and the branch cannot change the value"
		return base
	}
	return filepath.Join(cwd, base)
}

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

// Shared names for config subdirectories and files, used by both the
// project-local and the user-level config path helpers.
const (
	pathPrefix = "agents-sandbox"
	// projectConfigDir is the project-local metadata directory for the tool.
	projectConfigDir  = "." + pathPrefix
	EnvFileName       = "env"
	EnvSecretFileName = "env.secret"
	// Derived rather than a literal so gosec G101 does not flag the sensitive name.
	envSecretYAMLFileName = EnvSecretFileName + ".yaml"
	DockerFileName        = "Dockerfile"
)

const (
	envFileName       = EnvFileName
	envSecretFileName = EnvSecretFileName
	dockerfileName    = DockerFileName
)

// ProjectDockerfile returns the project Dockerfile path.
func (c *realConfigPaths) ProjectDockerfile() string {
	return filepath.Join(c.projectConfigBase(), dockerfileName)
}

func (c *realConfigPaths) ProjectEnvFile() string {
	return filepath.Join(c.projectConfigBase(), envFileName)
}

func (c *realConfigPaths) ProjectEnvSecretFile() string {
	return filepath.Join(c.projectConfigBase(), envSecretFileName)
}

// ProjectEnvSecretYAMLFile returns the project-level structured secret file.
func (c *realConfigPaths) ProjectEnvSecretYAMLFile() string {
	return filepath.Join(c.ProjectConfigDir(), envSecretYAMLFileName)
}

// IgnoredWorktreeConfigDir returns the worktree-level .agents-sandbox that a
// bare checkout's base ignores, or "" when none is ignored: everywhere outside a
// bare checkout, and at the bare root itself, where the base's directory is the
// only one. It lets callers warn about config the user must move to the bare root.
func (c *realConfigPaths) IgnoredWorktreeConfigDir() string {
	cwd, err := os.Getwd()
	if err != nil { // gomutants:disable BRANCH_IF reason="an unreadable CWD makes resolveProjectConfig of the empty path resolve through the relative dot, which never reports an ignored worktree copy, so the fall-through returns empty too"
		return ""
	}
	_, ignored := resolveProjectConfig(cwd)
	return ignored
}

// xdgBaseDir resolves an XDG base directory for the given environment variable,
// falling back to $HOME/<fallback> when the variable is unset or holds a
// relative path (the XDG base directory spec only accepts absolute values). It
// returns the base directory without the application's own subdirectory; each
// caller appends "agents-sandbox".
//
// The os package's UserConfigDir/UserCacheDir are deliberately not used: they
// return platform-specific paths (e.g., $HOME/Library/... on macOS) instead of
// honoring XDG on every platform. Cross-platform tools like git and curl read
// the XDG variables unconditionally, so we do the same.
func xdgBaseDir(env, fallback string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}
