package volume

import (
	"context"
	"errors"
	"fmt"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// checkForActiveVMs checks if there are active VMs for the given key.
func checkForActiveVMs(ctx context.Context, k state.Key) error {
	client := msb.Get()
	sandboxes, err := client.ListSandboxes(ctx, nil)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	for _, handle := range sandboxes {
		cfg, _ := handle.Config()
		var labels map[string]string
		if cfg != nil {
			labels = cfg.Labels
		}
		artifact := naming.SandboxIdentity(handle.Name(), labels)
		if artifact.Task {
			continue
		}
		if artifact.Slug == k.Slug && (artifact.Agent == k.Agent || artifact.Agent == "") {
			status := handle.Status()
			if !msb.IsSandboxInactive(status) {
				if msb.IsSandboxActive(status) {
					return fmt.Errorf(
						"VM still running for slug %q -- quit all sessions before migrating or resetting",
						k.Slug,
					)
				}
				return fmt.Errorf(
					"VM status %q is not safely inactive for slug %q -- quit all sessions before migrating or resetting",
					status,
					k.Slug,
				)
			}
		}
	}
	return nil
}

// volDryRunFunc returns the dry-run message string for the given operation.
// It receives the resolved source volume; the project slug and other context
// are captured by the enclosing command's closure.
type volDryRunFunc func(oldVolume string) string

// volMainFunc implements the operation's main behavior after volume
// preflight (state read, active-VM check, volume creation, prefill). It
// receives the resolved source volume and the freshly created target volume;
// the project slug, image tag, and UI are captured by the enclosing command's
// closure, and clients are obtained via msb.Get() when needed.
type volMainFunc func(oldVolume, newVolumeName string) error

// volCallbacks contains the callbacks for each volume operation variant.
type volCallbacks struct {
	dryRun volDryRunFunc
	main   volMainFunc
}

// CmdMigrate creates a new home volume, copies old files on top, updates state.
func CmdMigrate(
	ctx context.Context,
	k state.Key, volumeName string,
	info image.ImageInfo,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, k, volumeName, info, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create volume %q, copy files from %q",
				HomeVolumeName(k),
				oldVolume,
			)
		},
		main: func(oldVolume, newVolumeName string) error {
			if err := NewManager(ui).CopyVolume(
				ctx, msb.Get(), k, oldVolume, newVolumeName, info, ui,
			); err != nil {
				return err
			}
			ui.Infof("migrated to new home volume %q (files copied from %q)", newVolumeName, oldVolume)
			return nil
		},
	}, ui)
}

// CmdReset creates a new home volume from image only, no copy.
func CmdReset(
	ctx context.Context,
	k state.Key, volumeName string,
	info image.ImageInfo,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, k, volumeName, info, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create fresh volume %q, remove %q",
				HomeVolumeName(k),
				oldVolume,
			)
		},
		main: func(_ string, newVolumeName string) error {
			ui.Infof("reset to new home volume %q", newVolumeName)
			return nil
		},
	}, ui)
}

// CmdEdit creates a new volume alongside the old for manual transfer.
func CmdEdit(
	ctx context.Context,
	k state.Key, volumeName string,
	info image.ImageInfo,
	rmOld, dryRun bool,
	ui termio.UI,
) error {
	return volumeOp(ctx, k, volumeName, info, rmOld, dryRun, volCallbacks{
		dryRun: func(oldVolume string) string {
			return fmt.Sprintf(
				"dry-run: Would create volume %q alongside %q for manual transfer",
				HomeVolumeName(k),
				oldVolume,
			)
		},
		main: func(oldVolume, newVolumeName string) error {
			if err := editVolumesInteractively(ctx, k, oldVolume, newVolumeName, info, ui); err != nil {
				return err
			}
			ui.Infof("exited interactive session, new volume: %q", newVolumeName)
			return nil
		},
	}, ui)
}

// volumeOp is the shared implementation for migrate, reset, and edit operations.
func volumeOp(
	ctx context.Context,
	k state.Key, volumeName string,
	info image.ImageInfo,
	rmOld, dryRun bool,
	cbs volCallbacks,
	ui termio.UI,
) error {
	client := msb.Get()

	st, err := state.ReadState(k)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			return fmt.Errorf("no state file found for project %q", k.Slug)
		}
		return fmt.Errorf("read state: %w", err)
	}

	oldVolume := volumeName
	if oldVolume == "" {
		oldVolume = st.HomeVolume
		if oldVolume == "" {
			// A VM-only identity adoption (#144) records the legacy home volume in
			// RecoveredHomeVolume before destroying the legacy VM, leaving HomeVolume
			// empty until home resolution runs. Use the pending volume as the source
			// so a first volume op works instead of failing.
			oldVolume = st.RecoveredHomeVolume
		}
		if oldVolume == "" {
			return errors.New("no volume to operate on: state has no home_volume set")
		}
	}

	if vmErr := checkForActiveVMs(ctx, k); vmErr != nil {
		return vmErr
	}

	if dryRun {
		ui.Info(cbs.dryRun(oldVolume))
		return nil
	}

	newVolumeName := HomeVolumeName(k)
	newVol, err := client.CreateVolume(ctx, newVolumeName,
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return fmt.Errorf("create volume %s: %w", newVolumeName, err)
	}

	vm := NewManager(ui)
	if err := vm.PrefillVolume(ctx, client, k, newVol.Name(), info, ui); err != nil {
		return fmt.Errorf("prefill new volume: %w", err)
	}

	if err := cbs.main(oldVolume, newVolumeName); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		vm.cleanupVolume(cleanupCtx, client, newVolumeName, ui)
		return err
	}

	// The operation replaced the volume a pending recovered-volume marker may
	// still record (#144), so commitNewHomeVolume clears it.
	if err := commitNewHomeVolume(k, st.HomeVolume, newVolumeName, info.Digest, true); err != nil {
		vm.cleanupUncommittedVolume(ctx, client, newVolumeName, err, ui)
		return err
	}
	// If the operation replaced a volume an identity adoption preserved, its
	// claim is spent; retire it so it is not left stale (#144).
	clearAdoptionClaim(k, oldVolume, ui)

	if rmOld {
		if err := client.RemoveVolume(ctx, oldVolume); err != nil {
			ui.Warnf("failed to remove old volume %q: %v", oldVolume, err)
		} else {
			ui.Infof("removed old volume %q", oldVolume)
		}
	}

	return nil
}

// editVolumesInteractively attaches a login shell to a throwaway sandbox that
// mounts the old volume at /src and the new one at /dst. A failing shell is
// only reported, since the user may have finished the transfer regardless.
func editVolumesInteractively(
	ctx context.Context,
	k state.Key, oldVolume, newVolumeName string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	return withThrowawaySandbox(ctx, msb.Get(), k, info, throwawaySandbox{
		purpose: "edit",
		mounts: map[string]msbSdk.MountConfig{
			srcMount: msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
			dstMount: msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{}),
		},
		spinnerLabel: "Starting interactive session with both volumes",
	}, ui, func(sb msb.Sandbox, spin termio.Spinner) error {
		ui.Infof("Both volumes mounted in session:")
		ui.Infof("  Old volume (source):  /src")
		ui.Infof("  New volume (target):  /dst")
		ui.Infof("Type 'exit' to finish and return to the host.")
		spin.Stop()
		if _, err := sb.Attach(ctx, "/bin/bash", "-l"); err != nil {
			ui.Warnf("shell exited with error: %v", err)
		}
		return nil
	})
}
