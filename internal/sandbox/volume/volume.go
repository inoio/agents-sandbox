package volume

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/msb"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/naming"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/options"
	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"
)

// Action constants for the home volume resolution prompt.
const (
	actionKeep    = "1"
	actionMigrate = "2"
	actionReset   = "3"
	actionQuit    = "4"

	actionMigrateLabel = "migrate"
	actionResetLabel   = "reset"

	// Mount point constants used by volume operations (prefill, copy, edit).
	srcMount     = "/src"
	dstMount     = "/dst"
	tmpMountPath = "/tmp"
)

// HomeVolumeName generates a volume name for the given project slug using the
// standard home volume prefix and a UTC timestamp.
func HomeVolumeName(projectSlug string) string {
	ts := time.Now().UTC().Format("20060102T150405")
	return naming.HomePrefix + projectSlug + "-" + ts
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
// target home volume at /mnt/home and copies the image's home directory onto it.
// The throwaway sandbox is stopped and removed afterwards.
func (vm *Manager) PrefillVolume(
	ctx context.Context,
	client msb.Client,
	projectSlug, volumeName, imageTag string,
	ui termio.UI,
) error {
	prefillName := naming.TaskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	mountConfig := msbSdk.Mount.Named(volumeName, msbSdk.MountOptions{})

	spin := ui.Spinner("Preparing home volume")
	sb, err := client.CreateSandbox(ctx, prefillName,
		msbSdk.WithImage(imageTag),
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
		stopCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		_ = sb.Stop(stopCtx)
		_ = sb.Close()
		_ = client.RemoveSandbox(context.Background(), prefillName)
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

	hs := state.HomeState{
		HomeVolume:  volName,
		ImageDigest: imageDigest,
		EnvState:    state.EnvState{},    //nolint:exhaustruct // intentionally zeroed; fingerprint re-established on next apply
		SecretState: state.SecretState{}, //nolint:exhaustruct // intentionally zeroed; fingerprint re-established on next apply
	}
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

// actionLabel returns a human-friendly label for a home volume action.
func actionLabel(action string) string {
	if action == actionReset {
		return actionResetLabel
	}
	if action == actionMigrate {
		return actionMigrateLabel
	}
	return "keep"
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
	projectSlug, oldVolume, imageTag, currentDigest, action string,
	opts options.RunOptions,
	ui termio.UI,
) (string, error) {
	if action == actionKeep {
		if opts.DryRun {
			return oldVolume, nil
		}
		if err := vm.RecordHomeImage(projectSlug, currentDigest, ui); err != nil {
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

	newVol, err := client.CreateVolume(ctx, HomeVolumeName(projectSlug),
		msbSdk.WithVolumeKind(msbSdk.VolumeKindDir),
	)
	if err != nil {
		return "", fmt.Errorf("create volume: %w", err)
	}
	newName := newVol.Name()

	if err := vm.PrefillVolume(ctx, client, projectSlug, newName, imageTag, ui); err != nil {
		vm.cleanupVolume(ctx, client, newName, ui)
		return "", err
	}

	if action == actionMigrate {
		if err := vm.CopyVolume(ctx, client, projectSlug, oldVolume, newName, imageTag, ui); err != nil {
			vm.cleanupVolume(ctx, client, newName, ui)
			return "", err
		}
	}

	newState := state.HomeState{
		HomeVolume:  newName,
		ImageDigest: currentDigest,
		EnvState:    state.EnvState{},    //nolint:exhaustruct // intentionally zeroed; fingerprint re-established on next apply
		SecretState: state.SecretState{}, //nolint:exhaustruct // intentionally zeroed; fingerprint re-established on next apply
	}
	if err := state.WriteState(projectSlug, newState); err != nil {
		ui.Warnf("failed to write state file: %v", err)
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
	projectSlug, oldVolume, newVolume, imageTag string,
	ui termio.UI,
) error {
	copySbName := naming.TaskPrefix + projectSlug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	copySb, err := client.CreateSandbox(ctx, copySbName,
		msbSdk.WithImage(imageTag),
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
		stopCtx, cancel := context.WithTimeout(context.Background(), options.SandboxStopTimeout)
		defer cancel()
		_ = copySb.Stop(stopCtx)
		_ = copySb.Close()
		_ = client.RemoveSandbox(context.Background(), copySbName)
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

// ResolveHomeAction compares the stored image digest with the current one.
// If they match, returns actionKeep immediately.
// If they differ, presents a prompt: keep/migrate/reset/quit.
// In non-interactive mode or with --yes, defaults to actionKeep.
func (vm *Manager) ResolveHomeAction(
	ui termio.UI,
	storedDigest, currentDigest string,
) string {
	if storedDigest == currentDigest {
		return actionKeep
	}

	if !ui.IsInteractive() {
		ui.Infof("non-interactive: using existing home volume")
		return actionKeep
	}

	prompt := "Docker image changed for project. The image's home directory is different from your current one."
	choices := []termio.Choice{
		{Key: actionKeep, Label: "keep", Description: "continue with existing home volume"},
		{Key: actionMigrate, Label: actionMigrateLabel, Description: "create fresh volume, copy all files on top"},
		{
			Key:         actionReset,
			Label:       actionResetLabel,
			Description: "replace with fresh volume from image (lose local changes)",
		},
		{Key: actionQuit, Label: "quit", Description: "exit without starting a session"},
	}
	selected, err := ui.Select(prompt, choices, actionKeep)
	if err != nil {
		ui.Warnf("prompt failed, continuing with existing volume")
		return actionKeep
	}
	return selected
}
