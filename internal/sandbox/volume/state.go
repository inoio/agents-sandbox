package volume

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/sandbox/msb"
	"github.com/inoio/opencode-sandbox/internal/sandbox/options"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
	"github.com/inoio/opencode-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// ResolveHomeVolume checks the state file for an existing volume reference.
// If found and the volume still exists, returns the volume name and state.
// If not found or the volume does not exist, falls through to EnsureNewHome.
func (vm *Manager) ResolveHomeVolume(
	ctx context.Context,
	client msb.Client,
	projectSlug, imageDigest, imageTag string,
	opts options.RunOptions,
	ui termio.UI,
) (string, state.HomeState, error) {
	st, err := state.ReadState(projectSlug)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("corrupted state file, creating fresh home volume")
		}
		return vm.EnsureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
	}

	_, err = client.GetVolume(ctx, st.HomeVolume)
	if err != nil {
		ui.Warnf("existing home volume %q not found, creating fresh", st.HomeVolume)
		return vm.EnsureNewHome(ctx, client, projectSlug, imageDigest, imageTag, opts, ui)
	}

	return st.HomeVolume, *st, nil
}

// EnsureNewHome creates a fresh home volume from the image and writes the state.
func (vm *Manager) EnsureNewHome(
	ctx context.Context,
	client msb.Client,
	projectSlug, imageDigest, imageTag string,
	opts options.RunOptions,
	ui termio.UI,
) (string, state.HomeState, error) {
	volName := HomeVolumeName(projectSlug)
	vol, err := client.CreateVolume(ctx, volName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", state.HomeState{}, fmt.Errorf("create volume %s: %w", volName, err)
	}

	if !opts.DryRunVM {
		if err := vm.PrefillVolume(ctx, client, projectSlug, vol.Name(), imageTag, ui); err != nil {
			return "", state.HomeState{}, err
		}
	} else {
		ui.Infof("dry-run: Would prefill home volume")
	}

	hs := state.NewHomeState(volName, imageDigest)
	if err := state.WriteState(projectSlug, hs); err != nil {
		ui.Warnf("failed to write state file: %v", err)
	}
	return volName, hs, nil
}

// RecordHomeImage updates the stored image digest for a project to the
// current digest, preserving the tracked home volume. It is called after the
// image-change prompt so subsequent runs no longer detect a mismatch and do
// not re-prompt. Missing state is a no-op.
func (vm *Manager) RecordHomeImage(projectSlug, currentDigest string, ui termio.UI) error {
	st, err := state.ReadState(projectSlug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return nil
		}
		return err
	}
	st.ImageDigest = currentDigest
	if err := state.WriteState(projectSlug, *st); err != nil {
		ui.Warnf("failed to write state file: %v", err)
		return err
	}
	return nil
}
