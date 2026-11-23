package firstrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

// claudeCodeAgent returns the registered claude-code profile.
func claudeCodeAgent(t *testing.T) agent.Agent {
	t.Helper()
	a, ok := agent.Lookup("claude-code")
	require.True(t, ok, "claude-code is registered")
	return a
}

// claudeCodeSeedFacts are the seed facts for a claude-code host; the seed
// plans its instruction files from the host itself.
func claudeCodeSeedFacts() Facts {
	return Facts{Agent: "claude-code", Slug: "widget-abc"}
}

// managedClaudeDir is the per-slug managed claude-code config dir (its launcher
// config-dir name is "claude"), the root the config-dir mirror copies into the
// VM's ~/.claude.
func managedClaudeDir(paths configpaths.ConfigPaths) string {
	return filepath.Join(paths.UserSlugConfigDir("widget-abc"), "claude")
}

// symlinkHostPath creates the symlink rel under root pointing at target.
func symlinkHostPath(t *testing.T, target, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(target, path))
}

func TestHostInstructionFilesSelectsMarkdownInTheAllowlist(t *testing.T) {
	hostHome := t.TempDir()
	for _, rel := range []string{
		".claude/CLAUDE.md",
		".claude/commands/review.md",
		".claude/agents/planner.md",
		".claude/skills/deploy/SKILL.md",
		".claude/skills/deploy/notes/LIMITS.MD",
	} {
		writeHostFile(t, hostHome, rel, "# instructions\n")
	}

	got := HostInstructionFiles(claudeCodeAgent(t), hostHome)

	require.Equal(t, []string{
		filepath.Join(hostHome, ".claude/CLAUDE.md"),
		filepath.Join(hostHome, ".claude/agents/planner.md"),
		filepath.Join(hostHome, ".claude/commands/review.md"),
		filepath.Join(hostHome, ".claude/skills/deploy/SKILL.md"),
		filepath.Join(hostHome, ".claude/skills/deploy/notes/LIMITS.MD"),
	}, got, "every Markdown file in the allowlist, sorted")
}

func TestHostInstructionFilesNeverSelectsATree(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/commands/review.md", "# review\n")
	writeHostFile(t, hostHome, ".claude/commands/frontend/lint.md", "# lint\n")
	writeHostFile(t, hostHome, ".claude/commands/frontend/lint.sh", "#!/bin/sh\n")
	writeHostFile(t, hostHome, ".claude/skills/docs/SKILL.md", "# docs\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")
	for _, rel := range []string{
		".claude/settings.json",                               // config, not an instruction file
		".claude/notes.md",                                    // Markdown outside the allowlist
		".claude/projects/widget/memory.md",                   // a tree outside the allowlist
		".claude/commands/package-lock.json",                  // a lockfile beside a command
		".claude/skills/deploy/run.sh",                        // a script: the whole skill is skipped
		".claude/skills/docs/node_modules/left-pad/index.js",  // dependencies are pruned
		".claude/skills/docs/.git/HEAD",                       // a nested git dir is pruned
		".claude/skills/.git/description.md",                  // a checkout's git dir is pruned
		".claude/skills/stray.txt",                            // a stray non-Markdown file
		".claude/skills/docs/node_modules/left-pad/README.md", // never descended into
	} {
		writeHostFile(t, hostHome, rel, "not seeded\n")
	}

	got := HostInstructionFiles(claudeCodeAgent(t), hostHome)

	require.Equal(t, []string{
		filepath.Join(hostHome, ".claude/commands/frontend/lint.md"),
		filepath.Join(hostHome, ".claude/commands/review.md"),
		filepath.Join(hostHome, ".claude/skills/docs/SKILL.md"),
	}, got, "only a skills tree is taken in whole directories; a command beside a script is still a file")
}

func TestHostInstructionFilesFollowsSymlinksInsideTheHome(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, "dotfiles/claude/CLAUDE.md", "# rules\n")
	writeHostFile(t, hostHome, "dotfiles/claude/skills/deploy/SKILL.md", "# deploy\n")
	writeHostFile(t, hostHome, "dotfiles/shared/review.md", "# review\n")
	symlinkHostPath(t, filepath.Join(hostHome, "dotfiles/claude/CLAUDE.md"), hostHome, ".claude/CLAUDE.md")
	symlinkHostPath(t, filepath.Join(hostHome, "dotfiles/claude/skills"), hostHome, ".claude/skills")
	symlinkHostPath(t, "../../dotfiles/shared/review.md", hostHome, ".claude/commands/review.md")
	symlinkHostPath(t, filepath.Join(hostHome, "dotfiles/claude/skills"), hostHome,
		"dotfiles/claude/skills/deploy/loop")

	got := HostInstructionFiles(claudeCodeAgent(t), hostHome)

	require.Equal(t, []string{
		filepath.Join(hostHome, ".claude/CLAUDE.md"),
		filepath.Join(hostHome, ".claude/commands/review.md"),
		filepath.Join(hostHome, ".claude/skills/deploy/SKILL.md"),
	}, got, "the files are named by their path under the allowlist entry, and the link cycle is walked once")
}

func TestHostInstructionFilesSkipsSymlinksLeavingTheHome(t *testing.T) {
	hostHome := t.TempDir()
	outside := t.TempDir()
	writeHostFile(t, outside, "shared.md", "# shared\n")
	writeHostFile(t, outside, "tree/SKILL.md", "# linked tree\n")
	writeHostFile(t, hostHome, ".claude/skills/own/SKILL.md", "# own\n")
	symlinkHostPath(t, filepath.Join(outside, "shared.md"), hostHome, ".claude/CLAUDE.md")
	symlinkHostPath(t, filepath.Join(outside, "tree"), hostHome, ".claude/skills/linked")
	symlinkHostPath(t, filepath.Join(outside, "tree"), hostHome, ".claude/commands")
	symlinkHostPath(t, hostHome, hostHome, ".claude/agents")
	symlinkHostPath(t, filepath.Join(hostHome, "missing.md"), hostHome, ".claude/skills/own/broken.md")

	got := HostInstructionFiles(claudeCodeAgent(t), hostHome)

	require.Empty(t, got, "a link out of the home, to the home itself, or nowhere is never selected, and "+
		"the skill holding the broken link is not whole")
}

func TestHostInstructionFilesEmptyWithoutTheCapability(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".hermes/AGENTS.md", "# hermes\n")
	hermes, ok := agent.Lookup("hermes")
	require.True(t, ok)

	require.Empty(t, HostInstructionFiles(hermes, hostHome), "hermes declares no instruction paths")
	require.Empty(t, HostInstructionFiles(plainProfile{}, hostHome), "a profile without a merger has no mirror root")
}

func TestHostInstructionFilesEmptyForAnUnresolvableHome(t *testing.T) {
	missingHome := filepath.Join(t.TempDir(), "missing")

	require.Empty(t, HostInstructionFiles(claudeCodeAgent(t), missingHome))
}

// seederWithoutMerger declares instruction paths but no ConfigMerger, so it has
// no mirror root.
type seederWithoutMerger struct{ plainProfile }

func (seederWithoutMerger) InstructionPaths() []string { return []string{"AGENTS.md"} }

func TestHostInstructionFilesNeedAMirrorRoot(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, "AGENTS.md", "# rules\n")

	require.Empty(t, HostInstructionFiles(seederWithoutMerger{}, hostHome))
	require.Empty(t, instructionFileNames(seederWithoutMerger{}))
}

func TestSeedInstructionsReportsAFailedWriteAndContinues(t *testing.T) {
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/CLAUDE.md", "# rules\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/README.md", "# readme\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")
	targetDir := t.TempDir()
	writeHostFile(t, targetDir, "CLAUDE.md/blocker", "a directory where the file would go\n")
	writeHostFile(t, targetDir, "skills/deploy/README.md/blocker", "a directory where the file would go\n")

	outcome := seedInstructions(claudeCodeAgent(t), hostHome, targetDir)

	require.Equal(t, []string{
		filepath.Join(hostHome, ".claude/CLAUDE.md"),
		filepath.Join(hostHome, ".claude/skills/deploy/README.md"),
	}, outcome.unseedable)
	require.Equal(t, 1, outcome.copied)
	require.FileExists(t, filepath.Join(targetDir, "skills/deploy/SKILL.md"),
		"a failed write is reported and the rest is still written")
}

func TestSeedFromHostCopiesInstructionFilesToTheMirrorRoot(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/CLAUDE.md", "# rules\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, SeedResult{Secrets: 0, InstructionFiles: 2, InstructionDir: managedClaudeDir(paths)}, result)
	rules, err := os.ReadFile(filepath.Join(managedClaudeDir(paths), "CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "# rules\n", string(rules))
	skill, err := os.ReadFile(filepath.Join(managedClaudeDir(paths), "skills/deploy/SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, "# deploy\n", string(skill))
	info, err := os.Stat(filepath.Join(managedClaudeDir(paths), "skills/deploy/SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestSeedFromHostReadsALinkedFileUnderItsAllowlistPath(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, "dotfiles/CLAUDE.md", "# from dotfiles\n")
	symlinkHostPath(t, filepath.Join(hostHome, "dotfiles/CLAUDE.md"), hostHome, ".claude/CLAUDE.md")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.NoError(t, err)
	require.Equal(t, 1, result.InstructionFiles)
	rules, err := os.ReadFile(filepath.Join(managedClaudeDir(paths), "CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "# from dotfiles\n", string(rules), "the link's target is copied as a regular file")
}

func TestSeedFromHostReportsSymlinksLeavingTheHome(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	outside := t.TempDir()
	writeHostFile(t, outside, "shared.md", "# shared\n")
	symlinkHostPath(t, filepath.Join(outside, "shared.md"), hostHome, ".claude/CLAUDE.md")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.Error(t, err)
	require.Contains(t, err.Error(), "skipped 1 symlink(s) that resolve outside your home: "+
		filepath.Join(hostHome, ".claude/CLAUDE.md"))
	require.Zero(t, result.InstructionFiles)
	require.NoDirExists(t, managedClaudeDir(paths))
}

func TestSeedFromHostRefusesAnInstructionFileWithASecret(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/CLAUDE.md", "# rules\n\nexport OPENAI_API_KEY=abc123def456ghi\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")
	writeHostFile(t, hostHome, ".claude/skills/release/SKILL.md", "# release\n")
	writeHostFile(t, hostHome, ".claude/skills/release/notes.md", "TOKEN=deadbeef1234\n")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.Error(t, err)
	require.Contains(t, err.Error(), "skipped 2 instruction file(s) that look like they hold a secret: "+
		filepath.Join(hostHome, ".claude/CLAUDE.md")+", "+filepath.Join(hostHome, ".claude/skills/release/notes.md"))
	require.NotContains(t, err.Error(), "abc123def456ghi", "the error never carries the value")
	require.Equal(t, 1, result.InstructionFiles)
	require.NoFileExists(t, filepath.Join(managedClaudeDir(paths), "CLAUDE.md"), "a refused file is never written")
	require.NoDirExists(t, filepath.Join(managedClaudeDir(paths), "skills/release"),
		"a skill with a refused file is skipped whole")
	require.FileExists(t, filepath.Join(managedClaudeDir(paths), "skills/deploy/SKILL.md"),
		"the clean files are still seeded")
}

func TestSeedFromHostSkipsASkillWithNonMarkdownFilesWhole(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/skills/deploy/SKILL.md", "# deploy\n")
	writeHostFile(t, hostHome, ".claude/skills/deploy/scripts/run.sh", "#!/bin/sh\n")
	writeHostFile(t, hostHome, ".claude/skills/docs/SKILL.md", "# docs\n")
	writeHostFile(t, hostHome, ".claude/skills/README.txt", "a stray file, not a skill\n")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.Error(t, err)
	require.Equal(t,
		"firstrun: skipped 1 skill(s) with non-Markdown files; use the drop-in or copy them yourself: skills/deploy",
		err.Error(), "a stray file in the skills tree is not a skill")
	require.Equal(t, 1, result.InstructionFiles)
	require.NoDirExists(t, filepath.Join(managedClaudeDir(paths), "skills/deploy"))
	require.FileExists(t, filepath.Join(managedClaudeDir(paths), "skills/docs/SKILL.md"))
}

func TestSeedFromHostSkipsASkillWithAnUnreadableFileWhole(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	writeHostFile(t, hostHome, ".claude/skills/big/SKILL.md", "# big\n")
	writeHostFile(t, hostHome, ".claude/skills/big/reference.md", strings.Repeat("x", readback.MaxHashedBytes+1))

	_, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	require.Error(t, err)
	require.Contains(t, err.Error(), "skipped 1 host file(s) that could not be seeded: "+
		filepath.Join(hostHome, ".claude/skills/big/reference.md"))
	require.NoDirExists(t, managedClaudeDir(paths), "nothing of the skill is written")
}

func TestSeedFromHostAdmitsWholeSkillsUnderTheFileCap(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	for i := range maxSeedInstructionFiles - 1 {
		writeHostFile(t, hostHome, fmt.Sprintf(".claude/commands/c%03d.md", i), "# command\n")
	}
	writeHostFile(t, hostHome, ".claude/skills/a/SKILL.md", "# a\n")
	writeHostFile(t, hostHome, ".claude/skills/a/more.md", "# a, more\n")
	writeHostFile(t, hostHome, ".claude/skills/b/SKILL.md", "# b\n")

	result, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	assertOverCap(t, err, 2)
	require.Equal(t, maxSeedInstructionFiles, result.InstructionFiles, "the cap admits a file exactly at it")
	require.NoDirExists(t, filepath.Join(managedClaudeDir(paths), "skills/a"), "a skill is never cut in half")
	require.FileExists(t, filepath.Join(managedClaudeDir(paths), "skills/b/SKILL.md"),
		"a later skill that still fits is seeded")
}

// TestSeedFromHostCapsTheInstructionBytes fills the byte cap with max-size files
// whose first is three bytes short: a skill of two 2-byte files after them is
// over the cap and skipped whole, and the 3-byte file after it still fits
// exactly at the cap, so the cap counts a unit's bytes, is inclusive, and a
// skip does not end the copy.
func TestSeedFromHostCapsTheInstructionBytes(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	paths := configpaths.Get()
	hostHome := t.TempDir()
	const fileBytes = readback.MaxHashedBytes
	require.Zero(t, maxSeedInstructionBytes%fileBytes, "the cap is a whole number of max-size files")
	for i := range maxSeedInstructionBytes / fileBytes {
		size := fileBytes
		if i == 0 {
			size -= 3
		}
		writeHostFile(t, hostHome, fmt.Sprintf(".claude/skills/s%02d.md", i), markdownOfSize(size))
	}
	writeHostFile(t, hostHome, ".claude/skills/t/SKILL.md", "x\n")
	writeHostFile(t, hostHome, ".claude/skills/t/more.md", "x\n")
	writeHostFile(t, hostHome, ".claude/skills/u.md", "xx\n")

	_, err := SeedFromHost(claudeCodeSeedFacts(), paths, hostHome)

	assertOverCap(t, err, 2)
	require.NoDirExists(t, filepath.Join(managedClaudeDir(paths), "skills/t"))
	require.FileExists(t, filepath.Join(managedClaudeDir(paths), "skills/u.md"),
		"a later file that fits exactly at the byte cap is seeded")
}

// assertOverCap asserts that err reports exactly wantSkipped instruction files
// over the seed cap.
func assertOverCap(t *testing.T, err error, wantSkipped int) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf(
		"skipped %d instruction file(s) over the seed cap of %d files and %d bytes",
		wantSkipped, maxSeedInstructionFiles, maxSeedInstructionBytes))
}

// markdownOfSize returns size bytes of short lines, which the secret scan
// reads quickly.
func markdownOfSize(size int) string {
	return strings.Repeat("xxxxxxx\n", size/8+1)[:size]
}

func TestSeedCapsAreTheDocumentedLimits(t *testing.T) {
	require.Equal(t, 200, maxSeedInstructionFiles)
	require.Equal(t, 2*1024*1024, maxSeedInstructionBytes)
}
