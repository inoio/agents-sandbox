package vm

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// Preview describes what a run/shell would do, without doing it: the agent
// version, the image build decision, and the home volume a real run would use.
type Preview struct {
	AgentVersion          string
	ResolveVersionAtBuild bool
	// UpgradeCheckSkipped is true when a concrete agent version is known
	// (recovered from the image label or the recorded cache) but the real run's
	// upgrade check did not run, so the reported version is the known one and
	// not a possible upgrade. False for an explicit pin (never upgraded), for a
	// user-provided agent (never upgrade-checked), and when resolution is
	// deferred to build time.
	UpgradeCheckSkipped bool
	ImageTag            string
	BuildImage          bool
	BuildReasons        []string
	HomeVolume          string
	CreateHome          bool
}

// PreviewSandbox resolves the read-only plan for a run: the agent version, the
// image build decision, and the home volume, using only local inspection. It
// builds no image, creates no volume, writes no state, starts no VM, prompts,
// and makes no network request. The agent version is the explicit pin, else the
// release the existing image reports through its label, else the recorded cache
// — recovered for reporting only, never recorded. A user-provided agent ignores
// the pin and never recovers a version, matching a real run.
func PreviewSandbox(ctx context.Context, opts options.RunOptions, ui termio.UI) (Preview, error) {
	cwd, err := resolveWorkspaceDir()
	if err != nil {
		return Preview{}, err
	}

	a, ok := agent.Lookup(opts.Agent)
	if !ok {
		return Preview{}, fmt.Errorf("unknown agent %q", opts.Agent)
	}
	identity, err := git.ResolveIdentity(cwd)
	if err != nil {
		return Preview{}, err
	}
	// Read-only adoption: apply=false so the preview never moves state, but it
	// still resolves against the location a real run would find first (the
	// legacy slug while a migration is pending), matching the volume dry runs.
	adoption, err := AdoptLegacyIdentity(ctx, msb.Get(), identity, a.Name(), false, ui)
	if err != nil {
		return Preview{}, err
	}
	k := state.Key{Slug: adoption.Slug, Agent: a.Name()}

	_, userProvided := ResolveAgentVersionForBuild(k, opts.AgentVersion)
	version := previewAgentVersion(ctx, k, opts, userProvided)
	// A known version from the project's existing image label or its recorded
	// cache is what a real run would bake without running the upgrade check, so
	// disclose it. A pin is never upgraded and a user-provided agent is never
	// upgrade-checked, so neither carries the note.
	upgradeCheckSkipped := version != "" && opts.AgentVersion == "" && !userProvided

	img, err := image.PreviewImage(
		ctx, a, k.Slug,
		buildOptions(opts, version, opts.Rebuild, userProvided),
		ui,
	)
	if err != nil {
		return Preview{}, err
	}

	client := msb.Get()
	homeVolume, createHome, err := volume.PreviewHomeVolume(ctx, client, k, liveHomeVolume(ctx, client, k, ui))
	if err != nil {
		return Preview{}, err
	}

	return Preview{
		AgentVersion:          version,
		ResolveVersionAtBuild: img.ResolveVersionAtBuild,
		UpgradeCheckSkipped:   upgradeCheckSkipped,
		ImageTag:              img.Tag,
		BuildImage:            img.NeedsBuild,
		BuildReasons:          img.Reasons,
		HomeVolume:            homeVolume,
		CreateHome:            createHome,
	}, nil
}

// previewAgentVersion decides the agent version the preview reports, matching
// resolveBuildVersion's precedence. A user-provided agent is not owned by the
// tool: the preview ignores any --agent-version pin and reports the recorded
// version, recovering none from the image label (a real build resolves a missing
// version itself). Any other agent takes the pin, then the release the existing
// image reports through its label, then the recorded cache as the fallback when
// the image yields nothing.
func previewAgentVersion(ctx context.Context, k state.Key, opts options.RunOptions, userProvided bool) string {
	if userProvided {
		return currentAgentVersion(k)
	}
	if opts.AgentVersion != "" {
		return opts.AgentVersion
	}
	if recovered, ok := existingAgentVersion(ctx, k.Slug, k.Agent); ok {
		return recovered
	}
	return currentAgentVersion(k)
}
