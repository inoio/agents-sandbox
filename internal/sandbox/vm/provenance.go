package vm

import (
	"context"
	"strings"

	"github.com/inoio/opencode-sandbox/internal/agent"
	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Provenance files baked into the image by the agent and dind blocks.
const (
	agentSourcePath  = "/etc/opencode-sandbox/agent-source"
	dockerSourcePath = "/etc/opencode-sandbox/docker-source"
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
	state.AgentSource = agentSource
	state.DockerSource = dockerSource

	if agentSource == agentSourceTool {
		recordToolAgentVersion(ctx, a, sb, ui, &state)
	}

	if persistErr := saveUpgradeState(state); persistErr != nil {
		ui.Warnf("could not persist updater state: %v (continuing)", persistErr)
	}
}

// recordToolAgentVersion detects the installed agent version via the agent's
// VersionProvider and records it as the upgrade baseline. Failures are logged,
// never fatal.
func recordToolAgentVersion(ctx context.Context, a agent.Agent, sb msb.Sandbox, ui termio.UI, state *upgradeState) {
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		return
	}
	out, runErr := sb.Shell(ctx, provider.VersionCmd(), msbSdk.WithExecUser(DefaultSandboxUser))
	if runErr != nil {
		ui.Warnf("could not detect agent version: %v (continuing)", runErr)
		return
	}
	version, parseErr := provider.ParseVersion(out.Stdout())
	if parseErr != nil {
		ui.Warnf("could not parse agent version: %v (continuing)", parseErr)
		return
	}
	state.CurrentVersion = version
}
