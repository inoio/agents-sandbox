package options

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// ProjectVMKey identifies a project artifact by project slug and agent. It is
// the seam's own key type so options does not import the state persistence
// package; vm adapts it to state.Key where the lock is taken.
type ProjectVMKey struct {
	Slug  string
	Agent string
}

// Seams carries the run's behavior test seams: the injectable functions the run
// and shell lifecycle stages would otherwise reach through package-level
// variables. A nil field means the consumer uses its production default, so the
// zero value is production and only a test populates a field. They are behavior
// seams, not per-operation clients: production never sets one.
type Seams struct {
	// ProjectVMLock takes the per-(slug, agent) creation lock that serializes a
	// project VM's create and destroy.
	ProjectVMLock func(k ProjectVMKey) (func(), error)
	// DaemonHealth runs the agent serve daemon's health probe command.
	DaemonHealth func(ctx context.Context, sb msb.Sandbox, command string) (string, int, error)
	// AgentLatestVersion resolves an agent's newest release for the upgrade
	// check and the deferred image-build version.
	AgentLatestVersion func(ctx context.Context, a agent.Agent) (string, error)
	// RuntimeHomeMigrationOffer offers the opt-in legacy home-volume copy at
	// session start.
	RuntimeHomeMigrationOffer func(ctx context.Context, client msb.Client, ui termio.UI) error
}
