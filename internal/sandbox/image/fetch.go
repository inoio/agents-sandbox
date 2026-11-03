package image

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// AgentVersionResolver resolves the agent release to bake when the requested
// version is empty. It is injected through BuildOptions so the image package
// holds no exported test hook and does not import "testing".
type AgentVersionResolver func(ctx context.Context, a agent.Agent, requested string) (string, error)

// DefaultAgentVersionResolver returns the requested version when non-empty,
// otherwise resolves the latest release for the agent via agent.LatestVersion,
// the one resolution shared with the upgrade check. Agents without an
// UpgradeChecker fall back to the requested (or empty) version.
func DefaultAgentVersionResolver(ctx context.Context, a agent.Agent, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	return agent.LatestVersion(ctx, a)
}

// defaultAgentVersionResolver is the fallback used when a caller injects no
// resolver. Production always injects one at the boundary (cmd, prepare); this
// default exists so a direct caller of the image package still resolves, and it
// is what image's own tests override.
var defaultAgentVersionResolver AgentVersionResolver = DefaultAgentVersionResolver

// resolveAgentVersion resolves the version through the injected resolver,
// falling back to the package default when none is set.
func (o BuildOptions) resolveAgentVersion(ctx context.Context, a agent.Agent, requested string) (string, error) {
	resolve := o.ResolveVersion
	if resolve == nil {
		resolve = defaultAgentVersionResolver
	}
	return resolve(ctx, a, requested)
}
