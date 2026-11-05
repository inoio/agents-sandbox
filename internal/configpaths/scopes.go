package configpaths

import "path/filepath"

// ScopeKind classifies a config scope so callers can tell the generic user
// scope from the per-slug user scope and the project scope without parsing
// paths.
type ScopeKind int

const (
	// ScopeGenericUser is the plain user config dir shared by every project.
	ScopeGenericUser ScopeKind = iota
	// ScopePerSlugUser is the per-slug user config dir, keyed by the repo slug.
	ScopePerSlugUser
	// ScopeProject is the project-local config dir.
	ScopeProject
)

// ConfigScope is one config authority in the ordered scope chain. It holds only
// its kind and its base directory — the project scope's base may be relative to
// the CWD — and the file joins are its only behaviors.
type ConfigScope struct {
	Kind ScopeKind
	Dir  string
}

// ConfigScopes returns the ordered config-scope chain for slug: the generic
// user scope, the per-slug user scope (omitted for an empty slug, whose merged
// entries the caller must not place anywhere), then the project scope. The
// order is the merge precedence every config reader follows: later scopes
// override earlier ones.
func ConfigScopes(paths ConfigPaths, slug string) []ConfigScope {
	scopes := []ConfigScope{{Kind: ScopeGenericUser, Dir: paths.UserConfigDir()}}
	if slug != "" {
		scopes = append(scopes, ConfigScope{Kind: ScopePerSlugUser, Dir: paths.UserSlugConfigDir(slug)})
	}
	scopes = append(scopes, ConfigScope{Kind: ScopeProject, Dir: paths.ProjectConfigDir()})
	return scopes
}

// ScopeDirs maps scopes to their directories, preserving order.
func ScopeDirs(scopes []ConfigScope) []string {
	dirs := make([]string, len(scopes))
	for i, scope := range scopes {
		dirs[i] = scope.Dir
	}
	return dirs
}

// EnvFile returns the environment definitions file inside the scope.
func (s ConfigScope) EnvFile() string {
	return filepath.Join(s.Dir, EnvFileName)
}

// EnvSecretFile returns the secret environment definitions file inside the scope.
func (s ConfigScope) EnvSecretFile() string {
	return filepath.Join(s.Dir, EnvSecretFileName)
}

// EnvSecretYAMLFile returns the structured secret file inside the scope.
func (s ConfigScope) EnvSecretYAMLFile() string {
	return filepath.Join(s.Dir, envSecretYAMLFileName)
}

// AgentConfigDir returns the agent's config directory inside the scope.
func (s ConfigScope) AgentConfigDir(name string) string {
	return filepath.Join(s.Dir, name)
}
