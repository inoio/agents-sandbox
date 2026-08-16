package session

import (
	"context"
	"errors"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/git"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/opencode"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/image"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-sandbox/internal/termio"
)

// errUpgradeQuit signals the user chose to abort the session because of the
// pending image upgrade.
var errUpgradeQuit = errors.New("opencode upgrade cancelled") //nolint:err113 // static sentinel intended

type upgradeAction int

const (
	upgradeActionNone upgradeAction = iota
	upgradeActionRebuild
)

// openCodeUpgradeInfo returns the latest opencode release version string.
//
//nolint:gochecknoglobals,gocritic // test seam
var openCodeUpgradeInfo = func(ctx context.Context) (string, error) {
	return opencode.LatestVersion(ctx)
}

// rebuildImageForUpgrade rebuilds the runner image with the latest (or
// requested) opencode version and returns the resulting ImageInfo.
//
//nolint:gochecknoglobals // test seam
var rebuildImageForUpgrade = func(ctx context.Context, ui termio.UI, opts options.RunOptions) (image.ImageInfo, error) {
	return image.EnsureImage(
		ctx,
		git.ProjectSlug(ui),
		image.BuildOptions{Force: true, OpenCodeVersion: opts.OpenCodeVersion},
		ui,
	)
}

// maybePromptOpenCodeUpgrade offers to rebuild the runner image when a newer
// opencode release exists than the version baked into the current image.
func maybePromptOpenCodeUpgrade(
	ctx context.Context,
	ui termio.UI,
	opts options.RunOptions,
	info image.ImageInfo,
) (image.ImageInfo, upgradeAction, error) {
	if opts.Rebuild {
		return info, upgradeActionNone, nil
	}

	if info.OpenCodeVersion == "" {
		ui.Warnf("image has no opencode version label; forcing a rebuild to pin opencode")
		rebuilt, err := rebuildImageForUpgrade(ctx, ui, opts)
		if err != nil {
			ui.Warnf("could not rebuild image to pin opencode: %v (continuing with current image)", err)
			return info, upgradeActionNone, nil
		}
		return rebuilt, upgradeActionRebuild, nil
	}

	latest, err := openCodeUpgradeInfo(ctx)
	if err != nil {
		ui.Warnf("could not check for opencode updates: %v", err)
		return info, upgradeActionNone, nil
	}
	if opencode.VersionCompare(latest, info.OpenCodeVersion) <= 0 {
		return info, upgradeActionNone, nil
	}

	ui.Infof(
		"opencode %s available (image has %s); run 'opencode-sandbox build' to upgrade",
		latest,
		info.OpenCodeVersion,
	) //nolint:lll // user-facing line

	if !ui.IsInteractive() {
		return info, upgradeActionNone, nil
	}

	prompt := fmt.Sprintf("Rebuild the runner image with opencode %s?", latest)
	choices := []termio.Choice{
		{Label: "Rebuild", Key: "r", Description: "Rebuild the image with opencode " + latest},
		{Label: "Keep", Key: "k", Description: "Keep the current image"},
		{Label: "Quit", Key: "q", Description: "Abort this session"},
	}
	choice, err := ui.Select(prompt, choices, "r")
	if err != nil {
		return info, upgradeActionNone, err
	}
	switch choice {
	case "r":
		rebuilt, rebuildErr := rebuildImageForUpgrade(ctx, ui, opts)
		if rebuildErr != nil {
			return info, upgradeActionNone, rebuildErr
		}
		return rebuilt, upgradeActionRebuild, nil
	case "k":
		return info, upgradeActionNone, nil
	case "q":
		return info, upgradeActionNone, errUpgradeQuit
	default:
		return info, upgradeActionNone, nil
	}
}
