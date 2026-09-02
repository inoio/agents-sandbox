package agent

import "context"

// settingsFileName is the settings filename shared by the pi and claude-code
// agents, whose merged snippet config is written to <config dir>/settings.json.
const settingsFileName = "settings.json"

// DaemonProvider runs a long-lived server that clients attach to (opencode).
type DaemonProvider interface {
	DaemonStartCmd(serveOnly bool) string
	DaemonKillCmd() string
	DaemonHealthCmd() string
	DaemonHealthParse(stdout string) (bool, error)
}

// WorktreeProvider manages git worktrees through a running daemon.
type WorktreeProvider interface {
	WorktreeListCmd() string
	WorktreeCreateCmd(spec WorktreeSpec) string
	WorktreeParseDir(stdout string) (string, bool)
}

// UpgradeChecker can resolve and compare releases.
type UpgradeChecker interface {
	LatestVersion(ctx context.Context) (string, error)
	NewerThan(a, b string) (bool, error)
}

// ConfigMerger exposes data consumed by the shared merge logic in
// internal/configmerge.BuildMerged.
type ConfigMerger interface {
	SnippetPattern() string
	VMConfigPath(home string) string
	// ConfigFileNames returns the config filenames the agent reads from its VM
	// config directory that the merged config supersedes (and which a stale
	// host copy could otherwise shadow it with).
	ConfigFileNames() []string
}

// AttachRunner starts the client TUI/session.
type AttachRunner interface {
	AttachCommand(target string, args []string) string
}

// Provisioner declares host files to copy into the VM by default (drop-in).
type Provisioner interface {
	ProvisionRules() []ProvisionRule
}

// AsDaemonProvider returns the agent's DaemonProvider, if it implements one.
func AsDaemonProvider(a Agent) (DaemonProvider, bool) {
	p, ok := a.(DaemonProvider)
	return p, ok
}

// AsWorktreeProvider returns the agent's WorktreeProvider, if it implements one.
func AsWorktreeProvider(a Agent) (WorktreeProvider, bool) {
	p, ok := a.(WorktreeProvider)
	return p, ok
}

// AsUpgradeChecker returns the agent's UpgradeChecker, if it implements one.
func AsUpgradeChecker(a Agent) (UpgradeChecker, bool) { p, ok := a.(UpgradeChecker); return p, ok }

// AsConfigMerger returns the agent's ConfigMerger, if it implements one.
func AsConfigMerger(a Agent) (ConfigMerger, bool) { p, ok := a.(ConfigMerger); return p, ok }

// AsAttachRunner returns the agent's AttachRunner, if it implements one.
func AsAttachRunner(a Agent) (AttachRunner, bool) { p, ok := a.(AttachRunner); return p, ok }

// AsProvisioner returns the agent's Provisioner, if it implements one.
func AsProvisioner(a Agent) (Provisioner, bool) { p, ok := a.(Provisioner); return p, ok }

// VersionProvider exposes how to read the installed agent version from a
// running image: the shell command to run and a parser for its output. Agents
// print their version in different formats, so parsing is per-agent.
type VersionProvider interface {
	VersionCmd() string
	ParseVersion(stdout string) (string, error)
}

// AsVersionProvider returns the agent's VersionProvider, if it implements one.
func AsVersionProvider(a Agent) (VersionProvider, bool) { p, ok := a.(VersionProvider); return p, ok }
