package volume

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Mount point constants used by volume operations (prefill, copy, edit).
const (
	srcMount = "/src"
	dstMount = "/dst"
)

// HomeVolumeName generates a volume name for the given project/agent key using
// the standard home volume prefix and a UTC timestamp.
func HomeVolumeName(k state.Key) string {
	ts := time.Now().UTC().Format("20060102T150405")
	return naming.HomePrefix + k.Slug + "-" + k.Agent + "-" + ts
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
	if err := image.EnsureLoaded(ctx, client, info, ui); err != nil {
		return fmt.Errorf("load runner image: %w", err)
	}
	prefillName := naming.TaskPrefix + k.Slug + "-" + k.Agent + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	mountConfig := msbSdk.Mount.Named(volumeName, msbSdk.MountOptions{})

	spin := ui.Spinner("Preparing home volume")
	sb, err := client.CreateSandbox(ctx, prefillName,
		msbSdk.WithImage(info.Tag),
		msbSdk.WithMounts(map[string]msbSdk.MountConfig{
			"/mnt/home": mountConfig,
		}),
		msbSdk.WithReplace(),
	)
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("create prefill sandbox: %w", err)
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		if destroyErr := sb.Destroy(destroyCtx); destroyErr != nil &&
			!msb.IsReplaced(destroyErr) && !msb.IsNotFound(destroyErr) {
			ui.Warnf("failed to destroy prefill sandbox %s: %v", prefillName, destroyErr)
		}
		_ = sb.Close()
	}()

	out, err := sb.Exec(ctx, "sh", []string{"-c", "cp -a /home/dev/. /mnt/home/ && chown -R dev:dev /mnt/home"})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("prefill cp: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("prefill cp failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}

// ApplyHomeAction executes the migrate/reset action the user chose, always
// keeping the old volume. It returns the home volume to mount for this run.
//
// State is only written when the action actually executes successfully; a real
// run creates the new volume, prefills it (spawning a VM), copies files for
// migrate (spawning another VM), and then records the new volume and current
// digest. Both --dry-run and --dry-run-vm simulate the action without changing
// state: --dry-run performs no writes at all, and --dry-run-vm additionally
// never spawns a VM, so the chosen action is left uncommitted.
func (vm *Manager) ApplyHomeAction(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume string,
	info image.ImageInfo,
	action VolumeAction,
	opts options.RunOptions,
	ui termio.UI,
) (string, error) {
	if action == ActionKeep {
		if opts.DryRun {
			return oldVolume, nil
		}
		if err := vm.RecordHomeImage(k, info.Digest, ui); err != nil {
			ui.Warnf("failed to record image digest: %v", err)
		}
		return oldVolume, nil
	}

	label := actionLabel(action)
	if opts.DryRun {
		ui.Infof("dry-run: Would %s home volume (old %q kept)", label, oldVolume)
		return oldVolume, nil
	}
	if opts.DryRunVM {
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

	// Change only the home volume identity and digest, keeping every other field
	// (env/secret/network/mount fingerprints and the upgrade state) that a home
	// action does not affect: dropping them would trigger a false VM rebuild or
	// re-fire the upgrade prompt on the next startup (see
	// EnvChanged/SecretsChanged/NetworkChanged/MountsChanged treating empty
	// state as "changed"). The write runs under the home lock but is guarded by a
	// compare-and-set on the resolved home volume, since the msb I/O above ran
	// without the lock: if another writer changed home_volume in the meantime,
	// abort and drop the new volume rather than clobber it.
	if err := state.WithHomeState(k, func(cur *state.HomeState, _ bool) (bool, error) {
		if cur.HomeVolume != oldVolume {
			return false, fmt.Errorf(
				"home volume changed concurrently (want %q, have %q)",
				oldVolume,
				cur.HomeVolume,
			)
		}
		cur.HomeVolume = newName
		cur.ImageDigest = info.Digest
		return true, nil
	}); err != nil {
		vm.cleanupVolume(ctx, client, newName, ui)
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

// CopyVolume copies the contents of the old home volume on top of the newly
// created one via a throwaway sandbox that mounts both.
func (vm *Manager) CopyVolume(
	ctx context.Context,
	client msb.Client,
	k state.Key, oldVolume, newVolume string,
	info image.ImageInfo,
	ui termio.UI,
) error {
	if err := image.EnsureLoaded(ctx, client, info, ui); err != nil {
		return fmt.Errorf("load runner image: %w", err)
	}
	copySbName := naming.TaskPrefix + k.Slug + "-" + k.Agent + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	copySb, err := client.CreateSandbox(ctx, copySbName,
		msbSdk.WithImage(info.Tag),
		msbSdk.WithMounts(map[string]msbSdk.MountConfig{
			srcMount: msbSdk.Mount.Named(oldVolume, msbSdk.MountOptions{}),
			dstMount: msbSdk.Mount.Named(newVolume, msbSdk.MountOptions{}),
		}),
		msbSdk.WithReplace(),
	)
	if err != nil {
		return fmt.Errorf("create copy sandbox: %w", err)
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		if destroyErr := copySb.Destroy(destroyCtx); destroyErr != nil &&
			!msb.IsReplaced(destroyErr) && !msb.IsNotFound(destroyErr) {
			ui.Warnf("failed to destroy copy sandbox %s: %v", copySbName, destroyErr)
		}
		_ = copySb.Close()
	}()

	spin := ui.Spinner("Copying files from existing home volume")
	out, err := copySb.Exec(ctx, "sh", []string{"-c", "cp -a /src/. /dst/ && chown -R dev:dev /dst"})
	if err != nil {
		spin.StopError(err)
		return fmt.Errorf("copy files: %w", err)
	}
	if !out.Success() {
		err := fmt.Errorf("copy failed (exit %d): %s", out.ExitCode(), out.Stderr())
		spin.StopError(err)
		return err
	}
	spin.Stop()
	return nil
}
