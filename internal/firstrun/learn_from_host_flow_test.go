package firstrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configmerge"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
)

// TestLearnFromHostSeedsClaudeCodeInstructionFiles drives the UC4 flow for a
// claude-code host whose settings.json is withheld and whose only setup is
// CLAUDE.md and a skill: BuildFacts offers the host seed, SeedFromHost writes
// the files to the per-slug mirror root, and the recorded decision stays quiet
// on a later run even after CLAUDE.md changes, because instruction files are not
// part of the source hash.
func TestLearnFromHostSeedsClaudeCodeInstructionFiles(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/settings.json", `{"env":{"ANTHROPIC_API_KEY":"sk-ant-api03-abcdefghijkl"}}`)
	writeHostFile(t, hostHome, ".claude/CLAUDE.md", "# rules\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")

	facts, err := BuildFacts("claude-code", "widget-abc", hostHome, paths)
	require.NoError(t, err)
	require.Empty(t, facts.HostFiles, "settings.json is withheld")
	require.Empty(t, facts.HostSourceHash, "instruction files are not hashed")

	ui := &termiotest.Mock{IsInteractiveResult: true}
	var offeredKeys []string
	ui.SelectFn = func(_ string, choices []termio.Choice, _ string) (string, error) {
		for _, choice := range choices {
			offeredKeys = append(offeredKeys, choice.Key)
		}
		return string(ChoiceLearnFromHost), nil
	}
	outcome, offered := Prompt(ui, facts)
	require.True(t, offered)
	require.Contains(t, offeredKeys, string(ChoiceLearnFromHost))

	migrated, err := SeedFromHost(facts, paths, hostHome)
	require.NoError(t, err)
	require.Zero(t, migrated.Secrets)
	mirrorRoot := filepath.Join(paths.UserSlugConfigDir("widget-abc"), "claude")
	require.FileExists(t, filepath.Join(mirrorRoot, "CLAUDE.md"))
	require.FileExists(t, filepath.Join(mirrorRoot, "skills", "deploy", "SKILL.md"))
	require.NoFileExists(t, filepath.Join(mirrorRoot, "settings.json"), "the withheld settings are never seeded")
	require.Equal(t, []string{"/home/dev/.claude/CLAUDE.md", "/home/dev/.claude/skills/deploy/SKILL.md"},
		claudeCodeMirrorDestinations(t, mirrorRoot), "the config-dir mirror carries both files into the VM's ~/.claude")
	var opts options.RunOptions
	require.NoError(t, outcome.Apply(&opts, "widget-abc", "claude-code"))

	writeHostFile(t, hostHome, ".claude/CLAUDE.md", "# rules, edited\n")
	later, err := BuildFacts("claude-code", "widget-abc", hostHome, paths)
	require.NoError(t, err)
	require.False(t, later.ManagedConfig, "the seeded instruction files are not a managed config layer")
	require.Equal(t, facts.HostSourceHash, later.HostSourceHash, "editing CLAUDE.md leaves the source hash")
	require.Equal(t, state.FirstRunLearned, later.Prior.Choice)
	require.False(t, Offer(later), "an edited CLAUDE.md does not re-arm the decision")

	seededRules, err := os.ReadFile(filepath.Join(mirrorRoot, "CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "# rules\n", string(seededRules), "the seed is a one-time copy")
}

// claudeCodeMirrorDestinations returns the VM paths the launcher's config-dir
// mirror provisions from the managed dir, for a VM home of /home/dev.
func claudeCodeMirrorDestinations(t *testing.T, managedDir string) []string {
	t.Helper()
	merger, ok := agent.AsConfigMerger(claudeCodeAgent(t))
	require.True(t, ok)
	entries := configmerge.ScanMirror(
		merger.SnippetPattern(),
		merger.ConfigFileNames(),
		[]string{managedDir},
		filepath.Dir(merger.VMConfigPath("/home/dev")),
	)
	destinations := make([]string, len(entries))
	for i, entry := range entries {
		destinations[i] = entry.VMPath
	}
	return destinations
}
