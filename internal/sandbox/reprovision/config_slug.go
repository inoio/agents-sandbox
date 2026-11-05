package reprovision

import (
	"os"

	"github.com/inoio/agents-sandbox/internal/agent"
	cp "github.com/inoio/agents-sandbox/internal/configpaths"
)

// SlugHasUserInputs reports whether the per-slug user config dir for slug holds
// any per-slug user input the launcher consumes: a launcher config file (found
// with the same discovery NewResolver merges from), an env/secret file, or a
// registered agent's config dir. A secrets-only dir counts, so a bare root is
// not routed to its legacy slug just because its per-slug dir has no config
// file yet (#578). An empty slug has no per-slug scope, so it reports false.
func SlugHasUserInputs(slug string) bool {
	for _, scope := range cp.ConfigScopes(cp.Get(), slug) {
		if scope.Kind != cp.ScopePerSlugUser {
			continue
		}
		if _, _, present := cp.FindConfigFile(scope.Dir); present {
			return true
		}
		if pathExists(scope.EnvFile()) || pathExists(scope.EnvSecretFile()) || pathExists(scope.EnvSecretYAMLFile()) {
			return true
		}
		for _, name := range agent.Names() {
			a, _ := agent.Lookup(name)
			// The built-in registry validates its own config-dir names, so an
			// invalid name simply contributes no candidate; there is no error
			// branch to handle here.
			dirNames, _ := agent.ConfigDirNames(a)
			for _, dirName := range dirNames {
				if IsDir(scope.AgentConfigDir(dirName)) {
					return true
				}
			}
		}
	}
	return false
}

// EffectiveConfigSlug returns the slug whose per-slug user config directory the
// launcher should read. It is the repo slug — shared by every clone of a remote —
// except for a bare root whose legacy config directory has not been migrated:
// its legacy config lives under a path-derived legacy slug, which adoption
// renames onto the repo slug. Adoption runs after the resolver is built, so the
// callers that read per-slug config recompute the slug through this after it.
// The fallback is read-only, so a dry run stays side-effect-free.
func EffectiveConfigSlug(repoSlug string, legacySlugs []string) string {
	if SlugHasUserInputs(repoSlug) {
		return repoSlug
	}
	for _, legacy := range legacySlugs {
		if SlugHasUserInputs(legacy) {
			return legacy
		}
	}
	return repoSlug
}

// pathExists reports whether path exists; any stat error (including a missing
// file) reports false.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
