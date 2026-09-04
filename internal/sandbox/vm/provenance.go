package vm

import (
	"context"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Provenance files baked into the image by the agent and dind blocks.
const (
	agentSourcePath  = "/etc/agents-sandbox/agent-source"
	dockerSourcePath = "/etc/agents-sandbox/docker-source"
)

// Agent-source provenance values baked into the image.
const (
	agentSourceTool = "tool"
	agentSourceUser = "user"
)

// recordImageProvenance reads the image's install-provenance files on first
// boot and, for a tool-installed agent, detects and records its version as the
// upgrade baseline. Failures are logged, never fatal.
func recordImageProvenance(ctx context.Context, a agent.Agent, sb msb.Sandbox, ui termio.UI) {
	agentSource, err := sb.FS().ReadString(ctx, agentSourcePath)
	if err != nil {
		ui.Verbosef("no agent-source file in image (pre-provenance image); skipping")
		return
	}
	agentSource = strings.TrimSpace(agentSource)

	dockerSource, _ := sb.FS().ReadString(ctx, dockerSourcePath)
	dockerSource = strings.TrimSpace(dockerSource)

	state, err := loadUpgradeState()
	if err != nil {
		ui.Warnf("could not read updater state: %v (continuing)", err)
		return
	}
	if state.Agents == nil {
		state.Agents = map[string]agentUpgradeState{}
	}
	entry := state.Agents[a.Name()]
	entry.AgentSource = agentSource
	entry.DockerSource = dockerSource
	if agentSource == agentSourceTool {
		// recordToolAgentVersion writes entry.CurrentVersion
		entry = recordToolAgentVersion(ctx, a, sb, ui, entry)
	}
	state.Agents[a.Name()] = entry

	if persistErr := saveUpgradeState(state); persistErr != nil {
		ui.Warnf("could not persist updater state: %v (continuing)", persistErr)
	}
}

// recordToolAgentVersion detects the installed agent version via the agent's
// VersionProvider and records it as the upgrade baseline. Failures are logged,
// never fatal.
func recordToolAgentVersion(
	ctx context.Context,
	a agent.Agent,
	sb msb.Sandbox,
	ui termio.UI,
	entry agentUpgradeState,
) agentUpgradeState {
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		return entry
	}
	out, runErr := sb.Shell(ctx, provider.VersionCmd(), msbSdk.WithExecUser(DefaultSandboxUser))
	if runErr != nil {
		ui.Warnf("could not detect agent version: %v (continuing)", runErr)
		return entry
	}
	version, parseErr := provider.ParseVersion(out.Stdout())
	if parseErr != nil {
		ui.Warnf("could not parse agent version: %v (continuing)", parseErr)
		return entry
	}
	entry.CurrentVersion = version
	return entry
}
