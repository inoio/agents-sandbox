// Package hostenv resolves the host facts the launcher defaults into a project
// VM's environment: the host time zone, a UTF-8 locale, and the host git commit
// identity. See specs/vm/host-environment-defaults.md.
package hostenv

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
)

// Sources are the host accessors the defaults are resolved from.
type Sources struct {
	Getenv      func(string) string
	Readlink    func(string) (string, error)
	GitIdentity func() (name, email string)
}

// hostGetenv and hostReadlink are the real host accessors behind HostSources;
// they are vars so an internal test can pin Defaults off the host.
var (
	hostGetenv   = os.Getenv
	hostReadlink = os.Readlink
)

// HostSources returns the real host accessors.
func HostSources() Sources {
	return Sources{Getenv: hostGetenv, Readlink: hostReadlink, GitIdentity: hostGitIdentity}
}

// Defaults resolves the host environment defaults from the real host sources
// for the given config scopes.
func Defaults(scopes []configpaths.ConfigScope) map[string]string {
	return Resolve(HostSources(), ProvisionedGitConfig(scopes))
}

// Resolve returns the host-derived environment defaults for a project VM. A
// default is present only when the host fact resolves; the git identity is
// omitted when the user provisions a git config.
func Resolve(s Sources, gitConfigProvisioned bool) map[string]string {
	defaults := make(map[string]string)
	if zone := resolveTimezone(s.Getenv, s.Readlink); zone != "" {
		defaults["TZ"] = zone
	}
	if locale := resolveLocale(s.Getenv); locale != "" {
		defaults["LANG"] = locale
	}
	if !gitConfigProvisioned {
		name, email := s.GitIdentity()
		if name != "" && email != "" {
			defaults["GIT_AUTHOR_NAME"] = name
			defaults["GIT_AUTHOR_EMAIL"] = email
			defaults["GIT_COMMITTER_NAME"] = name
			defaults["GIT_COMMITTER_EMAIL"] = email
		}
	}
	return defaults
}

// Under returns defaults with base laid over it: a key present in base keeps
// its value. Neither input map is modified.
func Under(base, defaults map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(base))
	maps.Copy(merged, defaults)
	maps.Copy(merged, base)
	return merged
}

// ProvisionedGitConfig reports whether any config scope's home: key provisions
// a git global config. A scope whose config file cannot be read is skipped; the
// config pipeline reports a malformed file separately.
func ProvisionedGitConfig(scopes []configpaths.ConfigScope) bool {
	for _, scope := range scopes {
		manifest, ok, err := homeconfig.ReadHomeFromConfigDir(scope.Dir)
		if err != nil || !ok {
			continue
		}
		for target := range manifest {
			switch filepath.Clean(target) {
			case ".gitconfig", ".config/git/config":
				return true
			}
		}
	}
	return false
}

const zoneinfoMarker = "zoneinfo/"

func resolveTimezone(getenv func(string) string, readlink func(string) (string, error)) string {
	if tz := getenv("TZ"); tz != "" {
		return tz
	}
	target, err := readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	_, after, found := strings.CutLast(target, zoneinfoMarker)
	if !found {
		return ""
	}
	return strings.TrimPrefix(after, "/")
}

func resolveLocale(getenv func(string) string) string {
	effective := getenv("LC_ALL")
	if effective == "" {
		effective = getenv("LC_CTYPE")
	}
	if effective == "" {
		effective = getenv("LANG")
	}
	lower := strings.ToLower(effective)
	if strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8") {
		return "C.UTF-8"
	}
	return ""
}

// execCommand is the host git launcher. A var so an internal test injects a
// fake (the notify/opener seam pattern).
var execCommand = exec.Command

func gitConfigValue(key string) string {
	out, err := execCommand("git", "config", "--global", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func hostGitIdentity() (string, string) {
	return gitConfigValue("user.name"), gitConfigValue("user.email")
}
