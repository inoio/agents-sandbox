package volume

import (
	"context"
	"errors"
	"fmt"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// ResolveHomeVolume determines the home volume to mount for a project run.
//
// When the state file resolves to a volume that still exists, that volume is
// reused. Otherwise the live VM's mounted volume (recoveredVolume) is adopted
// if it still exists, so a lost state file does not mint a duplicate volume and
// orphan the previous one (issue #219). When neither is available it falls back
// to EnsureNewHome.
func (vm *Manager) ResolveHomeVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	dryRunVM bool,
	fingerprints state.Fingerprints,
	recoveredVolume string,
	ui termio.UI,
) (string, state.HomeState, error) {
	st, err := state.ReadState(k)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("could not read state file for project %q: %v", k.Slug, err)
		}
		return vm.recoverOrCreateHome(ctx, client, k, info, dryRunVM, fingerprints, recoveredVolume, nil, ui)
	}

	if _, err := client.GetVolume(ctx, st.HomeVolume); err != nil {
		ui.Warnf("existing home volume %q not found, creating fresh", st.HomeVolume)
		return vm.recoverOrCreateHome(ctx, client, k, info, dryRunVM, fingerprints, recoveredVolume, st, ui)
	}

	return st.HomeVolume, *st, nil
}

// recoverOrCreateHome adopts the home volume still mounted by the project VM
// when its state file is unusable, writing a recovered state so the volume is
// not duplicated and the previous one orphaned. It falls back to a fresh
// prefill when no surviving volume can be confirmed.
//
// prior is the still-readable state when one exists (the recorded volume was
// gone) so its env/secret/network/mount fingerprints can be carried forward;
// they describe the live VM, not the home volume, and dropping them would
// trigger a spurious rebuild (see specs/reconfig-decision.md).
func (vm *Manager) recoverOrCreateHome(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	dryRunVM bool,
	fingerprints state.Fingerprints,
	recoveredVolume string,
	prior *state.HomeState,
	ui termio.UI,
) (string, state.HomeState, error) {
	recovered, ok := confirmedRecoveredVolume(ctx, client, recoveredVolume)
	if !ok {
		return vm.EnsureNewHome(ctx, client, k, info, dryRunVM, fingerprints, ui)
	}

	ui.Warnf(
		"state for project %q is missing or its volume is gone; recovering home volume %q from the running VM",
		k.Slug,
		recovered,
	)
	hs := recoveredHomeState(recovered, info, prior, fingerprints)
	// Replace wholesale: this recovery path must repair a malformed state file
	// (which ResolveHomeVolume routes here), so it cannot merge via a read that
	// would fail on the same broken document.
	if err := state.ReplaceHomeState(k, hs); err != nil {
		ui.Warnf("failed to write state file: %v", err)
	}
	return recovered, hs, nil
}

// confirmedRecoveredVolume returns the candidate volume when it is non-empty and
// still exists, so recovery only adopts a volume that can actually be mounted.
func confirmedRecoveredVolume(ctx context.Context, client msb.Client, candidate string) (string, bool) {
	if candidate == "" {
		return "", false
	}
	if _, err := client.GetVolume(ctx, candidate); err != nil {
		return "", false
	}
	return candidate, true
}

// recoveredHomeState builds the state for a recovered volume. It carries forward
// the fingerprints of a still-readable prior state (they describe the live VM),
// or seeds the desired fingerprints when the state was lost entirely (#183), so
// recovery never leaves a fingerprint-less state beside a live VM.
func recoveredHomeState(
	recoveredVolume string,
	info image.ImageInfo,
	prior *state.HomeState,
	fingerprints state.Fingerprints,
) state.HomeState {
	hs := state.NewHomeState(recoveredVolume, info.Digest)
	if prior != nil {
		hs.EnvState = prior.EnvState
		hs.SecretState = prior.SecretState
		hs.NetworkState = prior.NetworkState
		hs.MountState = prior.MountState
		hs.AgentVersion = prior.AgentVersion
		hs.AgentSource = prior.AgentSource
		hs.DockerSource = prior.DockerSource
		hs.OfferedVersions = prior.OfferedVersions
		return hs
	}
	return fingerprints.Apply(hs)
}

// EnsureNewHome creates a fresh home volume from the image and writes the state.
// It seeds the state with the desired config fingerprints so a fresh state is
// never fingerprint-less while its VM exists (issue #183).
func (vm *Manager) EnsureNewHome(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	dryRunVM bool,
	fingerprints state.Fingerprints,
	ui termio.UI,
) (string, state.HomeState, error) {
	volName := HomeVolumeName(k)
	vol, err := client.CreateVolume(ctx, volName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", state.HomeState{}, fmt.Errorf("create volume %s: %w", volName, err)
	}

	if !dryRunVM {
		if err := vm.PrefillVolume(ctx, client, k, vol.Name(), info, ui); err != nil {
			return "", state.HomeState{}, err
		}
	} else {
		ui.Infof("dry-run: Would prefill home volume")
	}

	hs := fingerprints.Apply(state.NewHomeState(volName, info.Digest))
	// Replace wholesale: a fresh home volume's state is authoritative, and the
	// path is also reached to repair a malformed state file.
	if err := state.ReplaceHomeState(k, hs); err != nil {
		ui.Warnf("failed to write state file: %v", err)
	}
	return volName, hs, nil
}

// RecordHomeImage updates the stored image digest for a project to the
// current digest, preserving the tracked home volume. It is called after the
// image-change prompt so subsequent runs no longer detect a mismatch and do
// not re-prompt. Missing state is a no-op.
func (vm *Manager) RecordHomeImage(k state.Key, currentDigest string, ui termio.UI) error {
	if _, err := state.ReadState(k); err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return nil
		}
		return err
	}
	if err := state.UpdateHomeState(k, func(cur *state.HomeState) { cur.ImageDigest = currentDigest }); err != nil {
		ui.Warnf("failed to write state file: %v", err)
		return err
	}
	return nil
}
