package main

import (
	"context"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// projectImageOperation ensures or builds the runner image for the adopted
// project key.
type projectImageOperation func(ctx context.Context, key state.Key) (image.ImageInfo, error)

// adoptAndPrepareImage resolves the project identity, adopts a legacy one like
// PrepareSandbox does, runs the caller's image operation for the adopted key,
// and records the agent release the image bakes.
//
// The adopted slug keys both the image and the state, never re-derived from git:
// adoption may have just moved the state, and a dry run deliberately returns the
// legacy repo slug while the migration stays pending. Adoption runs before the
// dry-run gate so its notice prints; a dry run then returns without touching
// the image, since ensuring one could build it. A failed version record only
// warns: the next run reconciles from the image label.
func adoptAndPrepareImage(
	ctx context.Context,
	paths configpaths.ConfigPaths,
	msbClient msb.Client,
	agentName string,
	dryRun bool,
	ui termio.UI,
	prepareImage projectImageOperation,
) (state.Key, image.ImageInfo, error) {
	identity, err := git.ResolveIdentity(".")
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	adoption, err := sandbox.AdoptLegacyIdentity(ctx, paths, msbClient, identity, agentName, !dryRun, ui)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, fmt.Errorf("adopt legacy identity: %w", err)
	}
	key := state.Key{Slug: adoption.Slug, Agent: agentName}
	if dryRun {
		return key, image.ImageInfo{}, nil
	}
	info, err := prepareImage(ctx, key)
	if err != nil {
		return state.Key{}, image.ImageInfo{}, err
	}
	if err := state.RecordAgentVersion(key, info.AgentVersion); err != nil {
		ui.Warnf("could not persist the agent version: %v (continuing)", err)
	}
	return key, info, nil
}
