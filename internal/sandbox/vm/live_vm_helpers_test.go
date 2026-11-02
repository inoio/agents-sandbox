package vm

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// lookUpAndEnsureProjectVM looks the project VM up through the given msb
// client and ensures it with empty host env and secrets, as the plan-reconfig
// and ensure-vm stages do together.
// A skipVM run leaves the VM alone, so it needs no lookup and no msb mock.
func lookUpAndEnsureProjectVM(
	ctx context.Context,
	client msb.Client,
	opts options.RunOptions,
	info image.ImageInfo, homeVolume, repoPath string,
	k state.Key,
	repoSlug string,
	skipVM bool,
	ui termio.UI,
) (msb.Sandbox, VMBoot, error) {
	var existing msb.SandboxHandle
	if !skipVM {
		var err error
		existing, _, err = FindProjectVM(ctx, client, k)
		if err != nil {
			return nil, VMBootConnected, err
		}
	}
	return EnsureProjectVM(
		ctx,
		client,
		opts,
		info,
		homeVolume,
		repoPath,
		k,
		existing,
		repoSlug,
		skipVM,
		envsecret.EnvSecrets{},
		ui,
	)
}
