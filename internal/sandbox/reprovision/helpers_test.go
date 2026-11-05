package reprovision

import (
	"path/filepath"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
)

// configBases serves fixed user and project config bases, so a test can place
// the agent's config-dir candidates where os.Stat fails.
type configBases struct {
	configpaths.ConfigPaths

	user, project string
}

func (c configBases) UserConfigDir() string { return c.user }

func (c configBases) UserSlugConfigDir(slug string) string { return filepath.Join(c.user, slug) }

func (c configBases) ProjectConfigDir() string { return c.project }

func (c configBases) ProjectConfigDirAbs() string { return c.project }

// resolveConfigSource resolves an agent's source against the installed seam's
// scopes with an empty slug, preserving the pre-per-slug test behavior.
func resolveConfigSource(a agent.Agent) (ConfigSource, error) {
	return ResolveConfigSource(a, configpaths.ConfigScopes(configpaths.Get(), ""))
}

// invalidConfigDirAgent is a profile whose config-dir name is not a single
// clean path element, exercising the accessors' error path through the
// production resolution boundary.
type invalidConfigDirAgent struct{}

func (invalidConfigDirAgent) Name() string { return "bad-profile" }

func (invalidConfigDirAgent) Experimental() bool { return false }

func (invalidConfigDirAgent) ConfigDirName() string { return "../escape" }

func (invalidConfigDirAgent) ImageSpec() agent.ImageSpec { return agent.ImageSpec{} }

// opencodeTestAgent returns the default opencode profile for LoadConfigFiles
// tests that do not care which agent is active.
func opencodeTestAgent() agent.Agent {
	a, _ := agent.Lookup("")
	return a
}

// opencodeMergedPath is where production writes the merged opencode config
// (ConfigFiles.MergedPath) under VMHomeDir.
const opencodeMergedPath = "/home/dev/.config/opencode/opencode.jsonc"

// plainAgent implements only the base Agent, so it is neither a Provisioner
// nor a ConfigMerger; LoadConfigFilesForHost must produce no merged config.
type plainAgent struct{}

func (plainAgent) Name() string { return "plain" }

func (plainAgent) Experimental() bool { return false }

func (plainAgent) ConfigDirName() string { return "plain" }

func (plainAgent) ImageSpec() agent.ImageSpec {
	return agent.ImageSpec{InstallCommand: "true"}
}

// badProvisionerAgent implements Provisioner with malformed rules so the
// validate-warning branch is exercised.
type badProvisionerAgent struct {
	plainAgent
}

func (badProvisionerAgent) ProvisionRules() []agent.ProvisionRule {
	return []agent.ProvisionRule{
		{Dir: ".config/tool", Patterns: []string{"", "**", "!"}},
	}
}

// relMergedConfigAgent is a ConfigMerger whose merged-config path is always
// relative, so deriving the reserved home target against an absolute vmHome
// fails in filepath.Rel.
type relMergedConfigAgent struct {
	plainAgent
}

func (relMergedConfigAgent) SnippetPattern() string { return "nomatch*.json" }

func (relMergedConfigAgent) VMConfigPath(string) string {
	return "relative/merged.jsonc"
}

func (relMergedConfigAgent) ConfigFileNames() []string { return []string{"merged.jsonc"} }
