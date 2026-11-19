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

// ErrNoHomeVolume reports that a project's state exists but records no home
// volume, which is distinct from a state file that is missing entirely
// (state.ErrStateNotFound). The read-back needs a volume to look up.
var ErrNoHomeVolume = errors.New("no home volume recorded")

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

// InspectVolume asks the volume package, which asks microsandbox.
//
//deadcode:ignore reason="the volume seam's production method; reached through HomeVolumePath once the S7 run --learn after-run resolves the home volume"
func (productionInspector) InspectVolume(ctx context.Context, name string) (volume.VolumeDetail, error) {
	return volume.InspectVolume(ctx, name)
}

// HomeVolumePath returns the host filesystem path of the given project's home
// volume, from the volume recorded in state. A missing state file wraps
// state.ErrStateNotFound and a state that records no volume wraps
// ErrNoHomeVolume; both name the project whose volume is unknown, because the
// read-back has nothing to look up.
//
//deadcode:ignore reason="read-back API; the S7 run --learn after-run resolves the home-volume path before reading it"
func HomeVolumePath(ctx context.Context, k state.Key) (string, error) {
	homeState, err := state.ReadState(k)
	if err != nil {
		return "", fmt.Errorf("read state for %s/%s: %w", k.Slug, k.Agent, err)
	}
	if homeState.HomeVolume == "" {
		return "", fmt.Errorf(
			"no home volume recorded for project %q agent %q: %w",
			k.Slug, k.Agent, ErrNoHomeVolume,
		)
	}
	detail, err := volumeInspector.InspectVolume(ctx, homeState.HomeVolume)
	if err != nil {
		return "", fmt.Errorf("inspect home volume %q: %w", homeState.HomeVolume, err)
	}
	if detail.Path == "" {
		return "", fmt.Errorf("home volume %q has no host path", homeState.HomeVolume)
	}
	return detail.Path, nil
}
