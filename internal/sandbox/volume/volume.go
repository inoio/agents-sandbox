package volume

import (
	"context"
	"fmt"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Mount point constants used by volume operations (prefill, copy, edit).
const (
	homeMount = "/mnt/home"
	srcMount  = "/src"
	dstMount  = "/dst"
)

// homeVolumeClock is the clock HomeVolumeName timestamps from. It is a seam so
// tests can control the generated names.
var homeVolumeClock = time.Now

// HomeVolumeName generates a prefix-free volume name for the given
// project/agent key with a UTC timestamp.
func HomeVolumeName(k state.Key) string {
	ts := homeVolumeClock().UTC().Format("20060102T150405")
	return naming.HomeVolumeName(k.Slug, k.Agent, ts)
}

// Manager manages home-volume lifecycle for projects.
type Manager struct {
	ui termio.UI
}

// NewManager returns a volume manager backed by the given UI.
func NewManager(ui termio.UI) *Manager {
	return &Manager{ui: ui}
}

// PrefillVolume builds a throwaway sandbox from the given image, mounts the
// target home volume at /mnt/home, and copies the image's home directory onto it.
// The throwaway sandbox is stopped and removed afterwards.
func (vm *Manager) PrefillVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key, volumeName string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	return withThrowawaySandbox(ctx, client, k, info, throwawaySandbox{
		purpose: "prefill",
		mounts: map[string]msbSdk.MountConfig{
			homeMount: msbSdk.Mount.Named(volumeName, msbSdk.MountOptions{}),
		},
		spinnerLabel: "Preparing home volume",
	}, ui, func(sb msb.Sandbox, spin termio.Spinner) error {
		return runShell(ctx, sb, spin, shellStep{
			execErrPrefix: "prefill cp",
			failurePrefix: "prefill cp failed",
			script:        "cp -a /home/dev/. " + homeMount + "/ && chown -R dev:dev " + homeMount,
		})
	})
}

// ApplyHomeAction executes the migrate/reset action the user chose, always
// keeping the old volume. It returns the home volume to mount for this run.
//
// State is only written when the action actually executes successfully; a real
// run creates the new volume, prefills it (spawning a VM), copies files for
// migrate (spawning another VM), and then records the new volume and current
// digest. A skipVM prepare leaves a migrate or reset action uncommitted, because
// the prefill and the copy would spawn a VM; ActionKeep still records the digest.
func (vm *Manager) ApplyHomeAction(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume string,
	info image.ImageInfo,
	action VolumeAction,
	skipVM bool,
	ui termio.UI,
) (string, error) {
	if action == ActionKeep {
		if err := vm.RecordHomeImage(k, info.Digest, ui); err != nil {
			ui.Warnf("failed to record image digest: %v", err)
		}
		return oldVolume, nil
	}

	label := actionLabel(action)
	if skipVM {
		ui.Infof("dry-run-vm: %s deferred; not applied without running a VM (old %q kept)", label, oldVolume)
		return oldVolume, nil
	}

	newVol, err := client.CreateVolume(ctx, HomeVolumeName(k),
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume: %w", err)
	}
	newName := newVol.Name()

	if err := vm.PrefillVolume(ctx, client, k, newName, info, ui); err != nil {
		vm.cleanupVolume(ctx, client, newName, ui)
		return "", err
	}

	if action == ActionMigrate {
		if err := vm.CopyVolume(ctx, client, k, oldVolume, newName, info, ui); err != nil {
			vm.cleanupVolume(ctx, client, newName, ui)
			return "", err
		}
	}

	// ResolveHomeVolume settles any recovered-volume marker before a home action
	// runs, so a marker present now was set concurrently and must be kept.
	if err := commitNewHomeVolume(k, oldVolume, newName, info.Digest, false); err != nil {
		vm.cleanupUncommittedVolume(ctx, client, newName, err, ui)
		return "", err
	}
	ui.Infof("%s to new home volume %q (old %q kept)", label, newName, oldVolume)
	return newName, nil
}

// cleanupVolume removes the given volume best-effort after a failed operation
// that created it, so an uncommitted volume does not linger as an orphan.
func (vm *Manager) cleanupVolume(ctx context.Context, client msb.Client, name string, ui termio.UI) {
	if err := client.RemoveVolume(ctx, name); err != nil {
		ui.Warnf("failed to remove new volume %q after error: %v", name, err)
	}
}

// cleanupUncommittedVolume removes name after a failed commit, unless the
// commit error means the rename already installed the state that names it (a
// post-rename durability failure): deleting the volume then would leave the
// saved state pointing at a removed volume.
func (vm *Manager) cleanupUncommittedVolume(
	ctx context.Context, client msb.Client, name string, commitErr error, ui termio.UI,
) {
	if state.WriteInstalled(commitErr) {
		return
	}
	vm.cleanupVolume(ctx, client, name, ui)
}

// CopyVolume copies the contents of the old home volume on top of the newly
// created one via a throwaway sandbox that mounts both.
func (vm *Manager) CopyVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume, newVolume string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	return withThrowawaySandbox(ctx, client, k, info, throwawaySandbox{
		purpose: "copy",
		mounts: map[string]msbSdk.MountConfig{
			srcMount: msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
			dstMount: msbSdk.Mount.Named(newVolume, msbSdk.MountOptions{}),
		},
		// The copy spinner starts only once the sandbox is up.
		spinnerLabel: "",
	}, ui, func(sb msb.Sandbox, _ termio.Spinner) error {
		spin := ui.Spinner("Copying files from existing home volume")
		return runShell(ctx, sb, spin, shellStep{
			execErrPrefix: "copy files",
			failurePrefix: "copy failed",
			script:        "cp -a " + srcMount + "/. " + dstMount + "/ && chown -R dev:dev " + dstMount,
		})
	})
}
