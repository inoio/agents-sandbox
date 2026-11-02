package prepare

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// loadOpencodeConfigFiles loads opencode's config files from an empty host
// config directory.
func loadOpencodeConfigFiles(t *testing.T, ui termio.UI) *reprovision.ConfigFiles {
	t.Helper()
	configFiles, err := reprovision.LoadConfigFilesForHost(
		opencodeAgent(t),
		t.TempDir(),
		reprovision.VMHomeDir,
		ui,
		true,
	)
	if err != nil {
		t.Fatalf("LoadConfigFilesForHost: %v", err)
	}
	return configFiles
}

// readAndResolveReconfigDecision reads the live project VM through client and
// resolves the reconfiguration decision from it, as planReconfigStage does,
// with empty host env and secrets.
func readAndResolveReconfigDecision(
	ctx context.Context,
	client msb.Client,
	volumes *volume.Manager,
	k state.Key,
	opts options.RunOptions,
	imageInfo image.ImageInfo, homeVolume string, homeState state.HomeState,
	configFiles *reprovision.ConfigFiles,
	ui termio.UI,
) (reconfigDecision, error) {
	live, err := readLiveProjectVM(ctx, client, k, configFiles.Keys, ui)
	if err != nil {
		return reconfigDecision{}, err
	}
	return resolveReconfigDecision(
		ctx,
		volumes,
		k,
		opts,
		imageInfo,
		homeVolume,
		homeState,
		configFiles,
		envsecret.EnvSecrets{},
		live,
		ui,
	)
}
