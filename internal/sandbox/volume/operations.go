package volume

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
		if !strings.HasPrefix(handle.Name(), naming.VMPrefix) {
			continue
		}
		artifact := naming.ArtifactFor(handle.Name())
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
			client := msb.Get()
			if err := image.EnsureLoaded(ctx, client, info, ui); err != nil {
				return fmt.Errorf("load runner image: %w", err)
			}
			spin := ui.Spinner("Starting interactive session with both volumes")
			editSandboxName := naming.TaskPrefix + k.Slug + "-" + k.Agent + "-" + strconv.FormatInt(
				time.Now().UnixNano(),
				10,
			)
			editOldMount := msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{})
			editNewMount := msbSdk.Mount.Named(newVolumeName, msbSdk.MountOptions{})
			editSb, editErr := client.CreateSandbox(ctx, editSandboxName,
				msbSdk.WithImage(info.Tag),
				msbSdk.WithMounts(map[string]msbSdk.MountConfig{
					srcMount: editOldMount,
					dstMount: editNewMount,
				}),
				msbSdk.WithReplace(),
			)
			if editErr != nil {
				return fmt.Errorf("create edit sandbox: %w", editErr)
			}
			ui.Infof("Both volumes mounted in session:")
			ui.Infof("  Old volume (source):  /src")
			ui.Infof("  New volume (target):  /dst")
			ui.Infof("Type 'exit' to finish and return to the host.")
			spin.Stop()
			_, shellErr := editSb.Attach(ctx, "/bin/bash", "-l")
			if shellErr != nil {
				ui.Warnf("shell exited with error: %v", shellErr)
			}
			// Best-effort cleanup: destroy through the live sandbox so a
			// same-name replacement is never removed by mistake.
			destroyCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
			if destroyErr := editSb.Destroy(destroyCtx); destroyErr != nil &&
				!msb.IsReplaced(destroyErr) && !msb.IsNotFound(destroyErr) {
				ui.Warnf("failed to destroy edit sandbox %s: %v", editSandboxName, destroyErr)
			}
			cancel()
			_ = editSb.Close()
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

	// Change only the home volume identity and digest, keeping every other field
	// (env/secret/network/mount fingerprints and the upgrade state) that a volume
	// operation does not affect: dropping them would trigger a false VM rebuild
	// or re-fire the upgrade prompt on the next startup (see
	// EnvChanged/SecretsChanged/NetworkChanged/MountsChanged treating empty
	// state as "changed"). The write runs under the home lock but is guarded by a
	// compare-and-set on the snapshot home volume, since the msb I/O above ran
	// without the lock: if another writer changed home_volume in the meantime,
	// abort rather than clobber it. A failed commit leaves the new volume
	// uncommitted and unreferenced, so drop it rather than orphan it.
	expectedVolume := st.HomeVolume
	if err := state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		if cur.HomeVolume != expectedVolume {
			return false, fmt.Errorf(
				"home volume changed concurrently (want %q, have %q)",
				expectedVolume,
				cur.HomeVolume,
			)
		}
		cur.HomeVolume = newVolumeName
		cur.ImageDigest = info.Digest
		return true, nil
	}); err != nil {
		vm.cleanupVolume(ctx, client, newVolumeName, ui)
		return err
	}

	if rmOld {
		if err := client.RemoveVolume(ctx, oldVolume); err != nil {
			ui.Warnf("failed to remove old volume %q: %v", oldVolume, err)
		} else {
			ui.Infof("removed old volume %q", oldVolume)
		}
	}

	return nil
}
