package vm

import (
	"context"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Provenance files baked into the image by the agent and docker blocks.
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
// boot and writes them into the project's HomeState, detecting and recording
// the installed agent version as the upgrade baseline for a tool-installed
// agent. It is a read-modify-write so the project's fingerprints and offered
// set survive. Failures are logged, never fatal.
func recordImageProvenance(ctx context.Context, k state.Key, a agent.Agent, sb msb.Sandbox, ui termio.UI) {
	agentSource, err := sb.FS().ReadString(ctx, agentSourcePath)
	if err != nil {
		ui.Verbosef("no agent-source file in image (pre-provenance image); skipping")
		return
	}
	agentSource = strings.TrimSpace(agentSource)

	dockerSource, _ := sb.FS().ReadString(ctx, dockerSourcePath)
	dockerSource = strings.TrimSpace(dockerSource)

	detectedVersion, hasVersion := "", false
	if agentSource == agentSourceTool {
		detectedVersion, hasVersion = detectToolAgentVersion(ctx, a, sb, ui)
	}

	if writeErr := state.UpdateHomeState(k, func(hs *state.HomeState) {
		hs.AgentSource = agentSource
		hs.DockerSource = dockerSource
		if hasVersion {
			hs.AgentVersion = detectedVersion
		}
	}); writeErr != nil {
		ui.Warnf("could not persist project state: %v (continuing)", writeErr)
	}
}

// detectToolAgentVersion detects the installed agent version via the agent's
// VersionProvider. The bool reports success; on failure the caller keeps any
// previously recorded version. Failures are logged, never fatal.
func detectToolAgentVersion(ctx context.Context, a agent.Agent, sb msb.Sandbox, ui termio.UI) (string, bool) {
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		return "", false
	}
	out, runErr := sb.Shell(ctx, provider.VersionCmd(), msbSdk.WithExecUser(DefaultSandboxUser))
	if runErr != nil {
		ui.Warnf("could not detect agent version: %v (continuing)", runErr)
		return "", false
	}
	version, parseErr := provider.ParseVersion(out.Stdout())
	if parseErr != nil {
		ui.Warnf("could not parse agent version: %v (continuing)", parseErr)
		return "", false
	}
	return version, true
}
