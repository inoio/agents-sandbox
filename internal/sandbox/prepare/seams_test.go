package prepare

import (
	"context"
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestAgentLatestVersionForSeamResolution pins the seam contract: a nil
// options.Seams.AgentLatestVersion uses the production default
// (agentLatestVersion), while a set field is used as-is.
func TestAgentLatestVersionForSeamResolution(t *testing.T) {
	orig := agentLatestVersion
	t.Cleanup(func() { agentLatestVersion = orig })
	agentLatestVersion = func(context.Context, agent.Agent) (string, error) { return "default", nil }

	if got, _ := agentLatestVersionFor(options.Seams{})(context.Background(), opencodeAgent(t)); got != "default" {
		t.Errorf("nil seam version = %q, want the production default %q", got, "default")
	}

	seam := func(context.Context, agent.Agent) (string, error) { return "seam", nil }
	if got, _ := agentLatestVersionFor(options.Seams{AgentLatestVersion: seam})(
		context.Background(), opencodeAgent(t),
	); got != "seam" {
		t.Errorf("set seam version = %q, want %q", got, "seam")
	}
}

// TestRuntimeHomeMigrationOfferSeamResolution pins the seam contract: a nil
// options.Seams.RuntimeHomeMigrationOffer uses the production default
// (offerRuntimeHomeMigration), while a set field is used as-is.
func TestRuntimeHomeMigrationOfferSeamResolution(t *testing.T) {
	orig := offerRuntimeHomeMigration
	t.Cleanup(func() { offerRuntimeHomeMigration = orig })

	defaultErr := errors.New("production default offer")
	offerRuntimeHomeMigration = func(context.Context, msb.Client, termio.UI) error { return defaultErr }
	if err := runtimeHomeMigrationOffer(options.Seams{})(
		context.Background(), nil, &termio.Mock{},
	); !errors.Is(err, defaultErr) {
		t.Errorf("nil seam offer error = %v, want the production default %v", err, defaultErr)
	}

	seamErr := errors.New("injected offer")
	seam := func(context.Context, msb.Client, termio.UI) error { return seamErr }
	if err := runtimeHomeMigrationOffer(options.Seams{RuntimeHomeMigrationOffer: seam})(
		context.Background(), nil, &termio.Mock{},
	); !errors.Is(err, seamErr) {
		t.Errorf("set seam offer error = %v, want %v", err, seamErr)
	}
}

// TestAgentVersionResolverForSeamResolution pins the resolver contract: a nil
// seam uses the shared buildVersionResolver default, a set seam resolves the
// latest version through it (unless a version is requested), and a requested
// version always wins.
func TestAgentVersionResolverForSeamResolution(t *testing.T) {
	orig := buildVersionResolver
	t.Cleanup(func() { buildVersionResolver = orig })
	buildVersionResolver = func(context.Context, agent.Agent, string) (string, error) { return "default", nil }

	if got, _ := agentVersionResolverFor(options.Seams{})(
		context.Background(), opencodeAgent(t), "",
	); got != "default" {
		t.Errorf("nil seam resolver = %q, want the default %q", got, "default")
	}

	seam := options.Seams{AgentLatestVersion: func(context.Context, agent.Agent) (string, error) {
		return "seam", nil
	}}
	if got, _ := agentVersionResolverFor(seam)(context.Background(), opencodeAgent(t), ""); got != "seam" {
		t.Errorf("set seam resolver = %q, want %q", got, "seam")
	}
	if got, _ := agentVersionResolverFor(seam)(context.Background(), opencodeAgent(t), "pinned"); got != "pinned" {
		t.Errorf("requested version resolver = %q, want the pin %q", got, "pinned")
	}
}
