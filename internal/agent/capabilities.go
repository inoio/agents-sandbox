package agent

import "context"

// DaemonProvider runs a long-lived server that clients attach to (opencode).
type DaemonProvider interface {
	DaemonStartCmd(serveOnly bool) string
	DaemonKillCmd() string
	DaemonHealthCmd() string
	DaemonHealthParse(stdout string) (bool, error)
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
// internal/opencodeconfig.BuildMerged.
type ConfigMerger interface {
	SnippetPattern() string
	VMConfigPath(home string) string
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

// AsUpgradeChecker returns the agent's UpgradeChecker, if it implements one.
func AsUpgradeChecker(a Agent) (UpgradeChecker, bool) { p, ok := a.(UpgradeChecker); return p, ok }

// AsConfigMerger returns the agent's ConfigMerger, if it implements one.
func AsConfigMerger(a Agent) (ConfigMerger, bool) { p, ok := a.(ConfigMerger); return p, ok }

// AsAttachRunner returns the agent's AttachRunner, if it implements one.
func AsAttachRunner(a Agent) (AttachRunner, bool) { p, ok := a.(AttachRunner); return p, ok }

// AsProvisioner returns the agent's Provisioner, if it implements one.
func AsProvisioner(a Agent) (Provisioner, bool) { p, ok := a.(Provisioner); return p, ok }
