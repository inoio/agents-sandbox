// Package readback reads a project's sandbox home volume back on the host, so
// the learn-mode analyser can derive config and secret facts without a channel.
// See specs/onboarding/learn-mode/home-read-back.md.
package readback

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
)

// VolumeInspector is the read-back's volume dependency: the one volume lookup
// that turns a recorded volume name into its host filesystem path.
type VolumeInspector interface {
	InspectVolume(ctx context.Context, name string) (volume.VolumeDetail, error)
}

// volumeInspector is the VolumeInspector production reads volumes through. It
// is a package seam so tests inject a double instead of reaching a live
// microsandbox daemon.
var volumeInspector VolumeInspector = productionInspector{}

// productionInspector resolves volumes through the volume package, which asks
// microsandbox.
type productionInspector struct{}

func (productionInspector) InspectVolume(ctx context.Context, name string) (volume.VolumeDetail, error) {
	return volume.InspectVolume(ctx, name)
}

// HomeVolumePath returns the host filesystem path of the given project's home
// volume, from the volume recorded in state. A state file that is missing or
// records no home volume is an error naming the project whose volume is
// unknown: the read-back has nothing to look up.
func HomeVolumePath(ctx context.Context, k state.Key) (string, error) {
	homeState, err := state.ReadState(k)
	if err != nil && !errors.Is(err, state.ErrStateNotFound) {
		return "", fmt.Errorf("read state for %s/%s: %w", k.Slug, k.Agent, err)
	}
	if homeState == nil || homeState.HomeVolume == "" {
		return "", fmt.Errorf(
			"no home volume recorded for project %q agent %q: %w",
			k.Slug, k.Agent, state.ErrStateNotFound,
		)
	}
	detail, err := volumeInspector.InspectVolume(ctx, homeState.HomeVolume)
	if err != nil {
		return "", fmt.Errorf("inspect home volume %q: %w", homeState.HomeVolume, err)
	}
	return detail.Path, nil
}
