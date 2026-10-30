package volume

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// ResolveHomeVolume determines the home volume to mount for a project run.
//
// When the state file resolves to a volume that still exists, that volume is
// reused. When it records a pending RecoveredHomeVolume (a VM-only identity
// adoption, #144), that volume is recovered into the state. Otherwise the live
// VM's mounted volume (recoveredVolume) is adopted if it still exists, so a lost
// state file does not mint a duplicate volume and orphan the previous one (issue
// #219). When none is available it falls back to EnsureNewHome.
//
// skipVM passes through to EnsureNewHome: a fresh home is neither created nor
// recorded, because its prefill spawns a VM; the first real run creates it.
func (vm *Manager) ResolveHomeVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	skipVM bool,
	fingerprints state.Fingerprints,
	recoveredVolume string,
	ui termio.UI,
) (string, state.HomeState, error) {
	st, err := state.ReadState(k)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("could not read state file for project %q: %v", k.Slug, err)
		}
		return vm.recoverOrCreateHome(ctx, client, k, info, skipVM, fingerprints, recoveredVolume, "", ui)
	}

	// A VM-only identity adoption (#144) destroyed the legacy VM and recorded its
	// home volume here first, because the VM is then gone and cannot be read.
	// Finish that recovery so the volume becomes the project's HomeVolume with
	// the current digest and fingerprints.
	if st.RecoveredHomeVolume != "" {
		return vm.recoverOrCreateHome(
			ctx,
			client,
			k,
			info,
			skipVM,
			fingerprints,
			st.RecoveredHomeVolume,
			st.HomeVolume,
			ui,
		)
	}

	if _, err := client.GetVolume(ctx, st.HomeVolume); err != nil {
		if !msb.IsVolumeNotFound(err) {
			return "", state.HomeState{}, fmt.Errorf("checking home volume %q: %w", st.HomeVolume, err)
		}
		ui.Warnf("existing home volume %q not found, creating fresh", st.HomeVolume)
		return vm.recoverOrCreateHome(ctx, client, k, info, skipVM, fingerprints, recoveredVolume, st.HomeVolume, ui)
	}

	return st.HomeVolume, *st, nil
}

// recoverOrCreateHome adopts the home volume still mounted by the project VM
// when its state file is unusable, writing a recovered state so the volume is
// not duplicated and the previous one orphaned. It falls back to a fresh
// prefill when no surviving volume can be confirmed.
//
// The rebuild runs through RecoverHomeState, the only wholesale writer: it
// repairs a missing or unparsable document, and a still-readable prior
// document (recorded volume gone) is carried forward so its fingerprints, which
// describe the live VM rather than the home volume, survive and dropping them
// does not trigger a spurious rebuild (see specs/reconfig-decision.md).
func (vm *Manager) recoverOrCreateHome(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	skipVM bool,
	fingerprints state.Fingerprints,
	recoveredVolume string,
	observedHomeVolume string,
	ui termio.UI,
) (string, state.HomeState, error) {
	recovered, ok, err := confirmedRecoveredVolume(ctx, client, recoveredVolume)
	if err != nil {
		return "", state.HomeState{}, err
	}
	if !ok {
		volName, hs, err := vm.EnsureNewHome(ctx, client, k, info, skipVM, fingerprints, observedHomeVolume, ui)
		if err == nil {
			clearAdoptionClaim(k, recoveredVolume, ui)
		}
		return volName, hs, err
	}

	ui.Warnf(
		"state for project %q needs home-volume recovery; recovering home volume %q",
		k.Slug,
		recovered,
	)
	var recoveredState state.HomeState
	callbackRan := false
	if err := state.RecoverHomeState(k, func(cur *state.HomeState, exists bool) (bool, error) {
		callbackRan = true
		recoveredState = recoveredHomeState(recovered, info, priorOrNil(cur, exists), fingerprints)
		*cur = recoveredState
		return true, nil
	}); err != nil {
		ui.Warnf("failed to recover state file: %v", err)
	} else {
		// The recovered volume is now the project's HomeVolume and the marker is
		// gone, so the adoption claim that recorded ownership is spent.
		clearAdoptionClaim(k, recovered, ui)
	}
	if !callbackRan {
		// The transaction failed before it could run the callback (lock or read
		// failure): still return a usable state rebuilt from the recovered
		// volume alone, without prior carry-forward.
		recoveredState = recoveredHomeState(recovered, info, nil, fingerprints)
	}
	return recovered, recoveredState, nil
}

// clearAdoptionClaim retires a spent adoption claim (#144) after the recovered
// volume has been folded into the project's own state. Best-effort: a stale
// claim is harmless once the legacy VM is gone, so a failure only warns.
func clearAdoptionClaim(k state.Key, volume string, ui termio.UI) {
	if volume == "" {
		return
	}
	if err := state.ClearAdoptedHomeVolume(k.Slug, volume); err != nil {
		ui.Warnf("failed to clear the adoption claim for volume %q: %v", volume, err)
	}
}

// priorOrNil returns the readable prior document for carry-forward, or nil when
// recovery is rebuilding from scratch. An identity-adoption marker
// (RecoveredHomeVolume set, #144) is not a real prior: it holds no fingerprints
// or upgrade fields, so treating it as one would copy empty fingerprints forward.
// Discarding it also seeds the desired fingerprints, the #183 rule for a state
// whose contents were effectively lost.
func priorOrNil(cur *state.HomeState, exists bool) *state.HomeState {
	if !exists || cur.RecoveredHomeVolume != "" {
		return nil
	}
	return cur
}

// confirmedRecoveredVolume returns the candidate volume when it is non-empty and
// still exists, so recovery only adopts a volume that can actually be mounted.
// Absence (the SDK's volume-not-found error) reports "not confirmed" with a nil
// error, falling back to a fresh prefill. Any other lookup error is a transient
// failure and is returned so the caller fails closed rather than duplicating the
// volume.
func confirmedRecoveredVolume(ctx context.Context, client msb.Client, candidate string) (string, bool, error) {
	if candidate == "" {
		return "", false, nil
	}
	if _, err := client.GetVolume(ctx, candidate); err != nil {
		if !msb.IsVolumeNotFound(err) {
			return "", false, fmt.Errorf("checking recovered home volume %q: %w", candidate, err)
		}
		return "", false, nil
	}
	return candidate, true, nil
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

// recoverHomeState is the wholesale state transaction a fresh home volume's
// state is written through. It is a seam: a state write fails only for
// environmental reasons a test filesystem cannot force (a read-only state
// directory, a full disk, a durability error, or an inode at the state path),
// so a test substitutes a failing writer to exercise the error path.
var recoverHomeState = state.RecoverHomeState

// EnsureNewHome creates a fresh home volume from the image and writes the state.
// It seeds the state with the desired config fingerprints so a fresh state is
// never fingerprint-less while its VM exists (issue #183).
//
// Under skipVM nothing is created or written: the prefill spawns a throwaway
// VM, and recording an unprefilled volume would make every later run reuse an
// empty home (#429). The would-be name and state are returned for the preview.
//
// A failed state write surfaces the error and removes the volume it just
// created, so a run never proceeds against a volume the launcher has no record
// of (#428). The volume is kept when the write error reports an
// installed-but-not-durable document (state.WriteInstalled): the installed state
// already names it, so rolling it back would strand the state on a removed
// volume (see specs/atomicwrite-durability.md).
//
// The commit is a compare-and-set against observedHomeVolume, the home volume
// the state named when the caller decided a fresh volume is needed ("" when the
// state was missing or unreadable) (#431): when a concurrent session committed
// its own fresh volume since, that volume is adopted and this one removed, so
// neither is left unrecorded.
func (vm *Manager) EnsureNewHome(
	ctx context.Context,
	client msb.Client,
	k state.Key,
	info image.ImageInfo,
	skipVM bool,
	fingerprints state.Fingerprints,
	observedHomeVolume string,
	ui termio.UI,
) (string, state.HomeState, error) {
	if skipVM {
		volName := HomeVolumeName(k)
		hs := fingerprints.Apply(state.NewHomeState(volName, info.Digest))
		ui.Infof("dry-run-vm: would create and prefill home volume %q (deferred to the next run with a VM)", volName)
		return volName, hs, nil
	}

	volName, err := createHomeVolume(ctx, client, k)
	if err != nil {
		return "", state.HomeState{}, err
	}

	if err := vm.PrefillVolume(ctx, client, k, volName, info, ui); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		vm.cleanupVolume(cleanupCtx, client, volName, ui)
		return "", state.HomeState{}, err
	}

	hs := fingerprints.Apply(state.NewHomeState(volName, info.Digest))
	var concurrentState *state.HomeState
	// Replace wholesale through the recovery transaction: a fresh home volume's
	// state is authoritative, and the path is also reached to repair a malformed
	// state file.
	if err := recoverHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		if committedConcurrently(cur, observedHomeVolume) {
			adopted := *cur
			concurrentState = &adopted
			// gomutants:disable-next-line RETURN_TRUE reason="writing back the unchanged concurrent document leaves the same state on disk"
			return false, nil
		}
		*cur = hs
		return true, nil
	}); err != nil {
		vm.cleanupUncommittedVolume(ctx, client, volName, err, ui)
		return "", state.HomeState{}, err
	}
	if concurrentState != nil {
		ui.Infof("home volume %q was created concurrently, using it", concurrentState.HomeVolume)
		vm.cleanupVolume(ctx, client, volName, ui)
		return concurrentState.HomeVolume, *concurrentState, nil
	}
	return volName, hs, nil
}

// committedConcurrently reports whether the state names a home volume other
// than the observed one, i.e. another session committed a fresh volume since.
// A missing or unparsable document reads as an empty HomeVolume.
func committedConcurrently(cur *state.HomeState, observedHomeVolume string) bool {
	return cur.HomeVolume != "" && cur.HomeVolume != observedHomeVolume
}

// homeVolumeNameAttempts bounds the retries when a generated home volume name is
// already taken.
const homeVolumeNameAttempts = 3

// homeVolumeNameGranularity is the timestamp resolution of HomeVolumeName:
// waiting this long yields a new name.
const homeVolumeNameGranularity = time.Second

// sleepForNextHomeVolumeName is time.Sleep, a seam so tests do not sleep.
var sleepForNextHomeVolumeName = time.Sleep

// createHomeVolume creates a fresh home volume and returns its name.
// HomeVolumeName has second granularity, so two sessions starting in the same
// second generate the same name (#431); the loser retries with the next name.
func createHomeVolume(ctx context.Context, client msb.Client, k state.Key) (string, error) {
	var err error
	for attempt := 1; ; attempt++ {
		volName := HomeVolumeName(k)
		_, err = client.CreateVolume(ctx, volName, msbSdk.WithVolumeKind(msbSdk.VolumeKindDir))
		if err == nil {
			return volName, nil
		}
		if !msb.IsVolumeAlreadyExists(err) || attempt == homeVolumeNameAttempts {
			return "", fmt.Errorf("create volume %s: %w", volName, err)
		}
		sleepForNextHomeVolumeName(homeVolumeNameGranularity)
	}
}

// RecordHomeImage updates the stored image digest for a project to the
// current digest, preserving the tracked home volume. It is called after the
// image-change prompt so subsequent runs no longer detect a mismatch and do
// not re-prompt. Missing state is a no-op.
func (vm *Manager) RecordHomeImage(k state.Key, currentDigest string, ui termio.UI) error {
	if err := state.WithHomeState(k, func(cur *state.HomeState, exists bool) (bool, error) {
		if !exists {
			return false, nil
		}
		cur.ImageDigest = currentDigest
		return true, nil
	}); err != nil {
		ui.Warnf("failed to update state file: %v", err)
		return err
	}
	return nil
}
